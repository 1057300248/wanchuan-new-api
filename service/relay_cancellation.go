package service

import (
	"context"
	"net/http"

	"github.com/QuantumNous/new-api/relaykit/types"
	"github.com/gin-gonic/gin"
)

// ClientGoneErrorCode identifies downstream delivery failures, never an upstream
// account failure. 499 is diagnostic only: a disconnected client is not written to.
const ClientGoneErrorCode types.ErrorCode = "client_gone"
const StatusClientClosedRequest = 499

// RelayRequestCancellation checks the parent request, not a per-attempt timeout.
// A timed-out upstream may still be retried while this context remains live.
func RelayRequestCancellation(c *gin.Context) *types.NewAPIError {
	if c == nil || c.Request == nil || c.Request.Context().Err() == nil {
		return nil
	}
	err := c.Request.Context().Err()
	code, status := types.ErrorCode("request_deadline_exceeded"), http.StatusRequestTimeout
	if err == context.Canceled {
		code, status = ClientGoneErrorCode, StatusClientClosedRequest
	}
	return types.NewErrorWithStatusCode(err, code, status, types.ErrOptionWithSkipRetry())
}
