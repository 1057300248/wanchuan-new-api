package service

// Restore only the mature shared callback-join fixture here. Financial scenario
// tests will be added with the independently reviewed financial integration.
import (
	"testing"
	"time"

	"github.com/bytedance/gopkg/util/gopool"
	"github.com/stretchr/testify/require"
)

func waitRefundCallback(t *testing.T, _ *BillingSession) {
	t.Helper()
	// These consumers run in an owned child containing no notification daemon.
	// gopool decrements WorkerCount only after callbacks return, including nested
	// token-cache work; refunded and final SQL are not callback-completion signals.
	require.Eventually(t, func() bool { return gopool.WorkerCount() == 0 }, 5*time.Second, time.Millisecond)
}
