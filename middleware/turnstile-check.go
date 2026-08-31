package middleware

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
)

const (
	turnstileExpectedHostname = "chuanapi.com"
	turnstileMaxTokenLength   = 2048
	turnstileMaxResponseBytes = 64 * 1024
)

var turnstileHTTPClient = &http.Client{Timeout: 10 * time.Second}

type turnstileCheckResponse struct {
	Success    bool     `json:"success"`
	Hostname   string   `json:"hostname"`
	Action     string   `json:"action"`
	ErrorCodes []string `json:"error-codes"`
}

func TurnstileCheck(expectedAction string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !common.TurnstileCheckEnabled {
			c.Next()
			return
		}

		response := strings.TrimSpace(c.Query("turnstile"))
		if response == "" || len(response) > turnstileMaxTokenLength {
			turnstileReject(c)
			return
		}
		if strings.TrimSpace(common.TurnstileSecretKey) == "" || expectedAction == "" {
			common.SysLog("Turnstile verification rejected: incomplete server configuration")
			turnstileReject(c)
			return
		}

		form := url.Values{
			"secret":   {common.TurnstileSecretKey},
			"response": {response},
			"remoteip": {c.ClientIP()},
		}
		request, err := http.NewRequestWithContext(
			c.Request.Context(),
			http.MethodPost,
			"https://challenges.cloudflare.com/turnstile/v0/siteverify",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			common.SysLog("Turnstile verification request could not be created")
			turnstileReject(c)
			return
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")

		rawRes, err := turnstileHTTPClient.Do(request)
		if err != nil {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify request failed: %v", err))
			turnstileReject(c)
			return
		}
		defer rawRes.Body.Close()

		if rawRes.StatusCode < http.StatusOK || rawRes.StatusCode >= http.StatusMultipleChoices {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify returned HTTP %d", rawRes.StatusCode))
			turnstileReject(c)
			return
		}

		var result turnstileCheckResponse
		if err = common.DecodeJson(io.LimitReader(rawRes.Body, turnstileMaxResponseBytes), &result); err != nil {
			common.SysLog(fmt.Sprintf("Turnstile Siteverify returned an invalid response: %v", err))
			turnstileReject(c)
			return
		}
		if !result.Success || result.Action != expectedAction || result.Hostname != turnstileExpectedHostname {
			common.SysLog(fmt.Sprintf(
				"Turnstile verification rejected: success=%t action=%q hostname=%q errors=%v",
				result.Success,
				result.Action,
				result.Hostname,
				result.ErrorCodes,
			))
			turnstileReject(c)
			return
		}

		c.Next()
	}
}

func turnstileReject(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
		"success": false,
		"code":    "TURNSTILE_VERIFICATION_FAILED",
		"message": "Turnstile 校验失败，请刷新后重试！",
	})
}
