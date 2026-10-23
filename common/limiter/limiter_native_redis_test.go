package limiter

import (
	"context"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// SCRIPT FLUSH is permitted only on the fresh, owned disposable Redis created
// by the native runner. This is not a test against an operator cache.
func TestAllowNativeRedisAfterScriptFlush(t *testing.T) {
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
	key := "rateLimit:clean-native-flush:" + uuid.NewString()
	previousInstance := instance
	instance = nil
	once = sync.Once{}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		deleteErr := client.Del(ctx, key).Err()
		closeErr := client.Close()
		instance = previousInstance
		once = sync.Once{}
		if previousInstance != nil {
			once.Do(func() {})
		}
		require.NoError(t, deleteErr)
		require.NoError(t, closeErr)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())
	info, err := client.Info(ctx, "server").Result()
	require.NoError(t, err)
	version := ""
	for _, line := range strings.Split(info, "\n") {
		if strings.HasPrefix(line, "redis_version:") {
			version = strings.TrimSpace(strings.TrimPrefix(line, "redis_version:"))
		}
	}
	require.Equal(t, "8.8.0", version)
	t.Logf("owned native Redis version: %s", version)
	count, err := client.Exists(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, count, "the synthetic bucket must be new")
	limiter := New(ctx, client)
	allow := func(requested int64, want bool, remaining int) {
		allowed, err := limiter.Allow(ctx, key, WithCapacity(60), WithRate(0), WithRequested(requested))
		require.NoError(t, err)
		require.Equal(t, want, allowed)
		tokens, err := client.HGet(ctx, key, "tokens").Int()
		require.NoError(t, err)
		require.Equal(t, remaining, tokens, "the same bucket must not reset or debit twice")
	}
	allow(20, true, 40)
	exists, err := client.ScriptExists(ctx, limiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{true}, exists)
	require.NoError(t, client.ScriptFlush(ctx).Err())
	exists, err = client.ScriptExists(ctx, limiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{false}, exists)
	allow(30, true, 10)
	exists, err = client.ScriptExists(ctx, limiter.limitScriptSHA).Result()
	require.NoError(t, err)
	require.Equal(t, []bool{true}, exists)
	allow(11, false, 10)
	allow(10, true, 0)
}
