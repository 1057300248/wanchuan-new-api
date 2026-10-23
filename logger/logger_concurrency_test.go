package logger

import (
	"bytes"
	"context"
	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"strings"
	"sync"
	"testing"
)

type concurrentLoggerCapture struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *concurrentLoggerCapture) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

// All writers join before capture/restore. Stay below rotation so the fixture
// owns no asynchronous SetupLogger worker; -race exercises the shared counter.
func TestConcurrentLoggerRetainsEveryLineAndCorrelation(t *testing.T) {
	var captured concurrentLoggerCapture
	common.LogWriterMu.Lock()
	oldWriter, oldError := gin.DefaultWriter, gin.DefaultErrorWriter
	gin.DefaultWriter, gin.DefaultErrorWriter = &captured, &captured
	common.LogWriterMu.Unlock()
	t.Cleanup(func() {
		common.LogWriterMu.Lock()
		gin.DefaultWriter, gin.DefaultErrorWriter = oldWriter, oldError
		common.LogWriterMu.Unlock()
	})
	ctx := context.WithValue(context.Background(), common.RequestIdKey, "synthetic-local-correlation")
	const workers, lines = 16, 128
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < workers; worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for i := 0; i < lines; i++ {
				switch i % 3 {
				case 0:
					LogInfo(ctx, "safe-message")
				case 1:
					LogWarn(ctx, "safe-message")
				case 2:
					LogError(ctx, "safe-message")
				}
			}
		}()
	}
	close(start)
	wg.Wait()
	body := captured.buffer.String()
	assert.Equal(t, workers*lines, strings.Count(body, "\n"))
	assert.Equal(t, workers*lines, strings.Count(body, "| synthetic-local-correlation | safe-message"))
}
