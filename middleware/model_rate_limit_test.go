package middleware

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestModelRedisRateLimitUsesUTCRegardlessOfLocalTimezone(t *testing.T) {
	redisServer, redisClient := useRateLimitMiniRedis(t)
	previousLocation := time.Local
	time.Local = time.FixedZone("test-utc-plus-eight", 8*60*60)
	t.Cleanup(func() { time.Local = previousLocation })

	ctx := context.Background()
	recordKey := "rateLimit:model-utc-record"
	reservation, allowed, err := reserveRedisModelSuccess(ctx, redisClient, recordKey, 2, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	reservation.Complete(true)
	recorded, err := redisClient.LIndex(ctx, recordKey, 0).Result()
	require.NoError(t, err)
	recordedAt, err := time.Parse(modelRateLimitTimeFormat, recorded)
	require.NoError(t, err)
	assert.WithinDuration(t, time.Now().UTC(), recordedAt, 2*time.Second)

	checkKey := "rateLimit:model-utc-check"
	withinWindow := time.Now().UTC().Add(-30 * time.Second).Format(modelRateLimitTimeFormat)
	_, err = redisServer.Push(checkKey, withinWindow, withinWindow)
	require.NoError(t, err)
	_, allowed, err = reserveRedisModelSuccess(ctx, redisClient, checkKey, 2, 60, nil)
	require.NoError(t, err)
	assert.False(t, allowed, "an existing UTC timestamp inside the window must remain limited on a non-UTC host")
}

var modelRateLimitTestUsers atomic.Int64

func TestModelRateLimitStreamFailuresDoNotConsumeSuccessLimit(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		for _, totalLimit := range []int{0, 2} {
			t.Run(fmt.Sprintf("%s/total=%d", backend, totalLimit), func(t *testing.T) {
				userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
				handler := memoryRateLimitHandler(60, totalLimit, 1)
				if backend == "redis" {
					useRateLimitMiniRedis(t)
					handler = redisRateLimitHandler(60, totalLimit, 1)
				}
				router := gin.New()
				router.GET("/:outcome", func(c *gin.Context) { c.Set("id", userID) }, handler, func(c *gin.Context) {
					status := relaycommon.NewStreamStatus()
					if c.Param("outcome") == "failed" {
						status.MarkFailed("server_error", "", 0)
					} else {
						status.MarkCompleted()
					}
					common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
					c.Status(http.StatusOK)
				})
				assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
				if totalLimit > 0 {
					assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
				} else {
					assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
				}
				assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			})
		}
	}
}

func TestModelRateLimitReservesConcurrentSuccessAdmission(t *testing.T) {
	for _, backend := range []string{"memory", "redis"} {
		t.Run(backend, func(t *testing.T) {
			handler := memoryRateLimitHandler(60, 0, 1)
			if backend == "redis" {
				useRateLimitMiniRedis(t)
				handler = redisRateLimitHandler(60, 0, 1)
			}
			userID := 7200000 + int(modelRateLimitTestUsers.Add(1))
			entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseFirst := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseFirst)
			router := gin.New()
			router.GET("/:outcome", func(c *gin.Context) { c.Set("id", userID) }, handler, func(c *gin.Context) {
				if c.Param("outcome") == "failed" {
					close(entered)
					<-release
					status := relaycommon.NewStreamStatus()
					status.MarkFailed("server_error", "", 0)
					common.SetContextKey(c, constant.ContextKeyResponseStreamStatus, status)
				}
				c.Status(http.StatusOK)
			})
			go func() {
				defer close(finished)
				assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/failed", "127.0.0.1:1000").Code)
			}()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("first request did not enter the handler")
			}
			assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			releaseFirst()
			select {
			case <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("first request did not finish")
			}
			assert.Equal(t, http.StatusOK, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
			assert.Equal(t, http.StatusTooManyRequests, performRateLimitRequest(router, "/completed", "127.0.0.1:1000").Code)
		})
	}
}

