package model

import (
	"math"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func createReserveTestUser(t *testing.T, quota int) User {
	t.Helper()
	user := User{
		Username:    "reserve-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       quota,
		QuotaPaid:   quota,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	return user
}

func createReserveTestToken(t *testing.T, remainQuota int) Token {
	t.Helper()
	token := Token{
		UserId:      1,
		Key:         "reserve-token-" + common.GetRandomString(8),
		Name:        "reserve-test",
		Status:      common.TokenStatusEnabled,
		ExpiredTime: -1,
		RemainQuota: remainQuota,
	}
	require.NoError(t, token.Insert())
	return token
}

func getUserQuotaFromDB(t *testing.T, id int) int {
	t.Helper()
	var user User
	require.NoError(t, DB.Select("quota").First(&user, id).Error)
	return user.Quota
}

func getTokenFromDB(t *testing.T, id int) Token {
	t.Helper()
	var token Token
	require.NoError(t, DB.First(&token, id).Error)
	return token
}

func resetBatchUpdateTestState(t *testing.T) {
	t.Helper()
	oldBatchEnabled := common.BatchUpdateEnabled
	common.BatchUpdateEnabled = false
	for i := 0; i < BatchUpdateTypeCount; i++ {
		batchUpdateLocks[i].Lock()
		batchUpdateStores[i] = make(map[int]int)
		batchUpdateLocks[i].Unlock()
	}
	t.Cleanup(func() {
		common.BatchUpdateEnabled = oldBatchEnabled
		for i := 0; i < BatchUpdateTypeCount; i++ {
			batchUpdateLocks[i].Lock()
			batchUpdateStores[i] = make(map[int]int)
			batchUpdateLocks[i].Unlock()
		}
	})
}

func TestTryReserveUserQuotaFailsClosed(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	// ChuanAPI 下 users.quota 是三账户兼容镜像，只能由账本事务维护；
	// 上游单列预扣 API 无安全调用条件，Redis 关闭时也必须 fail-closed。
	user := createReserveTestUser(t, 100)
	reserved, err := TryReserveUserQuota(user.Id, 60)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 100, getUserQuotaFromDB(t, user.Id), "reserve must not touch the quota mirror")
}

// TestTryReserveUserQuotaContractViaLedger 验证 ChuanAPI 的等价预扣契约：
// 余额只通过 DebitQuota 账本路径扣减，paid 优先、余额不足拒绝、quota 镜像
// 同步、balance_ledger 落账。
func TestTryReserveUserQuotaContractViaLedger(t *testing.T) {
	truncateTables(t)
	setupLedgerTestState(t)

	user := User{
		Username:    "reserve-ledger-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       100,
		QuotaPaid:   100,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 60)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)

	// 账本等价路径：预扣 60，paid 全额承担。
	result, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          60,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-1",
		IdempotencyKey: "test:reserve-ledger-debit-1",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	got := getUserBalances(t, user.Id)
	assert.Equal(t, 40, got.QuotaPaid)
	assert.Equal(t, 0, got.QuotaBonus)
	assert.Equal(t, 0, got.QuotaAffiliate)
	assert.Equal(t, 40, got.Quota)
	assertQuotaInvariant(t, got)

	// 余额不足时拒绝，且不破坏镜像。
	_, err = DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          41,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-2",
		IdempotencyKey: "test:reserve-ledger-debit-2",
	}, AccountPaid, AccountBonus)
	require.ErrorIs(t, err, ErrLedgerInsufficientBalance)
	got = getUserBalances(t, user.Id)
	assert.Equal(t, 40, got.Quota)
	assertQuotaInvariant(t, got)

	// 幂等重放：同键同参数不重复扣减。
	replayed, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          60,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "reserve-ledger-debit-1",
		IdempotencyKey: "test:reserve-ledger-debit-1",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.True(t, replayed.Replayed)
	assert.Equal(t, 40, getUserBalances(t, user.Id).Quota)
}

