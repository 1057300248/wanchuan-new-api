package relay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/relay/helper"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/setting/model_setting"

	"github.com/gin-gonic/gin"
)

func ImageHelper(c *gin.Context, info *relaycommon.RelayInfo) (newAPIError *types.NewAPIError) {
	info.InitChannelMeta(c)

	imageReq, ok := info.Request.(*dto.ImageRequest)
	if !ok {
		return types.NewErrorWithStatusCode(fmt.Errorf("invalid request type, expected dto.ImageRequest, got %T", info.Request), types.ErrorCodeInvalidRequest, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
	}

	request, err := common.DeepCopy(imageReq)
	if err != nil {
		return types.NewError(fmt.Errorf("failed to copy request to ImageRequest: %w", err), types.ErrorCodeInvalidRequest, types.ErrOptionWithSkipRetry())
	}

	err = helper.ModelMappedHelper(c, info, request)
	if err != nil {
		return types.NewError(err, types.ErrorCodeChannelModelMappedError, types.ErrOptionWithSkipRetry())
	}

	adaptor := GetAdaptor(info.ApiType)
	if adaptor == nil {
		return types.NewError(fmt.Errorf("invalid api type: %d", info.ApiType), types.ErrorCodeInvalidApiType, types.ErrOptionWithSkipRetry())
	}
	adaptor.Init(info)

	var requestBody io.Reader

	if model_setting.GetGlobalSettings().PassThroughRequestEnabled || info.ChannelSetting.PassThroughBodyEnabled {
		storage, err := common.GetBodyStorage(c)
		if err != nil {
			return types.NewErrorWithStatusCode(err, types.ErrorCodeReadRequestBodyFailed, http.StatusBadRequest, types.ErrOptionWithSkipRetry())
		}
		requestBody = common.NewReplayableBodyReader(storage)
		if request.Model != imageReq.Model {
			original, err := storage.Bytes()
			if err != nil {
				return types.NewError(err, types.ErrorCodeReadRequestBodyFailed, types.ErrOptionWithSkipRetry())
			}
			mapped, err := remapImagePassThroughBody(original, c.Request.Header.Get("Content-Type"), request.Model)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			body, closer, err := relaycommon.NewOutboundJSONBody(mapped)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			defer closer.Close()
			requestBody = body
		}
	} else {
		convertedRequest, err := adaptor.ConvertImageRequest(c, info, *request)
		if err != nil {
			return types.NewError(err, types.ErrorCodeConvertRequestFailed)
		}
		relaycommon.AppendRequestConversionFromRequest(info, convertedRequest)

		switch convertedRequest.(type) {
		case *bytes.Buffer:
			requestBody = convertedRequest.(io.Reader)
		default:
			jsonData, err := common.Marshal(convertedRequest)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}

			// apply param override
			if len(info.ParamOverride) > 0 {
				jsonData, err = relaycommon.ApplyParamOverrideWithRelayInfo(jsonData, info)
				if err != nil {
					return newAPIErrorFromParamOverride(err)
				}
			}

			logger.LogDebug(c, "image request body: %s", jsonData)
			body, closer, err := relaycommon.NewOutboundJSONBody(jsonData)
			if err != nil {
				return types.NewError(err, types.ErrorCodeConvertRequestFailed, types.ErrOptionWithSkipRetry())
			}
			defer closer.Close()
			jsonData = nil
			requestBody = body
		}
	}

	statusCodeMappingStr := c.GetString("status_code_mapping")

	resp, err := adaptor.DoRequest(c, info, requestBody)
	if err != nil {
		return types.NewOpenAIError(err, types.ErrorCodeDoRequestFailed, http.StatusInternalServerError)
	}
	var httpResp *http.Response
	if resp != nil {
		httpResp = resp.(*http.Response)
		info.IsStream = info.IsStream || strings.HasPrefix(httpResp.Header.Get("Content-Type"), "text/event-stream")
		if httpResp.StatusCode != http.StatusOK {
			if httpResp.StatusCode == http.StatusCreated && info.ApiType == constant.APITypeReplicate {
				// replicate channel returns 201 Created when using Prefer: wait, treat it as success.
				httpResp.StatusCode = http.StatusOK
			} else {
				newAPIError = service.RelayErrorHandler(c.Request.Context(), httpResp, false)
				// reset status code 重置状态码
				service.ResetStatusCode(newAPIError, statusCodeMappingStr)
				return newAPIError
			}
		}
	}

	usage, newAPIError := adaptor.DoResponse(c, httpResp, info)
	if newAPIError != nil {
		// reset status code 重置状态码
		service.ResetStatusCode(newAPIError, statusCodeMappingStr)
		return newAPIError
	}

	imageN := uint(1)
	if request.N != nil {
		imageN = *request.N
	}

	if usage.(*dto.Usage).TotalTokens == 0 {
		usage.(*dto.Usage).TotalTokens = 1
	}
	if usage.(*dto.Usage).PromptTokens == 0 {
		usage.(*dto.Usage).PromptTokens = 1
	}

	quality := request.Quality
	if quality == "" {
		quality = "standard"
	}

	var logContent []string

	if len(request.Size) > 0 {
		logContent = append(logContent, fmt.Sprintf("大小 %s", request.Size))
	}
	if len(quality) > 0 {
		logContent = append(logContent, fmt.Sprintf("品质 %s", quality))
	}
	if imageN > 0 {
		logContent = append(logContent, fmt.Sprintf("生成数量 %d", imageN))
	}

	service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), logContent)
	return nil
}

// Only rewrite the top-level model. Preserve vendor fields and the original
// replay storage, which may be reused for a retry on a different channel.
func remapImagePassThroughBody(data []byte, contentType, model string) ([]byte, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	if mediaType != "multipart/form-data" {
		var payload map[string]json.RawMessage
		if err := common.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		if payload == nil {
			return nil, fmt.Errorf("expected image request object")
		}
		value, err := common.Marshal(model)
		if err != nil {
			return nil, err
		}
		payload["model"] = value
		return common.Marshal(payload)
	}
	var output bytes.Buffer
	writer := multipart.NewWriter(&output)
	// Keep the original boundary so retries never inherit modified headers.
	if err := writer.SetBoundary(params["boundary"]); err != nil {
		return nil, err
	}
	reader := multipart.NewReader(bytes.NewReader(data), params["boundary"])
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		destination, err := writer.CreatePart(part.Header)
		if err != nil {
			return nil, err
		}
		if part.FormName() == "model" && part.FileName() == "" {
			_, err = io.WriteString(destination, model)
		} else {
			_, err = io.Copy(destination, part)
		}
		if err != nil {
			return nil, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}
