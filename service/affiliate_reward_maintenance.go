package service

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
)

const (
	affiliateMaintenanceInterval = time.Minute
	affiliateReconcileGrace      = 2 * time.Minute
	affiliateMaintenanceBatch    = 200
)

var (
	affiliateMaintenanceOnce    sync.Once
	affiliateMaintenanceRunning atomic.Bool
)

// StartAffiliateRewardMaintenance continuously reconciles durable paid-ledger
// costs into idempotent reward records and matures due rewards. It runs on the
// master node so reward delivery does not depend on a caller opening Wallet.
func StartAffiliateRewardMaintenance() {
	affiliateMaintenanceOnce.Do(func() {
		if !common.IsMasterNode {
			return
		}
		go func() {
			runAffiliateRewardMaintenanceOnce()
			ticker := time.NewTicker(affiliateMaintenanceInterval)
			defer ticker.Stop()
			for range ticker.C {
				runAffiliateRewardMaintenanceOnce()
			}
		}()
	})
}

func runAffiliateRewardMaintenanceOnce() {
	if !affiliateMaintenanceRunning.CompareAndSwap(false, true) {
		return
	}
	defer affiliateMaintenanceRunning.Store(false)

	if common.AffiliateRewardEnabled {
		cutoff := common.GetTimestamp() - int64(affiliateReconcileGrace/time.Second)
		candidates, err := model.ListAffiliatePaidCostCandidates(cutoff, affiliateMaintenanceBatch)
		if err != nil {
			common.SysError("affiliate reward reconciliation query failed: " + err.Error())
		} else {
			for _, candidate := range candidates {
				reconcileAffiliatePaidCost(candidate)
			}
		}
	}

	// Existing frozen rewards keep their snapshot semantics even if creation of
	// new rewards is switched off later. They are still revalidated here and
	// either mature or are cancelled.
	userIds, err := model.ListDueAffiliateRewardUserIds(common.GetTimestamp(), affiliateMaintenanceBatch)
	if err != nil {
		common.SysError("affiliate reward maturity query failed: " + err.Error())
		return
	}
	for _, userId := range userIds {
		if _, err := model.MatureAffiliateRewards(userId); err != nil {
			common.SysError(fmt.Sprintf("affiliate reward maturity failed: user=%d err=%s", userId, err.Error()))
		}
	}
}

func reconcileAffiliatePaidCost(candidate model.AffiliatePaidCostCandidate) {
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
			common.SysError("save affiliate reconciliation retry state failed: " + saveErr.Error())
		}
		return
	}

	if paidQuota > 0 && canonicalType != "" && canonicalId != "" {
		if _, err := RecordAffiliateRewardForRequest(candidate.SourceUserId, paidQuota, canonicalType, canonicalId); err != nil {
			state.Status = model.AffiliateReconcileRetry
			state.LastError = err.Error()
			if saveErr := model.SaveAffiliateRewardReconcileState(state); saveErr != nil {
				common.SysError("save affiliate reconciliation error state failed: " + saveErr.Error())
			}
			return
		}
	}
	if err := model.SaveAffiliateRewardReconcileState(state); err != nil {
		common.SysError("save affiliate reconciliation processed state failed: " + err.Error())
	}
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