func TestTryReserveTokenQuotaWithoutRedis(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)

	token := createReserveTestToken(t, 80)
	reserved, err := TryReserveTokenQuota(token.Id, token.Key, 25, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reloaded := getTokenFromDB(t, token.Id)
	assert.Equal(t, 55, reloaded.RemainQuota)
	assert.Equal(t, 25, reloaded.UsedQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 56, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 55, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestBatchReserveUserQuotaFailsClosedWhileTokenBatchSurvives(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)
	common.BatchUpdateEnabled = true

	user := User{
		Username:    "reserve-batch-user-" + common.GetRandomString(6),
		Password:    "unused-password-hash",
		Role:        common.RoleCommonUser,
		Status:      common.UserStatusEnabled,
		Group:       "default",
		AuthVersion: 1,
		Quota:       10,
		QuotaPaid:   10,
		AffCode:     "reserve-aff-" + common.GetRandomString(8),
	}
	require.NoError(t, DB.Create(&user).Error)
	// 批量模式启用时，上游会把缓存预扣增量入队并在 batchUpdate 时落库；
	// ChuanAPI 下该路径会绕过账本，因此 reserve 必须 fail-closed，且不触碰
	// 缓存与数据库余额。
	reserved, err := TryReserveUserQuota(user.Id, 8)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 10, getUserQuotaFromDB(t, user.Id))

	// 等价账本路径立即可见，不依赖批量落盘。
	result, err := DebitQuota(LedgerEntry{
		UserId:         user.Id,
		Delta:          8,
		Reason:         LedgerReasonBillingPreConsume,
		ReferenceType:  "test",
		ReferenceId:    "batch-ledger-debit",
		IdempotencyKey: "test:batch-ledger-debit",
	}, AccountPaid, AccountBonus)
	require.NoError(t, err)
	require.False(t, result.Replayed)
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id), "ledger debit updates the mirror immediately")

	token := createReserveTestToken(t, 9)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 3, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 9, getTokenFromDB(t, token.Id).RemainQuota)

	batchUpdate()
	// token 批量落库保留；user 镜像由账本事务维护，batchUpdate 不刷新该类型。
	assert.Equal(t, 2, getUserQuotaFromDB(t, user.Id))
	reloadedToken := getTokenFromDB(t, token.Id)
	assert.Equal(t, 2, reloadedToken.RemainQuota)
	assert.Equal(t, 7, reloadedToken.UsedQuota)
}

func TestLedgerAccumulatesTwoMaximumRequestCharges(t *testing.T) {
	truncateTables(t)
	setupLedgerTestState(t)

	user := createReserveTestUser(t, common.MaxQuota*2+100)
	for _, requestID := range []string{"max-charge-1", "max-charge-2"} {
		result, err := DebitQuota(LedgerEntry{
			UserId:         user.Id,
			Delta:          common.MaxQuota,
			Reason:         LedgerReasonBillingPreConsume,
			ReferenceType:  "test",
			ReferenceId:    requestID,
			RequestId:      requestID,
			IdempotencyKey: "test:" + requestID,
		}, AccountPaid)
		require.NoError(t, err)
		require.False(t, result.Replayed)
	}

	balances := getUserBalances(t, user.Id)
	assert.Equal(t, 100, balances.QuotaPaid)
	assert.Equal(t, 100, balances.Quota)
	assertQuotaInvariant(t, balances)

	var ledgerCount int64
	require.NoError(t, DB.Model(&BalanceLedger{}).
		Where("user_id = ? AND reason = ?", user.Id, LedgerReasonBillingPreConsume).
		Count(&ledgerCount).Error)
	assert.EqualValues(t, 2, ledgerCount)
}

func TestBatchUpdateAccumulatorSaturatesOverflow(t *testing.T) {
	resetBatchUpdateTestState(t)

	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MaxInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, 1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MaxInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()

	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	batchUpdateStores[BatchUpdateTypeUserQuota] = make(map[int]int)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
	addNewRecord(BatchUpdateTypeUserQuota, 1, math.MinInt)
	addNewRecord(BatchUpdateTypeUserQuota, 1, -1)
	batchUpdateLocks[BatchUpdateTypeUserQuota].Lock()
	assert.Equal(t, math.MinInt, batchUpdateStores[BatchUpdateTypeUserQuota][1])
	batchUpdateLocks[BatchUpdateTypeUserQuota].Unlock()
}

