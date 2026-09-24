package common

import (
	"context"
	"time"

	"github.com/QuantumNous/new-api/pkg/lifecycle"
)

func RunSystemMonitor(schedule, work context.Context) {
	lifecycle.Periodic(schedule, work, func() time.Duration {
		if !GetPerformanceMonitorConfig().Enabled {
			return 30 * time.Second
		}
		return 5 * time.Second
	}, true, func(context.Context) {
		if GetPerformanceMonitorConfig().Enabled {
			updateSystemStatus()
		}
	})
}
