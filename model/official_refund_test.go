package model

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Exercise the original subscription refund entry point on owned PostgreSQL.
// The allowance and receipt must commit together, including a repeated refund.
func TestOfficialSubscriptionRefundDatabaseBehavior(t *testing.T) {
	for _, dialect := range []struct {
		kind common.DatabaseType
		env  string
	}{
		{common.DatabaseTypePostgreSQL, "TEST_POSTGRES_DSN"},
	} {
		t.Run(string(dialect.kind), func(t *testing.T) {
			dsn := os.Getenv(dialect.env)
			if dialect.env != "" && dsn == "" {
				t.Skip(dialect.env + " not configured")
			}
			db := subscriptionPeriodDatabase(t, dialect.kind, dsn)
			require.NoError(t, db.AutoMigrate(&SubscriptionPlan{}, &UserSubscription{}, &SubscriptionPreConsumeRecord{}))
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(4)
			plan, sub := subscriptionPeriodSeed(t, db)
			plan.QuotaResetPeriod = SubscriptionResetNever
			require.NoError(t, db.Session(&gorm.Session{SkipHooks: true}).Save(plan).Error)
			InvalidateSubscriptionPlanCache(plan.Id)
			res, err := PreConsumeUserSubscription("official-receipt", sub.UserId, "model", 0, 100)
			require.NoError(t, err)
			assert.Positive(t, res.QuotaPeriod)
			assert.EqualValues(t, 100, res.ReservedAmount)
			var before SubscriptionPreConsumeRecord
			require.NoError(t, db.Where("request_id = ?", "official-receipt").First(&before).Error)
			assert.EqualValues(t, 100, before.PreConsumed)
			assert.Equal(t, res.QuotaPeriod, before.QuotaPeriod)
			assert.EqualValues(t, 100, before.ReservedAmount)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			original := DB
			DB = db.WithContext(ctx)
			err = RefundSubscriptionPreConsume("official-receipt")
			DB = original
			var after SubscriptionPreConsumeRecord
			require.NoError(t, db.First(&after, before.Id).Error)
			require.NoError(t, db.First(sub, sub.Id).Error)
			require.NoError(t, err)
			assert.NoError(t, ctx.Err(), "the watchdog must not explain a refund failure")
			assert.Equal(t, "refunded", after.Status)
			assert.Zero(t, sub.AmountUsed)
			require.NoError(t, RefundSubscriptionPreConsume("official-receipt"))
			require.NoError(t, db.First(sub, sub.Id).Error)
			assert.Zero(t, sub.AmountUsed)
		})
	}
}
