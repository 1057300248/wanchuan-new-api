package middleware

import (
	"context"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/i18n"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Run this root alone in its own test process: the official total limiter
// intentionally captures its first client in a process-wide singleton. These
// contracts exercise the existing rc46 handler, not donor Redis lease scripts.
func TestModelRateLimitNativeRedisContracts(t *testing.T) {
	if os.Getenv("TEST_RUNTIME_REDIS") != "1" || os.Getenv("TEST_REDIS_DISPOSABLE") != "1" {
		t.Skip("fresh owned TEST_RUNTIME_REDIS=1 and TEST_REDIS_DISPOSABLE=1 required")
	}
	address := os.Getenv("TEST_REDIS_ADDR")
	host, portText, err := net.SplitHostPort(address)
	require.NoError(t, err)
	ip := net.ParseIP(host)
	require.True(t, ip != nil && ip.IsLoopback(), "native Redis must use a literal loopback IP")
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	require.True(t, port > 0 && port <= 65535)
	client := redis.NewClient(&redis.Options{Addr: address, DialTimeout: 2 * time.Second, ReadTimeout: 2 * time.Second, WriteTimeout: 2 * time.Second, MaxRetries: -1})
	oldClient, oldEnabled := common.RDB, common.RedisEnabled
	oldMode, oldMinutes := gin.Mode(), setting.ModelRequestRateLimitDurationMinutes
	common.RDB, common.RedisEnabled = client, true
	setting.ModelRequestRateLimitDurationMinutes = 1
	gin.SetMode(gin.TestMode)
	t.Cleanup(func() {
		common.RDB, common.RedisEnabled = oldClient, oldEnabled
		setting.ModelRequestRateLimitDurationMinutes = oldMinutes
		gin.SetMode(oldMode)
		require.NoError(t, client.Close())
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	require.NoError(t, client.Ping(ctx).Err())
	info, err := client.Info(ctx, "server").Result()
	cancel()
	require.NoError(t, err)
	version := ""
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
		}
	}
	require.Equal(t, "8.8.0", version)
	t.Logf("owned native Redis version: %s", version)
	require.NoError(t, i18n.Init())
	cleanupKeys := func(t *testing.T, keys ...string) {
		t.Helper()
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			require.NoError(t, client.Del(ctx, keys...).Err())
		})
	}
	t.Run("failed_stream_success_limit_and_utc", func(t *testing.T) {
		oldLocation := time.Local
		time.Local = time.FixedZone("native-test-plus-eight", 8*60*60)
		t.Cleanup(func() { time.Local = oldLocation })
		subject := "clean-native-success:" + uuid.NewString()
		key := "rateLimit:" + ModelRequestRateLimitSuccessCountMark + ":" + subject
		cleanupKeys(t, key)
		calls := 0
		router := gin.New()
		router.GET("/:outcome", redisRateLimitHandler(subject, 60, 0, 1), func(c *gin.Context) {
			calls++
			status := relaycommon.NewStreamStatus()
			if c.Param("outcome") == "failed" {
				status.MarkFailed("server_error", "", 0)
			} else {
				status.MarkCompleted()
			}
			common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
			c.Status(http.StatusOK)
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
		count, err := client.LLen(ctx, key).Result()
		require.NoError(t, err)
		require.Zero(t, count, "a failed stream with HTTP 200 must not consume the success limit")
		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
		require.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
		require.Equal(t, 2, calls, "the limited request must not reach the handler")
		count, err = client.LLen(ctx, key).Result()
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
		stamp, err := client.LIndex(ctx, key, 0).Result()
		require.NoError(t, err)
		stored, err := time.Parse(modelRateLimitTimeFormat, stamp)
		require.NoError(t, err)
		require.WithinDuration(t, time.Now().UTC(), stored, 5*time.Second)
		ttl, err := client.PTTL(ctx, key).Result()
		require.NoError(t, err)
		require.Positive(t, ttl)
		require.LessOrEqual(t, ttl, time.Minute)
	})
	t.Run("total_bucket_after_script_flush", func(t *testing.T) {
		subject := "clean-native-total:" + uuid.NewString()
		cleanupKeys(t, "rateLimit:"+subject)
		calls := 0
		router := gin.New()
		router.GET("/", redisRateLimitHandler(subject, 60, 2, 0), func(c *gin.Context) {
			calls++
			c.Status(http.StatusOK)
		})
		started := time.Now()
		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		flushErr := client.ScriptFlush(ctx).Err()
		cancel()
		require.NoError(t, flushErr)
		require.Equal(t, http.StatusOK, performRateLimitRequest(router, "/", "127.0.0.1:1000").Code)
		third := performRateLimitRequest(router, "/", "127.0.0.1:1000")
		require.Less(t, time.Since(started), 20*time.Second, "fixture must finish before legitimate rate-2 token refill can admit a third request")
		require.Equal(t, http.StatusTooManyRequests, third.Code)
		require.Equal(t, 2, calls, "SCRIPT FLUSH must recover without resetting the total bucket")
	})
}
