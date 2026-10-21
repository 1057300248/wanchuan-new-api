package model

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Candidate-only continuation of TestReleaseUpgradePreservesWalletAndIdentity.
// Run pending, recover, then replay in THREE separate candidate processes, after
// the exact-release seed and candidate historical-data verification have passed.
// SQL_DSN/LOG_SQL_DSN must be the runner-owned disposable upgrade databases. This
// test never creates/drops databases or starts the runtime scheduler/providers.
func TestReleaseUpgradeBillingFinalization(t *testing.T) {
	phase := os.Getenv("TEST_RELEASE_FINALIZATION_PHASE")
	if phase == "" {
		t.Skip("set TEST_RELEASE_FINALIZATION_PHASE=pending, recover, then replay on the disposable release-upgrade database")
	}
	require.Contains(t, []string{"pending", "recover", "replay"}, phase)
	require.NotEmpty(t, os.Getenv("SQL_DSN"))
	require.NotEmpty(t, os.Getenv("LOG_SQL_DSN"))
	if path := os.Getenv("TEST_RELEASE_SQLITE_PATH"); path != "" {
		previous := common.SQLitePath
		common.SQLitePath = path
		t.Cleanup(func() { common.SQLitePath = previous })
	}
	previousMaster := common.IsMasterNode
	common.IsMasterNode = true
	t.Cleanup(func() { common.IsMasterNode = previousMaster })
	installDatabaseCompatRegistry(t)
	require.NoError(t, InitDB())
	require.NoError(t, InitLogDB())
	t.Cleanup(func() { require.NoError(t, CloseDB()) })

	// Sequence check only: migrations have already run. The invoking runner,
	// not this post-startup marker, must establish disposable database ownership.
	var historical User
	require.NoError(t, DB.Where("username = ?", "release-upgrade-owner").First(&historical).Error)
	require.Equal(t, "existing-password-hash", historical.Password)
	require.Equal(t, 5000, historical.Quota)
	require.Equal(t, 4000, historical.QuotaPaid)
	require.Equal(t, 1000, historical.QuotaBonus)
	require.EqualValues(t, 4294967400, historical.UsedQuota)
	require.True(t, DB.Migrator().HasTable(&BillingFinalization{}))
	require.True(t, DB.Migrator().HasIndex(&BillingFinalization{}, "idx_billing_refund_queue"))

	var before []BillingFinalization
	require.NoError(t, DB.Order("request_id").Find(&before).Error)
	if phase == "pending" {
		require.Empty(t, before, "refuse to seed an already-used terminal fixture")
	} else {
		require.Len(t, before, 3, "prior candidate phase must have persisted exactly the three fixture owners")
	}
	// Repeated production migrations must preserve the whole persisted owner,
	// including original split, identity, destination and terminal timestamps.
	for range 2 {
		require.NoError(t, CloseDB())
		require.NoError(t, InitDB())
		require.NoError(t, InitLogDB())
		var after []BillingFinalization
		require.NoError(t, DB.Order("request_id").Find(&after).Error)
		require.Equal(t, before, after)
	}

	ctx := context.Background()
	if phase == "pending" {
		for _, kind := range []string{"pending", "settled", "task"} {
			requestID := "release-finalization-" + kind
			user := User{Username: requestID, AffCode: requestID, Quota: 1000, QuotaPaid: 40, QuotaBonus: 30, QuotaReward: 930}
			require.NoError(t, DB.Create(&user).Error)
			token := Token{UserId: user.Id, Key: strings.Repeat(string(kind[0]), 48), Name: requestID,
				Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 1000}
			require.NoError(t, DB.Create(&token).Error)
			r := BillingFinalization{RequestID: requestID, UserID: user.Id, Source: "wallet", TokenID: token.Id,
				TokenFingerprint: BillingTokenFingerprint(token.Key), SourceHeld: 100, TokenHeld: 100, PreConsumed: 100, Paid: 40, Bonus: 30, Reward: 30}
			require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
				_, err := DebitQuotaTx(tx, LedgerEntry{UserId: user.Id, Delta: 100, Reason: LedgerReasonBillingPreConsume,
					ReferenceType: "relay", ReferenceId: requestID, RequestId: requestID, IdempotencyKey: "billing:preconsume:" + requestID}, AccountPaid, AccountBonus, AccountReward)
				if err != nil {
					return err
				}
				return ApplyBillingTokenDeltaTx(tx, &r, -100)
			}))
			switch kind {
			case "pending":
				state, err := PersistBillingRefund(ctx, r)
				require.NoError(t, err)
				require.Equal(t, BillingRefundPending, state)
			case "settled":
				applied, err := SettleBillingFinalization(ctx, r, 100, func(tx *gorm.DB) error { return ApplyBillingTokenDeltaTx(tx, &r, 0) })
				require.NoError(t, err)
				require.True(t, applied)
			case "task":
				task := &Task{TaskID: requestID + "-public", UserId: user.Id, Quota: 100, SettlementPending: true,
					PrivateData: TaskPrivateData{RequestId: requestID, BillingSource: "wallet", TokenId: token.Id,
						WalletDebit: r.Shares(), PendingSettlement: &TaskSettlement{Phase: "submit", Quota: 180}}}
				require.NoError(t, InsertTaskWithBillingFinalization(ctx, r, task))
			}
		}
		// Persisted intent must survive a real source-write failure and process
		// exit without independently restoring the token or marking success.
		const callback = "release_finalization_source_failure"
		require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
			if tx.Statement.Table == "users" {
				tx.AddError(errors.New("synthetic release recovery source failure"))
			}
		}))
		err := RecoverBillingRefund(ctx, "release-finalization-pending")
		require.NoError(t, DB.Callback().Update().Remove(callback))
		require.ErrorContains(t, err, "synthetic release recovery source failure")
	}

	verify := func(pendingState string) {
		t.Helper()
		for _, kind := range []string{"pending", "settled", "task"} {
			requestID := "release-finalization-" + kind
			var user User
			require.NoError(t, DB.Where("username = ?", requestID).First(&user).Error)
			var token Token
			require.NoError(t, DB.Where("user_id = ? AND name = ?", user.Id, requestID).First(&token).Error)
			var owner BillingFinalization
			require.NoError(t, DB.First(&owner, "request_id = ?", requestID).Error)
			expected := BillingFinalization{RequestID: requestID, UserID: user.Id, Source: "wallet", TokenID: token.Id,
				TokenFingerprint: BillingTokenFingerprint(strings.Repeat(string(kind[0]), 48)), SourceHeld: 100, TokenHeld: 100, PreConsumed: 100, Paid: 40, Bonus: 30, Reward: 30}
			require.True(t, sameBillingIdentity(expected, owner))
			require.Zero(t, owner.Attempts)
			quota, used, paid, bonus, reward := 900, 100, 0, 0, 900
			switch kind {
			case "pending":
				require.Equal(t, pendingState, owner.State)
				require.Zero(t, owner.ActualQuota)
				if pendingState == BillingRefunded {
					quota, used, paid, bonus, reward = 1000, 0, 40, 30, 930
				}
			case "settled":
				require.Equal(t, BillingSettled, owner.State)
				require.Equal(t, 100, owner.ActualQuota)
			case "task":
				require.Equal(t, BillingTaskOwned, owner.State)
				require.Equal(t, requestID+"-public", owner.TaskPublicID)
				var task Task
				require.NoError(t, DB.First(&task, owner.TaskID).Error)
				require.True(t, billingTaskIdentityMatches(&owner, &task))
				require.Equal(t, 100, task.Quota)
				require.True(t, task.SettlementPending)
				require.NotNil(t, task.PrivateData.PendingSettlement)
				require.Equal(t, 180, task.PrivateData.PendingSettlement.Quota)
			}
			require.Equal(t, quota, user.Quota)
			require.Equal(t, paid, user.QuotaPaid)
			require.Equal(t, bonus, user.QuotaBonus)
			require.Equal(t, reward, user.QuotaReward)
			require.Equal(t, quota, token.RemainQuota)
			require.Equal(t, used, token.UsedQuota)
		}
	}
	if phase == "replay" {
		verify(BillingRefunded)
	} else {
		verify(BillingRefundPending)
	}
	if phase != "pending" {
		completed, err := RecoverPendingBillingRefunds(ctx, 100)
		require.NoError(t, err)
		if phase == "recover" {
			require.Equal(t, 1, completed)
		} else {
			require.Zero(t, completed)
		}
		require.NoError(t, RecoverBillingRefund(ctx, "release-finalization-pending"))
		verify(BillingRefunded)
		for _, previous := range before {
			if previous.State == BillingSettled || previous.State == BillingTaskOwned {
				var current BillingFinalization
				require.NoError(t, DB.First(&current, "request_id = ?", previous.RequestID).Error)
				require.Equal(t, previous, current, "request worker must not mutate another terminal owner")
			}
		}
	}
	var owner BillingFinalization
	require.NoError(t, DB.First(&owner, "request_id = ?", "release-finalization-settled").Error)
	require.Error(t, DB.Create(&owner).Error, "request ownership uniqueness survives repeated startup")
	var finalHistorical User
	require.NoError(t, DB.First(&finalHistorical, historical.Id).Error)
	require.Equal(t, historical, finalHistorical, "new fixture must not mutate the exact-release historical owner")
}
