package model

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestSubscriptionRefundCommitsReceiptAndUsageTogether(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "refund.db")), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { sqlDB.Close() })
	require.NoError(t, db.AutoMigrate(&UserSubscription{}, &SubscriptionPreConsumeRecord{}))
	require.NoError(t, db.Create(&UserSubscription{Id: 29301, UserId: 29302, PlanId: 29303, AmountTotal: 1000, AmountUsed: 200, QuotaPeriod: 1, Status: "active"}).Error)
	require.NoError(t, db.Create(&SubscriptionPreConsumeRecord{RequestId: "atomic-sub-refund", UserId: 29302, UserSubscriptionId: 29301, PreConsumed: 100, ReservedAmount: 100, QuotaPeriod: 1, Status: "consumed"}).Error)
	originalDB := DB
	t.Cleanup(func() { DB = originalDB })
	// A deadline bounds the regression if refund accidentally requests a second
	// connection while its outer transaction owns the only available connection.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	DB = db.WithContext(ctx)
	require.NoError(t, db.Exec(`CREATE TRIGGER fail_refund_receipt BEFORE UPDATE ON subscription_pre_consume_records BEGIN SELECT RAISE(ABORT, 'synthetic receipt failure'); END`).Error)
	require.ErrorContains(t, RefundSubscriptionPreConsume("atomic-sub-refund"), "synthetic receipt failure")
	var sub UserSubscription
	var rec SubscriptionPreConsumeRecord
	require.NoError(t, db.First(&sub, 29301).Error)
	require.NoError(t, db.Where("request_id = ?", "atomic-sub-refund").First(&rec).Error)
	assert.EqualValues(t, 200, sub.AmountUsed, "receipt failure must roll back the quota refund")
	assert.Equal(t, "consumed", rec.Status)
	require.NoError(t, db.Exec("DROP TRIGGER fail_refund_receipt").Error)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { results <- RefundSubscriptionPreConsume("atomic-sub-refund") })
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}
	require.NoError(t, RefundSubscriptionPreConsume("atomic-sub-refund"))
	require.NoError(t, db.First(&sub, 29301).Error)
	require.NoError(t, db.Where("request_id = ?", "atomic-sub-refund").First(&rec).Error)
	assert.EqualValues(t, 100, sub.AmountUsed, "parallel/repeated refunds must credit only once")
	assert.Equal(t, "refunded", rec.Status)
}
