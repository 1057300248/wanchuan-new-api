package controller

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHealthAndReadinessProbes(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("healthz does not require dependencies", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/healthz", nil)

		GetHealthz(ctx)

		assert.Equal(t, http.StatusOK, recorder.Code)
		assert.JSONEq(t, `{"success":true}`, recorder.Body.String())
	})

	t.Run("readyz fails closed when database is not initialized", func(t *testing.T) {
		previousDB := model.DB
		previousRedisEnabled := common.RedisEnabled
		model.DB = nil
		common.RedisEnabled = false
		t.Cleanup(func() {
			model.DB = previousDB
			common.RedisEnabled = previousRedisEnabled
		})

		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		ctx.Request = httptest.NewRequest(http.MethodGet, "/api/readyz", nil)

		GetReadyz(ctx)

		require.Equal(t, http.StatusServiceUnavailable, recorder.Code)
		assert.JSONEq(t, `{"success":false}`, recorder.Body.String())
	})
}
