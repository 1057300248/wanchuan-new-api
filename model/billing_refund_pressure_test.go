package model

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// This opt-in workload is invoked only inside the fresh dedicated operations
// matrix DB. It creates real debit operations and pending receipt owners, not
// generated timing-only rows. No numeric capacity/SLA assertion is invented.
func testBillingRefundQueuePressure(t *testing.T, db *gorm.DB) {
	const total, poisoned, initialQuota = 2500, 2490, 10000
	const userID, tokenID = 95001, 95002
	setupStarted := time.Now()
	require.NoError(t, db.Create(&User{Id: userID, Username: "pressure-owner", AffCode: "pressure-owner", Quota: initialQuota, QuotaPaid: initialQuota}).Error)
	require.NoError(t, db.Create(&Token{Id: tokenID, UserId: userID, Key: "synthetic-pressure-original", RemainQuota: initialQuota - poisoned, UsedQuota: poisoned}).Error)
	for i := range total {
		request := fmt.Sprintf("pressure-%04d", i)
		_, err := DebitQuota(LedgerEntry{UserId: userID, Delta: 1, Reason: LedgerReasonBillingPreConsume, ReferenceType: "relay", ReferenceId: request, RequestId: request, IdempotencyKey: "billing:preconsume:" + request}, AccountPaid)
		require.NoError(t, err)
		r := BillingFinalization{RequestID: request, UserID: userID, Source: "wallet", SourceHeld: 1, PreConsumed: 1, Paid: 1}
		if i < poisoned {
			r.TokenID, r.TokenHeld, r.TokenFingerprint = tokenID, 1, BillingTokenFingerprint("synthetic-pressure-original")
		}
		_, err = PersistBillingRefund(context.Background(), r)
		require.NoError(t, err)
	}
	require.NoError(t, db.Model(&Token{}).Where("id = ?", tokenID).Update("key", "synthetic-pressure-replacement").Error)
	setupElapsed := time.Since(setupStarted)
	workerStarted := time.Now()
	completed := 0
	for pass := range 25 {
		n, err := RecoverPendingBillingRefunds(context.Background(), 100)
		// Each batch contains unrecoverable original-token identity at this
		// instant, but healthy authorized money at the tail still progresses.
		require.Error(t, err, "pass %d must expose its failed identities", pass)
		completed += n
	}
	workerElapsed := time.Since(workerStarted)
	require.Equal(t, total-poisoned, completed)
	var pending, escalated, refunded int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("user_id = ? AND state = ?", userID, BillingRefundPending).Count(&pending).Error)
	require.NoError(t, db.Model(&BillingFinalization{}).Where("user_id = ? AND state = ? AND attempts = ? AND last_error_code = ? AND escalated_at > ?", userID, BillingRefundPending, 1, "identity_mismatch", 0).Count(&escalated).Error)
	require.NoError(t, db.Model(&BillingFinalization{}).Where("user_id = ? AND state = ?", userID, BillingRefunded).Count(&refunded).Error)
	assert.EqualValues(t, poisoned, pending)
	assert.EqualValues(t, poisoned, escalated)
	assert.EqualValues(t, total-poisoned, refunded)
	var user User
	var token Token
	require.NoError(t, db.First(&user, userID).Error)
	require.NoError(t, db.First(&token, tokenID).Error)
	assert.Equal(t, initialQuota-poisoned, user.Quota)
	assert.Equal(t, initialQuota-poisoned, user.QuotaPaid)
	assert.Equal(t, initialQuota-poisoned, token.RemainQuota, "replacement credential receives no original credit")
	assert.Equal(t, poisoned, token.UsedQuota)
	var credits int64
	require.NoError(t, db.Model(&BalanceLedger{}).Where("user_id = ? AND reason = ?", userID, LedgerReasonBillingRefund).Count(&credits).Error)
	assert.EqualValues(t, total-poisoned, credits, "every healthy receipt credited once and poison credits rolled back")
	quietStarted := time.Now()
	n, err := RecoverPendingBillingRefunds(context.Background(), 100)
	require.NoError(t, err)
	assert.Zero(t, n, "scheduled poison rows must not hot-loop in the next pass")
	var retried int64
	require.NoError(t, db.Model(&BillingFinalization{}).Where("user_id = ? AND state = ? AND attempts <> ?", userID, BillingRefundPending, 1).Count(&retried).Error)
	assert.Zero(t, retried)
	t.Logf("real refund queue pressure: total=%d poisoned_pending=%d healthy_completed=%d setup=%s recovery_25_passes=%s repeated_due_empty_pass=%s", total, poisoned, completed, setupElapsed, workerElapsed, time.Since(quietStarted))
}
