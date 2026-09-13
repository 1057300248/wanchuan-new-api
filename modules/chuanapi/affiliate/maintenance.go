package affiliate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
)

const (
	affiliateMaintenanceInterval = time.Minute
	affiliateMaintenanceBatch    = 200
)

// AffiliateRewardMaintenanceResult is persisted on every scheduled run. New
// rewards are created inside the top-up settlement transaction, so the
// scheduled task only releases frozen rewards; the result therefore records
// maturity counts only.
type AffiliateRewardMaintenanceResult struct {
	MaturedUsers   int `json:"matured_users"`
	MaturedRewards int `json:"matured_rewards"`
}

type MaintenanceHandler struct{}

func (MaintenanceHandler) Type() string {
	return model.SystemTaskTypeAffiliateRewardMaintenance
}

// Matured rewards must continue to be released even when creation of new
// rewards is disabled, so this scheduled handler itself is always enabled.
func (MaintenanceHandler) Enabled() bool { return true }

func (MaintenanceHandler) Interval() time.Duration {
	return affiliateMaintenanceInterval
}

func (MaintenanceHandler) NewPayload() any { return nil }

func (MaintenanceHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := runAffiliateRewardMaintenance(ctx)
	status := model.SystemTaskStatusSucceeded
	errorMessage := ""
	if err != nil {
		status = model.SystemTaskStatusFailed
		errorMessage = err.Error()
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage); finishErr != nil {
		logMaintenanceTaskError(ctx, task, finishErr)
	}
}

func logMaintenanceTaskError(ctx context.Context, task *model.SystemTask, err error) {
	if errors.Is(err, model.ErrSystemTaskLockLost) {
		logger.LogWarn(ctx, fmt.Sprintf("system task %s lock lost", task.TaskID))
		return
	}
	logger.LogWarn(ctx, fmt.Sprintf("system task %s update failed: %v", task.TaskID, err))
}

func runAffiliateRewardMaintenance(ctx context.Context) (AffiliateRewardMaintenanceResult, error) {
	result := AffiliateRewardMaintenanceResult{}
	errs := make([]error, 0)

	// Existing frozen rewards keep their snapshot semantics even if creation of
	// new rewards is switched off later. They are still revalidated here and
	// either mature or are cancelled.
	userIds, err := model.ListDueAffiliateRewardUserIds(common.GetTimestamp(), affiliateMaintenanceBatch)
	if err != nil {
		errs = append(errs, fmt.Errorf("affiliate reward maturity query failed: %w", err))
		return result, errors.Join(errs...)
	}
	for _, userId := range userIds {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		matured, err := (RewardService{}).Mature(userId)
		if err != nil {
			errs = append(errs, fmt.Errorf("affiliate reward maturity failed: user=%d: %w", userId, err))
			continue
		}
		result.MaturedUsers++
		result.MaturedRewards += matured
	}
	return result, errors.Join(errs...)
}
