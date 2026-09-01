package model

import (
	"fmt"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm/clause"
)

const (
	AffiliateReconcileRetry     = "retry"
	AffiliateReconcileProcessed = "processed"
)

// AffiliateRewardReconcileState records durable reconciliation progress for a
// paid-ledger reference. The balance ledger remains the source of truth; this
// table prevents already reconciled task relay references from starving older
// gaps while retaining retry state for transient failures.
type AffiliateRewardReconcileState struct {
	Id                     int    `json:"id" gorm:"primaryKey;autoIncrement"`
	SourceUserId           int    `json:"source_user_id" gorm:"not null;uniqueIndex:idx_aff_reconcile_ref,priority:1"`
	ReferenceType          string `json:"reference_type" gorm:"type:varchar(64);not null;uniqueIndex:idx_aff_reconcile_ref,priority:2"`
	ReferenceId            string `json:"reference_id" gorm:"type:varchar(128);not null;uniqueIndex:idx_aff_reconcile_ref,priority:3"`
	CanonicalReferenceType string `json:"canonical_reference_type" gorm:"type:varchar(64);not null;default:''"`
	CanonicalReferenceId   string `json:"canonical_reference_id" gorm:"type:varchar(128);not null;default:''"`
	Status                 string `json:"status" gorm:"type:varchar(16);not null;index"`
	AttemptCount           int    `json:"attempt_count" gorm:"not null;default:0"`
	LastError              string `json:"last_error" gorm:"type:varchar(512);not null;default:''"`
	CreatedAt              int64  `json:"created_at" gorm:"bigint;autoCreateTime"`
	UpdatedAt              int64  `json:"updated_at" gorm:"bigint;autoUpdateTime;index"`
}

func (AffiliateRewardReconcileState) TableName() string {
	return "affiliate_reward_reconcile_states"
}

// AffiliatePaidCostCandidate is a net paid cost grouped by its durable ledger
// reference. PaidQuota is positive even though the underlying debit deltas are
// negative.
type AffiliatePaidCostCandidate struct {
	SourceUserId  int    `json:"source_user_id"`
	ReferenceType string `json:"reference_type"`
	ReferenceId   string `json:"reference_id"`
	PaidQuota     int    `json:"paid_quota"`
	LastCreatedAt int64  `json:"last_created_at"`
}

// ListAffiliatePaidCostCandidates returns unreconciled or retryable paid costs
// older than cutoff. The grace window lets async tasks persist their canonical
// task id before a relay reference is classified.
func ListAffiliatePaidCostCandidates(cutoff int64, limit int) ([]AffiliatePaidCostCandidate, error) {
	if limit <= 0 {
		limit = 200
	}
	if limit > 1000 {
		limit = 1000
	}
	rows := make([]AffiliatePaidCostCandidate, 0, limit)
	err := DB.Raw(`
SELECT
  bl.user_id AS source_user_id,
  bl.reference_type AS reference_type,
  bl.reference_id AS reference_id,
  -SUM(bl.delta) AS paid_quota,
  MAX(bl.created_at) AS last_created_at
FROM balance_ledger bl
LEFT JOIN affiliate_reward_reconcile_states rs
  ON rs.source_user_id = bl.user_id
 AND rs.reference_type = bl.reference_type
 AND rs.reference_id = bl.reference_id
WHERE bl.account = ?
  AND bl.reference_type IN (?, ?)
  AND bl.reference_id <> ''
  AND bl.created_at <= ?
  AND (rs.id IS NULL OR rs.status = ?)
GROUP BY bl.user_id, bl.reference_type, bl.reference_id
HAVING SUM(bl.delta) < 0
ORDER BY MAX(bl.created_at) ASC
LIMIT ?`, string(AccountPaid), "relay", "task", cutoff, AffiliateReconcileRetry, limit).
		Scan(&rows).Error
	return rows, err
}

// SaveAffiliateRewardReconcileState atomically upserts reconciliation status.
func SaveAffiliateRewardReconcileState(state AffiliateRewardReconcileState) error {
	if state.SourceUserId <= 0 || state.ReferenceType == "" || state.ReferenceId == "" || state.Status == "" {
		return fmt.Errorf("invalid affiliate reconciliation state")
	}
	state.AttemptCount = 1
	if state.UpdatedAt == 0 {
		state.UpdatedAt = common.GetTimestamp()
	}
	attemptCountExpression := "attempt_count + 1"
	if DB.Dialector.Name() == "postgres" {
		attemptCountExpression = `"affiliate_reward_reconcile_states"."attempt_count" + 1`
	}
	return DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "source_user_id"}, {Name: "reference_type"}, {Name: "reference_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"canonical_reference_type": state.CanonicalReferenceType,
			"canonical_reference_id":   state.CanonicalReferenceId,
			"status":                   state.Status,
			"attempt_count":            gormExpr(attemptCountExpression),
			"last_error":               state.LastError,
			"updated_at":               state.UpdatedAt,
		}),
	}).Create(&state).Error
}

// gormExpr is kept in one helper so reconciliation upserts remain concise.
func gormExpr(expression string) clause.Expr {
	return clause.Expr{SQL: expression}
}

// FindTaskByRequestId resolves an async task's submit-time relay reference.
// The LIKE query is only a candidate filter; decoded JSON is verified before
// returning to avoid wildcard or formatting false positives.
func FindTaskByRequestId(userId int, requestId string) (*Task, bool, error) {
	if userId <= 0 || requestId == "" {
		return nil, false, nil
	}
	var tasks []*Task
	// JSON serializers may insert whitespace around ':' and PostgreSQL JSONB
	// text rendering does so by default. Use the request id only as a bounded
	// candidate filter, then require an exact decoded RequestId match below.
	needle := `%` + requestId + `%`
	privateDataFilter := "private_data LIKE ?"
	if DB.Dialector.Name() == "postgres" {
		privateDataFilter = "CAST(private_data AS TEXT) LIKE ?"
	}
	if err := DB.Where("user_id = ? AND "+privateDataFilter, userId, needle).
		Order("id DESC").Limit(5).Find(&tasks).Error; err != nil {
		return nil, false, err
	}
	for _, task := range tasks {
		if task != nil && task.PrivateData.EffectiveRequestID() == requestId {
			return task, true, nil
		}
	}
	return nil, false, nil
}

// ListDueAffiliateRewardUserIds returns distinct recipients with matureable
// pending rewards so the maintenance worker can release them without relying
// on a wallet page visit.
func ListDueAffiliateRewardUserIds(now int64, limit int) ([]int, error) {
	if limit <= 0 {
		limit = 200
	}
	var ids []int
	err := DB.Model(&AffiliateReward{}).
		Where("status = ? AND unlock_at <= ?", string(AffiliateRewardPending), now).
		Distinct().Order("user_id ASC").Limit(limit).Pluck("user_id", &ids).Error
	return ids, err
}
