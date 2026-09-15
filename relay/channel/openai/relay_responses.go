package openai

import (
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

func OaiResponsesHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	defer service.CloseResponseBodyGracefully(resp)

	// read response body
	var responsesResponse dto.OpenAIResponsesResponse
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeReadResponseBodyFailed, http.StatusInternalServerError)
	}
	err = common.Unmarshal(responseBody, &responsesResponse)
	if err != nil {
		return nil, types.NewOpenAIError(err, types.ErrorCodeBadResponseBody, http.StatusInternalServerError)
	}
	if oaiError := responsesResponse.GetOpenAIError(); oaiError != nil && oaiError.Type != "" {
		return nil, types.WithOpenAIError(*oaiError, resp.StatusCode)
	}

	responseBody = rewriteSGLangResponsesCreatedAt(info, responseBody, "created_at", responsesResponse.CreatedAt)

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := &dto.Usage{}
	service.ApplyResponsesUsage(usage, responsesResponse.Usage)
	// Count actual tool invocations from Output (not tool declarations).
	for _, output := range responsesResponse.Output {
		switch output.Type {
		case dto.BuildInCallWebSearchCall:
			info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
		case dto.BuildInCallFileSearchCall:
			info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
		case dto.BuildInCallFunctionCall:
			info.CountBillableToolCall(dto.BuildInCallFunctionCall, output.Name)
		}
	}

	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	if !relaycommon.IsNonBillableResponsesStatus(responsesResponse.Status) {
		for i := range responsesResponse.Output {
			idx := i
			imageCounter.Observe(&responsesResponse.Output[i], &idx)
		}
	}
	imageCounter.Commit(info)

	return usage, nil
}

func OaiResponsesStreamHandler(c *gin.Context, info *relaycommon.RelayInfo, resp *http.Response) (*dto.Usage, *types.NewAPIError) {
	if resp == nil || resp.Body == nil {
		logger.LogError(c, "invalid response or response body")
		return nil, types.NewError(fmt.Errorf("invalid response"), types.ErrorCodeBadResponse)
	}

	defer service.CloseResponseBodyGracefully(resp)

	accumulator := service.NewResponsesUsageAccumulator(info)
	var streamErr *types.NewAPIError
	terminalSeen := false
	writeFailed := false
	sequenceNumber := 0
	failureResponse := dto.OpenAIResponsesResponse{
		ID: "resp_" + common.GetUUID(), Object: "response",
		CreatedAt: dto.IntValue(time.Now().Unix()), Model: info.UpstreamModelName,
		Status: []byte(`"failed"`), Output: []dto.ResponsesOutput{},
	}

	helper.StreamScannerHandler(c, resp, info, func(data string, sr *helper.StreamResult) {

		// 检查当前数据是否包含 completed 状态和 usage 信息
		var streamResponse dto.ResponsesStreamResponse
		if err := common.UnmarshalJsonStr(data, &streamResponse); err != nil {
			logger.LogError(c, "failed to unmarshal stream response: "+err.Error())
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		if streamResponse.Type == "" || strings.ContainsAny(streamResponse.Type, "\r\n") {
			streamErr = types.NewErrorWithStatusCode(fmt.Errorf("upstream Responses event has an invalid type"), types.ErrorCodeBadResponseBody, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		if streamResponse.SequenceNumber != nil && *streamResponse.SequenceNumber >= sequenceNumber {
			sequenceNumber = *streamResponse.SequenceNumber
		}
		if sequenceNumber < math.MaxInt {
			sequenceNumber++
		}
		if response := streamResponse.Response; response != nil {
			if response.ID != "" {
				failureResponse.ID = response.ID
			}
			if response.CreatedAt != 0 {
				failureResponse.CreatedAt = response.CreatedAt
			}
			if response.Model != "" {
				failureResponse.Model = response.Model
			}
			data = string(rewriteSGLangResponsesCreatedAt(info, []byte(data), "response.created_at", response.CreatedAt))
		}
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
			writeFailed = true
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		accumulator.Observe(&streamResponse)
		switch streamResponse.Type {
		case "response.completed", "response.done", "response.incomplete":
			terminalSeen = true
			sr.Done()
		case "error", "response.error", "response.failed", "response.cancelled", "response.canceled":
			// A native failed/incomplete event is a terminal response. Bare error
			// events still need a Responses terminal synthesized below.
			terminalSeen = streamResponse.Type != "error" && streamResponse.Type != "response.error"
			upstreamError := types.OpenAIError{Type: "server_error", Code: streamResponse.Code, Message: streamResponse.Message, Param: streamResponse.Param}
			if streamResponse.Response != nil {
				if err := streamResponse.Response.GetOpenAIError(); err != nil {
					upstreamError = *err
				}
			}
			if upstreamError.Type == "" {
				upstreamError.Type = "server_error"
			}
			if upstreamError.Code == nil || upstreamError.Code == "" {
				upstreamError.Code = "server_error"
			}
			if strings.TrimSpace(upstreamError.Message) == "" {
				upstreamError.Message = "upstream response stream failed: " + streamResponse.Type
			}
			streamErr = types.WithOpenAIError(upstreamError, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
		}
	})

	if streamErr == nil && !terminalSeen {
		statusCode := http.StatusBadGateway
		var err error = fmt.Errorf("upstream response stream ended before a terminal event")
		if c.Request.Context().Err() != nil {
			err = c.Request.Context().Err()
		} else if info.StreamStatus.EndError != nil {
			err = info.StreamStatus.EndError
		} else if info.StreamStatus.EndReason == relaycommon.StreamEndReasonTimeout {
			statusCode = http.StatusGatewayTimeout
			err = fmt.Errorf("upstream response stream timed out before a terminal event")
		}
		streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, statusCode, types.ErrOptionWithSkipRetry())
	}
	if streamErr != nil {
		common.SetContextKey(c, constant.ContextKeyResponsesStreamErrorHandled, true)
		if !info.StreamStatus.HasErrors() {
			info.StreamStatus.RecordError(streamErr.Error())
		}
		// A native failed event already carries the error. Bare error events and
		// abnormal EOF need a Responses terminal failure, never a fake completion.
		if !terminalSeen && !writeFailed && c.Request.Context().Err() == nil {
			oaiError := streamErr.ToOpenAIError()
			var code any = "server_error"
			if streamErr.GetErrorType() == types.ErrorTypeOpenAIError {
				code = oaiError.Code
			}
			failureResponse.Error = map[string]any{"code": code, "message": oaiError.Message}
			failure := dto.ResponsesStreamResponse{Type: "response.failed", SequenceNumber: &sequenceNumber, Response: &failureResponse}
			data, err := common.Marshal(failure)
			if err == nil {
				helper.ExtendWriteDeadline(c)
				err = helper.ResponseChunkData(c, failure, string(data))
			}
			if err != nil {
				info.StreamStatus.RecordError(err.Error())
				logger.LogError(c, "failed to send Responses terminal error: "+err.Error())
			}
		}
		return accumulator.Finish(), streamErr
	}

	return accumulator.Finish(), nil
}

func rewriteSGLangResponsesCreatedAt(info *relaycommon.RelayInfo, payload []byte, path string, createdAt dto.IntValue) []byte {
	if info.GetChannelType() != constant.ChannelTypeSGLang {
		return payload
	}
	if !gjson.GetBytes(payload, path).Exists() {
		return payload
	}
	patched, err := sjson.SetBytes(payload, path, int(createdAt))
	if err != nil {
		return payload
	}
	return patched
}
