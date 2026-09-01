package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	affiliateMaintenanceInterval = time.Minute
	affiliateReconcileGrace      = 2 * time.Minute
	affiliateMaintenanceBatch    = 200
)

// AffiliateRewardMaintenanceResult is persisted on every scheduled run. It is
// intentionally compact: detailed per-reference retry state remains in
// affiliate_reward_reconcile_states, while system_tasks records the run-level
// outcome operators need for scheduling and failure diagnosis.
type AffiliateRewardMaintenanceResult struct {
	ReconcileCandidates int `json:"reconcile_candidates"`
	MaturedUsers        int `json:"matured_users"`
	MaturedRewards      int `json:"matured_rewards"`
}

type affiliateRewardMaintenanceHandler struct{}

func (affiliateRewardMaintenanceHandler) Type() string {
	return model.SystemTaskTypeAffiliateRewardMaintenance
}

// Matured rewards must continue to be revalidated even when creation of new
// rewards is disabled, so this scheduled handler itself is always enabled.
func (affiliateRewardMaintenanceHandler) Enabled() bool { return true }

func (affiliateRewardMaintenanceHandler) Interval() time.Duration {
	return affiliateMaintenanceInterval
}

func (affiliateRewardMaintenanceHandler) NewPayload() any { return nil }

func (affiliateRewardMaintenanceHandler) Run(ctx context.Context, task *model.SystemTask, runnerID string) {
	result, err := runAffiliateRewardMaintenance(ctx)
	status := model.SystemTaskStatusSucceeded
	errorMessage := ""
	if err != nil {
		status = model.SystemTaskStatusFailed
		errorMessage = err.Error()
	}
	if finishErr := model.FinishSystemTask(task.TaskID, runnerID, status, result, errorMessage); finishErr != nil {
		logSystemTaskLockError(ctx, task, finishErr)
	}
}

// RegisterAffiliateRewardMaintenanceSystemTask registers the maintenance job
// before StartSystemTaskRunner. Registration is explicit (no init hook), so the
// composition root controls whether the ChuanAPI behavior is present.
func RegisterAffiliateRewardMaintenanceSystemTask() {
	RegisterSystemTaskHandler(affiliateRewardMaintenanceHandler{})
}

func runAffiliateRewardMaintenance(ctx context.Context) (AffiliateRewardMaintenanceResult, error) {
	result := AffiliateRewardMaintenanceResult{}
	errs := make([]error, 0)

	if common.AffiliateRewardEnabled {
		cutoff := common.GetTimestamp() - int64(affiliateReconcileGrace/time.Second)
		candidates, err := model.ListAffiliatePaidCostCandidates(cutoff, affiliateMaintenanceBatch)
		if err != nil {
			errs = append(errs, fmt.Errorf("affiliate reward reconciliation query failed: %w", err))
		} else {
			for _, candidate := range candidates {
				if err := ctx.Err(); err != nil {
					errs = append(errs, err)
					break
				}
				result.ReconcileCandidates++
				if err := reconcileAffiliatePaidCostWithError(candidate); err != nil {
					errs = append(errs, err)
				}
			}
		}
	}

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
		matured, err := model.MatureAffiliateRewards(userId)
		if err != nil {
			errs = append(errs, fmt.Errorf("affiliate reward maturity failed: user=%d: %w", userId, err))
			continue
		}
		result.MaturedUsers++
		result.MaturedRewards += matured
	}
	return result, errors.Join(errs...)
}

func reconcileAffiliatePaidCost(candidate model.AffiliatePaidCostCandidate) {
	if err := reconcileAffiliatePaidCostWithError(candidate); err != nil {
		common.SysError(err.Error())
	}
}

func reconcileAffiliatePaidCostWithError(candidate model.AffiliatePaidCostCandidate) error {
	canonicalType, canonicalId, paidQuota, retry, err := resolveAffiliatePaidCost(candidate)
	state := model.AffiliateRewardReconcileState{
		SourceUserId:           candidate.SourceUserId,
		ReferenceType:          candidate.ReferenceType,
		ReferenceId:            candidate.ReferenceId,
		CanonicalReferenceType: canonicalType,
		CanonicalReferenceId:   canonicalId,
		Status:                 model.AffiliateReconcileProcessed,
	}
	if err != nil || retry {
		state.Status = model.AffiliateReconcileRetry
		if err != nil {
			state.LastError = err.Error()
		} else {
			state.LastError = "canonical task is not settled yet"
		}
		if saveErr := model.SaveAffiliateRewardReconcileState(state); saveErr != nil {
			return fmt.Errorf("save affiliate reconciliation retry state failed: %w", saveErr)
		}
		return nil
	}

	if paidQuota > 0 && canonicalType != "" && canonicalId != "" {
		if _, err := RecordAffiliateRewardForRequest(candidate.SourceUserId, paidQuota, canonicalType, canonicalId); err != nil {
			state.Status = model.AffiliateReconcileRetry
			state.LastError = err.Error()
			if saveErr := model.SaveAffiliateRewardReconcileState(state); saveErr != nil {
				return fmt.Errorf("save affiliate reconciliation error state failed: %w", saveErr)
			}
			return nil
		}
	}
	if err := model.SaveAffiliateRewardReconcileState(state); err != nil {
		return fmt.Errorf("save affiliate reconciliation processed state failed: %w", err)
	}
	return nil
}

func resolveAffiliatePaidCost(candidate model.AffiliatePaidCostCandidate) (string, string, int, bool, error) {
	if candidate.ReferenceType == "task" {
		return resolveAffiliateTaskCost(candidate.SourceUserId, candidate.ReferenceId)
	}
	if candidate.ReferenceType != "relay" {
		return candidate.ReferenceType, candidate.ReferenceId, candidate.PaidQuota, false, nil
	}

	task, found, err := model.FindTaskByRequestId(candidate.SourceUserId, candidate.ReferenceId)
	if err != nil {
		return "", "", 0, true, err
	}
	if found {
		if task.Status == model.TaskStatusFailure {
			return "task", task.TaskID, 0, false, nil
		}
		if task.Status != model.TaskStatusSuccess {
			return "task", task.TaskID, 0, true, nil
		}
		return resolveAffiliateTaskCost(candidate.SourceUserId, task.TaskID)
	}
	cost, err := model.NetCostByReferences(candidate.SourceUserId, [][2]string{{"relay", candidate.ReferenceId}})
	if err != nil {
		return "relay", candidate.ReferenceId, 0, true, err
	}
	return "relay", candidate.ReferenceId, -cost[model.AccountPaid], false, nil
}

func resolveAffiliateTaskCost(userId int, taskId string) (string, string, int, bool, error) {
	task, found, err := model.GetByTaskId(userId, taskId)
	if err != nil {
		return "task", taskId, 0, true, err
	}
	if !found || task == nil || task.Status != model.TaskStatusSuccess {
		if found && task != nil && task.Status == model.TaskStatusFailure {
			return "task", taskId, 0, false, nil
		}
		return "task", taskId, 0, true, nil
	}
	refs := TaskWalletCostRefs(task)
	cost, err := model.NetCostByReferences(userId, refs)
	if err != nil {
		return "task", taskId, 0, true, err
	}
	return "task", taskId, -cost[model.AccountPaid], false, nil
}
