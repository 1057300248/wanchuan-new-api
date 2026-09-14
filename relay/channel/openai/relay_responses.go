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
	"github.com/QuantumNous/new-api/relaykit/relayconvert"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"

	"github.com/gin-gonic/gin"
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

	// 写入新的 response body
	service.IOCopyBytesGracefully(c, resp, responseBody)

	// compute usage
	usage := relayconvert.NormalizeResponsesUsage(responsesResponse.Usage)
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

	var usage = &dto.Usage{}
	var responseTextBuilder strings.Builder
	imageCounter := &relaycommon.ImageGenerationCallCounter{}
	imageCommitted := false
	var streamErr *types.NewAPIError
	terminalSeen := false
	writeFailed := false
	sequenceNumber := 0
	failureResponse := dto.OpenAIResponsesResponse{
		ID: "resp_" + common.GetUUID(), Object: "response",
		CreatedAt: int(time.Now().Unix()), Model: info.UpstreamModelName,
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
		}
		if err := helper.ResponseChunkData(c, streamResponse, data); err != nil {
			writeFailed = true
			streamErr = types.NewErrorWithStatusCode(err, types.ErrorCodeBadResponse, http.StatusBadGateway, types.ErrOptionWithSkipRetry())
			sr.Stop(streamErr)
			return
		}
		switch streamResponse.Type {
		case "response.completed", "response.done":
			terminalSeen = true
			if streamResponse.Response != nil {
				if streamResponse.Response.Usage != nil {
					incomingUsage := relayconvert.NormalizeResponsesUsage(streamResponse.Response.Usage)
					usage = dto.MergeUsageNonZero(usage, incomingUsage)
				}
				if !imageCommitted {
					if relaycommon.IsNonBillableResponsesStatus(streamResponse.Response.Status) {
						imageCounter.Reset()
						imageCounter.Commit(info)
						imageCommitted = true
					} else {
						for i := range streamResponse.Response.Output {
							idx := i
							imageCounter.Observe(&streamResponse.Response.Output[i], &idx)
						}
						imageCounter.Commit(info)
						imageCommitted = true
					}
				}
			} else if !imageCommitted {
				imageCounter.Commit(info)
				imageCommitted = true
			}
			sr.Done()
		case "response.incomplete":
			// Token limits are a valid incomplete terminal response, not a broken
			// transport. Preserve its usage and do not fabricate a failed/completed event.
			terminalSeen = true
			if streamResponse.Response != nil && streamResponse.Response.Usage != nil {
				usage = dto.MergeUsageNonZero(usage, relayconvert.NormalizeResponsesUsage(streamResponse.Response.Usage))
			}
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
			sr.Done()
		case "error", "response.error", "response.failed", "response.cancelled", "response.canceled":
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
			if !imageCommitted {
				imageCounter.Reset()
				imageCounter.Commit(info)
				imageCommitted = true
			}
			sr.Stop(streamErr)
		case "response.output_text.delta":
			// 处理输出文本
			responseTextBuilder.WriteString(streamResponse.Delta)
		case dto.ResponsesOutputTypeItemDone:
			if streamResponse.Item != nil {
				switch streamResponse.Item.Type {
				case dto.BuildInCallWebSearchCall:
					info.CountBillableToolCall(dto.BuildInCallWebSearchCall, "")
				case dto.BuildInCallFileSearchCall:
					info.CountBillableToolCall(dto.BuildInCallFileSearchCall, "")
				case dto.BuildInCallFunctionCall:
					info.CountBillableToolCall(dto.BuildInCallFunctionCall, streamResponse.Item.Name)
				case dto.ResponsesOutputTypeImageGenerationCall:
					if !imageCommitted {
						imageCounter.Observe(streamResponse.Item, streamResponse.OutputIndex)
					}
				}
			}
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
		return usage, streamErr
	}

	if usage.CompletionTokens == 0 {
		// 计算输出文本的 token 数量
		tempStr := responseTextBuilder.String()
		if len(tempStr) > 0 {
			// 非正常结束，使用输出文本的 token 数量
			completionTokens := service.CountTextToken(tempStr, info.UpstreamModelName)
			usage.CompletionTokens = completionTokens
		}
	}

	if usage.PromptTokens == 0 && usage.CompletionTokens != 0 {
		usage.PromptTokens = info.GetEstimatePromptTokens()
	}

	usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	if usage.BillingUsage != nil {
		usage.BillingUsage = dto.CloneBillingUsageWithEstimatedCompletion(usage.BillingUsage, usage.CompletionTokens)
	}

	return usage, nil
}
