package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"
	relaycommon "github.com/QuantumNous/new-api/relay/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// SupportsDurableTaskBilling reports the host session capability. Alternate
// BillingSettler implementations retain their own persistence contract.
func SupportsDurableTaskBilling(info *relaycommon.RelayInfo) bool {
	_, ok := info.Billing.(*BillingSession)
	return ok
}

// PrepareSubmittedTaskBilling transfers a real session's reservation to the
// task's first durable record. Quota records money actually held, not the final
// quote: otherwise a crash before a downward settlement loses the refund delta.
// Non-session implementations keep the existing BillingSettler contract.
// There remains an unavoidable window after upstream acceptance and before this
// first local insert: database unavailability/process death there needs operator
// reconciliation. This is not end-to-end exactly-once upstream submission.
func PrepareSubmittedTaskBilling(info *relaycommon.RelayInfo, task *model.Task, actualQuota int) {
	session, ok := info.Billing.(*BillingSession)
	if !ok {
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if task.PrivateData.Execution == nil {
		task.PrivateData.Execution = &model.TaskExecutionSnapshot{}
	}
	if task.PrivateData.Execution.RequestID == "" {
		task.PrivateData.Execution.RequestID = info.RequestId
	}
	task.Quota = session.preConsumedQuota
	task.SettlementPending = true
	task.PrivateData.PendingSettlement = &model.TaskSettlement{Phase: "submit", Quota: actualQuota, Clamp: info.QuotaClamp}
	if info.IsPlayground {
		task.PrivateData.TokenId = 0
	}
}

// SettleSubmittedTaskBilling returns whether the caller should write its rich
// request-scoped consumption log. Recovery writes an equivalent task log when
// it wins instead. Logs may use a separate database and are best-effort; the
// exactly-once boundary is the primary-DB funds, token, usage and task receipt.
func SettleSubmittedTaskBilling(c *gin.Context, info *relaycommon.RelayInfo, task *model.Task, actualQuota int) (bool, error) {
	if _, ok := info.Billing.(*BillingSession); !ok {
		return true, SettleBilling(c, info, actualQuota)
	}
	applied, err := commitPendingTaskBilling(c, task, false)
	if err != nil {
		return false, err
	}
	session := info.Billing.(*BillingSession)
	session.mu.Lock()
	session.settled = true
	session.fundingSettled = true
	preConsumed := session.preConsumedQuota
	session.mu.Unlock()
	if applied && actualQuota != 0 {
		if info.BillingSource == BillingSourceSubscription {
			info.SubscriptionPostDelta += int64(actualQuota - preConsumed)
			checkAndSendSubscriptionQuotaNotify(info)
		} else {
			checkAndSendQuotaNotify(info, actualQuota-preConsumed, preConsumed)
		}
	}
	return applied, nil
}

// RecoverTaskSettlement needs only the saved task intent, never a provider call
// or a plugin/pricing re-evaluation. Callers may safely race or reload on restart.
func RecoverTaskSettlement(ctx context.Context, task *model.Task) error {
	_, err := commitPendingTaskBilling(ctx, task, true)
	if err != nil {
		_ = model.NoteTaskSettlementAttempt(ctx, task.ID)
	}
	return err
}

func commitPendingTaskBilling(ctx context.Context, task *model.Task, logSubmission bool) (bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Resolve model policy before holding a connection: policy lookup can query
	// model.DB, including a single-connection SQLite deployment.
	var snapshot model.Task
	if err := model.DB.WithContext(ctx).First(&snapshot, task.ID).Error; err != nil {
		return false, err
	}
	var order []model.LedgerAccount
	if snapshot.SettlementPending && !taskIsSubscription(&snapshot) {
		order = taskDebitOrder(&snapshot)
	}
	var intent model.TaskSettlement
	var beforeQuota int
	var walletResult *model.LedgerResult
	stored, applied, err := model.CommitPendingTaskSettlement(ctx, task.ID, func(tx *gorm.DB, current *model.Task) error {
		intent = *current.PrivateData.PendingSettlement
		if intent.Quota < 0 || intent.Quota > common.MaxQuota || current.Quota < 0 || current.Quota > common.MaxQuota {
			return errors.New("task settlement quota is outside the supported range")
		}
		if intent.Phase != "submit" && intent.Phase != "terminal" && intent.Phase != "legacy_timeout" {
			return errors.New("unknown task settlement phase")
		}
		if intent.SkipFunds && (intent.Phase != "legacy_timeout" || current.SubmitTime <= 0 || current.SubmitTime >= model.TaskRefundLegacyCutoff) {
			return errors.New("invalid legacy task settlement")
		}
		beforeQuota = current.Quota
		delta := intent.Quota - beforeQuota
		if !intent.SkipFunds {
			key := fmt.Sprintf("task:%s:%d", intent.Phase, current.ID)
			shares, result, err := taskAdjustFundingTx(tx, current, delta, key, order)
			if err != nil {
				return err
			}
			if result != nil && result.Replayed {
				// The intent clear and these financial writes are one transaction.
				// An existing operation with a still-pending intent is corruption,
				// not permission to skip/repeat the token and aggregate counters.
				return errors.New("task settlement operation exists with pending intent")
			}
			walletResult = result
			if current.PrivateData.TokenId > 0 && delta != 0 {
				var token model.Token
				err = tx.First(&token, current.PrivateData.TokenId).Error
				if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
				if err == nil {
					if err = model.ApplyTokenQuotaDeltaTx(tx, token.Id, token.Key, -delta); err != nil {
						return err
					}
				}
			}
			current.PrivateData.WalletDebit = shares
			usageDelta := delta
			updates := map[string]any{}
			if intent.Phase == "submit" {
				if current.PrivateData.SubmissionAccounted {
					return errors.New("task submission usage was already accounted")
				}
				usageDelta = intent.Quota
				updates["request_count"] = gorm.Expr("request_count + ?", 1)
				current.PrivateData.SubmissionAccounted = true
			}
			if usageDelta != 0 || intent.Phase == "submit" {
				updates["used_quota"] = gorm.Expr("used_quota + ?", usageDelta)
				result := tx.Model(&model.User{}).Where("id = ?", current.UserId).Updates(updates)
				if result.Error != nil {
					return result.Error
				}
				if result.RowsAffected != 1 {
					return errors.New("task usage owner is missing")
				}
				if err := tx.Model(&model.Channel{}).Where("id = ?", current.ChannelId).
					Update("used_quota", gorm.Expr("used_quota + ?", usageDelta)).Error; err != nil {
					return err
				}
			}
		}
		current.Quota = intent.Quota
		return nil
	})
	if err != nil {
		return false, err
	}
	// A retainResult:false task keeps its result only for this live presenter.
	// Never write those bytes back when recovering or clearing its intent.
	inlineData := task.Data
	*task = *stored
	if task.PrivateData.ResultDiscarded && len(inlineData) > 0 {
		task.Data = inlineData
	}
	if !applied {
		return false, nil
	}
	if walletResult != nil {
		model.RefreshUserQuotaCache(task.UserId, walletResult.TotalAfter)
	}
	if intent.Phase != "submit" || logSubmission {
		recordRecoveredTaskBilling(task, intent, beforeQuota)
	}
	return true, nil
}

func recordRecoveredTaskBilling(task *model.Task, intent model.TaskSettlement, beforeQuota int) {
	if intent.SkipFunds {
		return
	}
	delta := intent.Quota - beforeQuota
	if intent.Phase == "submit" {
		delta = intent.Quota
	} else if delta == 0 {
		return
	}
	logType, quota := model.LogTypeConsume, delta
	if delta < 0 {
		logType, quota = model.LogTypeRefund, -delta
	}
	other := taskBillingOther(task)
	if !taskIsSubscription(task) {
		FillWalletCostLogInfo(other, task.UserId, TaskWalletCostRefs(task))
	}
	other.SetPublic("pre_consumed_quota", beforeQuota)
	other.SetPublic("actual_quota", intent.Quota)
	if intent.Phase == "submit" {
		if execution := task.PrivateData.Execution; execution != nil {
			other.SetPublic("request_path", execution.RequestPath)
		}
		if task.Status == model.TaskStatusSuccess || task.Status == model.TaskStatusFailure {
			other.SetPublic("task_sync", true)
		}
	}
	attachQuotaSaturationToOther(other, intent.Clamp)
	var content []*common.Message
	if intent.Reason != nil {
		content = []*common.Message{intent.Reason}
	}
	model.RecordTaskBillingLog(model.RecordTaskBillingLogParams{
		UserId: task.UserId, LogType: logType, Content: content, ChannelId: task.ChannelId,
		ModelName: taskModelName(task), Quota: quota, TokenId: task.PrivateData.TokenId,
		Group: task.Group, Other: other, NodeName: task.PrivateData.NodeName,
	})
}

func recoverPendingTaskSettlements(ctx context.Context) {
	tasks, err := model.GetPendingTaskSettlements(ctx, 100)
	if err != nil {
		logger.LogError(ctx, fmt.Sprintf("failed to load pending task settlements: %v", err))
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil {
			return
		}
		if err := RecoverTaskSettlement(ctx, task); err != nil {
			logger.LogError(ctx, fmt.Sprintf("task %s settlement remains pending: %v", task.TaskID, err))
		}
	}
}
