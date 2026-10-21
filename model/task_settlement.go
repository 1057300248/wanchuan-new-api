package model

import (
	"context"
	"errors"

	"github.com/QuantumNous/new-api/common"

	"gorm.io/gorm"
)

// GetPendingTaskSettlements includes terminal and discarded-inline tasks. They
// require no provider request or plugin reload to finish their saved adjustment.
func GetPendingTaskSettlements(ctx context.Context, limit int) ([]*Task, error) {
	var tasks []*Task
	err := DB.WithContext(ctx).Where("settlement_pending = ?", true).Order("COALESCE(settlement_attempted_at, 0), id").Limit(limit).Find(&tasks).Error
	return tasks, err
}

// CommitPendingTaskSettlement serializes observers on the task row. The callback
// and clearing the intent commit together in the primary database; a callback
// failure leaves the durable intent untouched for retry. SQLite conflicting
// writers may return busy and retry on a later polling pass, never double-apply.
func CommitPendingTaskSettlement(ctx context.Context, id int64, apply func(*gorm.DB, *Task) error) (*Task, bool, error) {
	var stored Task
	applied := false
	err := DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockForUpdate(tx).First(&stored, id).Error; err != nil {
			return err
		}
		if !stored.SettlementPending {
			return nil
		}
		if stored.PrivateData.PendingSettlement == nil {
			return errors.New("task pending settlement has no durable intent")
		}
		if err := apply(tx, &stored); err != nil {
			return err
		}
		stored.SettlementPending = false
		stored.PrivateData.PendingSettlement = nil
		result := tx.Model(&Task{}).Where("id = ? AND settlement_pending = ?", id, true).
			Select("quota", "private_data", "settlement_pending").Updates(&stored)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return errors.New("task pending settlement changed concurrently")
		}
		applied = true
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &stored, applied, nil
}

// NoteTaskSettlementAttempt rotates repeatedly failing intents behind untried
// rows, preventing the oldest batch from starving later settlements.
func NoteTaskSettlementAttempt(ctx context.Context, id int64) error {
	return DB.WithContext(ctx).Model(&Task{}).Where("id = ? AND settlement_pending = ?", id, true).
		UpdateColumn("settlement_attempted_at", common.GetTimestamp()).Error
}