func TestReserveFallsBackToDatabaseWhenRedisIsUnavailable(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	user := createReserveTestUser(t, 20)
	require.NoError(t, populateUserCache(user))
	server.Close()

	// Redis 故障时 user reserve 同样 fail-closed：不降级为绕过账本的单列扣减。
	reserved, err := TryReserveUserQuota(user.Id, 5)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	assert.Equal(t, 20, getUserQuotaFromDB(t, user.Id))

	// token 降级路径保留：数据库条件更新保证服务可用且不会超扣。
	token := createReserveTestToken(t, 20)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 5, false)
	require.NoError(t, err)
	assert.True(t, reserved)
	assert.Equal(t, 15, getTokenFromDB(t, token.Id).RemainQuota)

	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 16, false)
	require.NoError(t, err)
	assert.False(t, reserved)
	assert.Equal(t, 15, getTokenFromDB(t, token.Id).RemainQuota)
}

func TestSynchronousReserveCompensatesCacheWhenPersistenceFails(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	useUserCacheMiniRedis(t)

	// fail-closed 的 user reserve 不触碰缓存：已水合的缓存余额保持不变。
	user := createReserveTestUser(t, 10)
	require.NoError(t, populateUserCache(user))
	require.NoError(t, DB.Delete(&user).Error)

	reserved, err := TryReserveUserQuota(user.Id, 6)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, ErrUserQuotaReserveUnavailable)
	cached, cacheErr := cacheGetUserBase(user.Id)
	require.NoError(t, cacheErr)
	assert.Equal(t, 10, cached.Quota)

	// token 补偿路径保留：用户/令牌删除后回滚缓存预扣。
	token := createReserveTestToken(t, 12)
	_, err = GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	require.NoError(t, DB.Delete(&token).Error)
	reserved, err = TryReserveTokenQuota(token.Id, token.Key, 7, false)
	assert.False(t, reserved)
	assert.ErrorIs(t, err, gorm.ErrRecordNotFound)
	cachedToken, cacheErr := cacheGetTokenByKey(token.Key)
	require.NoError(t, cacheErr)
	assert.Equal(t, 12, cachedToken.RemainQuota)
	assert.Zero(t, cachedToken.UsedQuota)
}

func TestTokenCacheInitPreservesLiveQuotaAndFenceBlocksStaleSnapshot(t *testing.T) {
	truncateTables(t)
	resetBatchUpdateTestState(t)
	server := useUserCacheMiniRedis(t)

	token := createReserveTestToken(t, 100)
	loaded, err := GetTokenByKey(token.Key, true)
	require.NoError(t, err)
	stale := *loaded

	result, err := cacheApplyTokenQuotaDelta(token.Id, token.Key, -70)
	require.NoError(t, err)
	require.Equal(t, cacheQuotaOK, result)

	// 已存在的哈希只刷新 TTL：数据库快照不得覆盖已被原子预扣的余额。
	code, err := cacheInitToken(stale)
	require.NoError(t, err)
	assert.Equal(t, 2, code)
	cached, err := cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 30, cached.RemainQuota)

	// 变更期间：fence 删除缓存并拦截并发读者手中的过期快照。
	require.NoError(t, invalidateTokenCacheForMutation(token.Key))
	code, err = cacheInitToken(stale)
	require.NoError(t, err)
	assert.Zero(t, code, "the pre-mutation snapshot must not be published while fenced")
	_, err = cacheGetTokenByKey(token.Key)
	assert.Error(t, err)

	// fence 过期后可重新从数据库水合。
	server.FastForward(time.Duration(tokenCacheFenceSeconds+1) * time.Second)
	fresh, err := GetTokenByKey(token.Key, false)
	require.NoError(t, err)
	assert.Equal(t, 100, fresh.RemainQuota)
	cached, err = cacheGetTokenByKey(token.Key)
	require.NoError(t, err)
	assert.Equal(t, 100, cached.RemainQuota)
}
