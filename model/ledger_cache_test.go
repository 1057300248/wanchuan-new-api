package model

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Read the committed balance at the precise cache-miss interleaving which used
// to be followed by an obsolete aggregate increment. No timing or load loop is
// needed: DEL has completed, but the credit caller has not resumed yet.
type ledgerCacheHydrationHook struct {
	userID   int
	hydrated atomic.Bool
	err      error
}

// Redis v8 hooks preserve the same interleaving: hydrate before the obsolete
// EVAL control or after a successful DEL, before the mutation caller resumes.
func (h *ledgerCacheHydrationHook) BeforeProcess(ctx context.Context, cmd redis.Cmder) (context.Context, error) {
	args := cmd.Args()
	if cmd.Name() == "eval" && len(args) > 3 && args[1] == userQuotaDeltaScript && args[3] == getUserCacheKey(h.userID) {
		if h.hydrated.CompareAndSwap(false, true) {
			_, h.err = GetUserCache(h.userID)
		}
	}
	return ctx, nil
}

func (h *ledgerCacheHydrationHook) AfterProcess(ctx context.Context, cmd redis.Cmder) error {
	args := cmd.Args()
	if cmd.Err() == nil && cmd.Name() == "del" && len(args) == 2 && args[1] == getUserCacheKey(h.userID) {
		if h.hydrated.CompareAndSwap(false, true) {
			_, h.err = GetUserCache(h.userID)
		}
	}
	return nil
}

func (h *ledgerCacheHydrationHook) BeforeProcessPipeline(ctx context.Context, cmds []redis.Cmder) (context.Context, error) {
	return ctx, nil
}

func (h *ledgerCacheHydrationHook) AfterProcessPipeline(ctx context.Context, cmds []redis.Cmder) error {
	return nil
}

func TestLedgerMutationDoesNotApplyCreditTwiceAfterCacheHydration(t *testing.T) {
	for _, tc := range []struct {
		name    string
		want    int
		reserve int
		apply   func(*testing.T, int)
	}{
		{name: "epay", want: 1210, apply: func(t *testing.T, userID int) {
			order := createEpayTestOrder(t, userID, "CACHEHYDRATEEPAY", PaymentProviderEpay, common.TopUpStatusPending)
			_, err := RechargeEpay(order.TradeNo, "alipay", "10.00", "127.0.0.1")
			require.NoError(t, err)
		}},
		{name: "redemption", want: 1700, apply: func(t *testing.T, userID int) {
			require.NoError(t, DB.AutoMigrate(&Redemption{}))
			code := Redemption{Key: "cache-hydrate-redemption", Name: "cache regression", Quota: 500, Status: common.RedemptionCodeStatusEnabled}
			require.NoError(t, DB.Create(&code).Error)
			t.Cleanup(func() { require.NoError(t, DB.Unscoped().Delete(&code).Error) })
			quota, err := Redeem(code.Key, userID)
			require.NoError(t, err)
			assert.Equal(t, 500, quota)
		}},
		{name: "admin_add_with_ledger_reservation", want: 1600, reserve: 100, apply: func(t *testing.T, userID int) {
			_, err := AdjustUserQuota(userID, common.RoleRootUser, "add", 500, LedgerEntry{IdempotencyKey: "cache-admin-add"})
			require.NoError(t, err)
		}},
		{name: "admin_subtract", want: 700, apply: func(t *testing.T, userID int) {
			_, err := AdjustUserQuota(userID, common.RoleRootUser, "subtract", 500, LedgerEntry{IdempotencyKey: "cache-admin-subtract"})
			require.NoError(t, err)
		}},
		{name: "admin_override", want: 2200, apply: func(t *testing.T, userID int) {
			_, err := AdjustUserQuota(userID, common.RoleRootUser, "override", 2000, LedgerEntry{IdempotencyKey: "cache-admin-override"})
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			truncateTables(t)
			server := useUserCacheMiniRedis(t)
			previousUnit := common.QuotaPerUnit
			common.QuotaPerUnit = 5
			t.Cleanup(func() { common.QuotaPerUnit = previousUnit })
			user := User{Username: "ledger-cache-race", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, AuthVersion: 1, Quota: 1200, QuotaPaid: 1000, QuotaBonus: 200}
			require.NoError(t, DB.Create(&user).Error)
			if tc.reserve > 0 {
				_, err := DebitQuota(LedgerEntry{UserId: user.Id, Delta: tc.reserve, Reason: LedgerReasonBillingPreConsume, IdempotencyKey: "cache-live-reservation"}, AccountPaid)
				require.NoError(t, err)
			}
			server.Del(getUserCacheKey(user.Id))
			hook := &ledgerCacheHydrationHook{userID: user.Id}
			common.RDB.AddHook(hook)

			tc.apply(t, user.Id)

			require.True(t, hook.hydrated.Load(), "the committed-read interleaving must execute")
			require.NoError(t, hook.err)
			require.NoError(t, DB.First(&user, user.Id).Error)
			assert.Equal(t, tc.want, user.Quota)
			assert.Equal(t, 200, user.QuotaBonus)
			assert.Equal(t, user.Quota, user.QuotaPaid+user.QuotaBonus+user.QuotaReward)
			cached, err := GetUserCache(user.Id)
			require.NoError(t, err)
			assert.Equal(t, tc.want, cached.Quota)
		})
	}
}