func TestModelRedisReservationCleanupOutlivesCancelledRequest(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	ctx, cancel := context.WithCancel(context.Background())
	key := "rateLimit:cleanup:" + uuid.NewString()
	reservation, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, cancel)
	require.NoError(t, err)
	require.True(t, allowed)
	cancel()
	reservation.Complete(false)
	reservation.Complete(true) // Cannot turn an already released failure into success.
	assert.EqualValues(t, 0, client.LLen(context.Background(), key).Val())
	second, allowed, err := reserveRedisModelSuccess(context.Background(), client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	second.Complete(true)
	second.Complete(true)
	assert.EqualValues(t, 1, client.LLen(context.Background(), key).Val())
}

func TestModelRedisReservationReplayAndLeaseExpiry(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	checkModelRedisReservationContract(t, client)
}

func checkModelRedisReservationContract(t *testing.T, client *redis.Client) {
	t.Helper()
	ctx := context.Background()
	key := "rateLimit:runtime-test:" + uuid.NewString()
	t.Cleanup(func() {
		keys, err := client.Keys(ctx, key+"*").Result()
		if err == nil && len(keys) > 0 {
			require.NoError(t, client.Del(ctx, keys...).Err())
		}
	})
	first, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	defer first.Complete(false)
	_, allowed, err = reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.False(t, allowed)
	// Redis client retry of the same reservation is idempotent, even at capacity.
	cutoff := time.Now().UTC().Add(-time.Minute).Format(modelRateLimitTimeFormat)
	replay, err := modelSuccessReserveScript.Run(ctx, client, []string{key, key + ":pending-v2"}, cutoff, 1, first.id, modelLimitReservationLease.Milliseconds()).Int64()
	require.NoError(t, err)
	assert.EqualValues(t, 1, replay)
	renewed, err := modelSuccessRenewScript.Run(ctx, client, []string{key + ":pending-v2"}, first.id, modelLimitReservationLease.Milliseconds()).Int64()
	require.NoError(t, err)
	assert.EqualValues(t, 1, renewed)
	// Expire the admission without sleeping. A crashed process must not retain
	// a permanent success slot; a late completion still records its outcome.
	require.NoError(t, client.ZAdd(ctx, key+":pending-v2", &redis.Z{Score: 0, Member: first.id}).Err())
	second, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	require.True(t, allowed)
	second.Complete(false)
	first.Complete(true)
	replay, err = modelSuccessCompleteScript.Run(ctx, client, []string{key, key + ":pending-v2", key + ":done:" + first.id}, first.id, 1, time.Now().UTC().Format(modelRateLimitTimeFormat), 1, 60, 1200000).Int64()
	require.NoError(t, err)
	assert.Zero(t, replay)
	assert.EqualValues(t, 1, client.LLen(ctx, key).Val())
	_, allowed, err = reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.NoError(t, err)
	assert.False(t, allowed)

	totalKey := key + ":total"
	allowed, err = allowRedisModelAttempt(ctx, client, totalKey, 1, 60)
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = allowRedisModelAttempt(ctx, client, totalKey, 1, 60)
	require.NoError(t, err)
	assert.False(t, allowed)
}

func TestModelRedisRateLimitFailsClosedWithoutClobberingInvalidKeys(t *testing.T) {
	_, client := useRateLimitMiniRedis(t)
	ctx := context.Background()
	key := "rateLimit:invalid:" + uuid.NewString()
	require.NoError(t, client.Set(ctx, key, "not-a-list", time.Minute).Err())
	_, allowed, err := reserveRedisModelSuccess(ctx, client, key, 1, 60, nil)
	require.Error(t, err)
	assert.False(t, allowed)
	assert.Equal(t, "not-a-list", client.Get(ctx, key).Val())
	_, allowed, err = reserveRedisModelSuccess(ctx, nil, key, 1, 60, nil)
	require.Error(t, err)
	assert.False(t, allowed)
}

func TestModelRateLimitRealRedis(t *testing.T) {
	if os.Getenv("TEST_RUNTIME_REDIS") != "1" {
		t.Skip("set TEST_RUNTIME_REDIS=1 for isolated real Redis verification")
	}
	dsn := os.Getenv("TEST_REDIS_DSN")
	require.NotEmpty(t, dsn)
	options, err := redis.ParseURL(dsn)
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	checkModelRedisReservationContract(t, client)
}
