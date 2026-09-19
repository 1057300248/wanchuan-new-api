package model

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/ratio_setting"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGroupNamespaceConcurrentWriters(t *testing.T) {
	sqlDB, err := DB.DB()
	require.NoError(t, err)
	previousMax := sqlDB.Stats().MaxOpenConnections
	sqlDB.SetMaxOpenConns(max(previousMax, 4))
	t.Cleanup(func() {
		sqlDB.SetMaxOpenConns(previousMax)
	})

	type scenario struct {
		name string
		seed func(*testing.T) (func() error, func(*testing.T))
	}
	scenarios := []scenario{
		{
			name: "channel_insert",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				stale := &Channel{Name: "stale-insert", Key: "sk-stale-insert", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				return stale.Insert, func(t *testing.T) {
					var count int64
					require.NoError(t, DB.Model(&Channel{}).Where(commonGroupCol+" = ?", "old").Count(&count).Error)
					assert.Zero(t, count)
				}
			},
		},
		{
			name: "channel_update",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				channel := Channel{Name: "stale-channel", Key: "sk-stale-channel", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				require.NoError(t, DB.Create(&channel).Error)
				stale := channel
				stale.Name = "stale-write"
				return stale.Update, func(t *testing.T) {
					var stored Channel
					require.NoError(t, DB.First(&stored, channel.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "token_group_and_auto_groups",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "token-user", AffCode: "token-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				token := Token{UserId: user.Id, Key: "stale-token", Name: "stale", Status: common.TokenStatusEnabled, Group: "old", AutoGroups: `["old"]`}
				require.NoError(t, DB.Create(&token).Error)
				stale := token
				return stale.Update, func(t *testing.T) {
					var stored Token
					require.NoError(t, DB.First(&stored, token.Id).Error)
					assert.Equal(t, "new", stored.Group)
					assert.JSONEq(t, `["new"]`, stored.AutoGroups)
				}
			},
		},
		{
			name: "user_group",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "stale-user", AffCode: "stale-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				stale := user
				return func() error { return stale.Edit(false) }, func(t *testing.T) {
					var stored User
					require.NoError(t, DB.First(&stored, user.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "user_update_group",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "stale-update-user", AffCode: "stale-update-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				stale := user
				return func() error { return stale.Update(false) }, func(t *testing.T) {
					var stored User
					require.NoError(t, DB.First(&stored, user.Id).Error)
					assert.Equal(t, "new", stored.Group)
				}
			},
		},
		{
			name: "subscription_plan",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				plan := SubscriptionPlan{Title: "stale-plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
				require.NoError(t, DB.Create(&plan).Error)
				stale := plan
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var stored SubscriptionPlan
					require.NoError(t, DB.First(&stored, plan.Id).Error)
					assert.Equal(t, "new", stored.UpgradeGroup)
					assert.Equal(t, "new", stored.DowngradeGroup)
				}
			},
		},
		{
			name: "active_user_subscription",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				user := User{Username: "sub-user", AffCode: "sub-user", Status: common.UserStatusEnabled, Group: "old"}
				require.NoError(t, DB.Create(&user).Error)
				plan := SubscriptionPlan{Title: "sub-plan", UpgradeGroup: "old", DowngradeGroup: "old", Enabled: true}
				require.NoError(t, DB.Create(&plan).Error)
				sub := UserSubscription{UserId: user.Id, PlanId: plan.Id, Status: "active", UpgradeGroup: "old", PrevUserGroup: "old", DowngradeGroup: "old"}
				require.NoError(t, DB.Create(&sub).Error)
				stale := sub
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var stored UserSubscription
					require.NoError(t, DB.First(&stored, sub.Id).Error)
					assert.Equal(t, "new", stored.UpgradeGroup)
					assert.Equal(t, "new", stored.PrevUserGroup)
					assert.Equal(t, "new", stored.DowngradeGroup)
				}
			},
		},
		{
			name: "ability",
			seed: func(t *testing.T) (func() error, func(*testing.T)) {
				channel := Channel{Name: "ability-channel", Key: "sk-ability", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
				require.NoError(t, DB.Create(&channel).Error)
				ability := Ability{Group: "old", Model: "m1", ChannelId: channel.Id, Enabled: true}
				require.NoError(t, DB.Create(&ability).Error)
				stale := ability
				return func() error { return DB.Save(&stale).Error }, func(t *testing.T) {
					var oldCount int64
					require.NoError(t, DB.Model(&Ability{}).
						Where("channel_id = ? AND model = ? AND "+commonGroupCol+" = ?", channel.Id, "m1", "old").
						Count(&oldCount).Error)
					assert.Zero(t, oldCount)
					var newCount int64
					require.NoError(t, DB.Model(&Ability{}).
						Where("channel_id = ? AND model = ? AND "+commonGroupCol+" = ?", channel.Id, "m1", "new").
						Count(&newCount).Error)
					assert.Equal(t, int64(1), newCount)
				}
			},
		},
	}

	for _, tc := range scenarios {
		t.Run(tc.name, func(t *testing.T) {
			setupGroupRenameTest(t)
			seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
			writer, verify := tc.seed(t)

			renameAtCommit := make(chan struct{})
			releaseRename := make(chan struct{})
			var hookOnce sync.Once
			previousHook := groupRenameTxFailureHook
			groupRenameTxFailureHook = func() error {
				hookOnce.Do(func() { close(renameAtCommit) })
				<-releaseRename
				return nil
			}
			t.Cleanup(func() { groupRenameTxFailureHook = previousHook })

			renameResult := make(chan error, 1)
			go func() {
				_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
				renameResult <- err
			}()
			<-renameAtCommit

			writerStarted := make(chan struct{})
			writerResult := make(chan error, 1)
			go func() {
				close(writerStarted)
				writerResult <- writer()
			}()
			<-writerStarted
			select {
			case err := <-writerResult:
				t.Fatalf("group writer completed while rename transaction still held namespace lock: %v", err)
			case <-time.After(100 * time.Millisecond):
				// The writer has entered its transaction while rename still
				// owns the namespace lock; it must remain blocked until release.
			}

			close(releaseRename)
			require.NoError(t, <-renameResult)
			writeErr := <-writerResult
			require.Error(t, writeErr)
			require.ErrorIs(t, writeErr, ErrGroupReferenceRenamed)
			verify(t)
		})
	}
}

func testGroupNamespaceRollbackState(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	previousHook := groupRenameTxFailureHook
	groupRenameTxFailureHook = func() error { return errors.New("forced namespace rollback") }
	t.Cleanup(func() { groupRenameTxFailureHook = previousHook })

	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.Error(t, err)

	var count int64
	require.NoError(t, DB.Model(&Option{}).Where("key IN ?", []string{groupRenameAliasesOptionKey, groupConfigVersionOptionKey}).Count(&count).Error)
	assert.Zero(t, count, "rename alias/version state must roll back with the transaction")

	groupRenameTxFailureHook = nil
	channel := Channel{Name: "old-after-rollback", Key: "sk-old-after-rollback", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, channel.Insert(), "rolled-back rename must not tombstone the old group")
}

func testGroupConfigPubSubAndFallback(t *testing.T) {
	setupGroupRenameTest(t)
	seedGroupRenameOption(t, "GroupRatio", `{"old":0.8}`)
	channel := Channel{Name: "pubsub-channel", Key: "sk-pubsub", Status: common.ChannelStatusEnabled, Group: "old", Models: "m1"}
	require.NoError(t, channel.Insert())

	_, err := RenameGroups([]GroupRenameMapping{{From: "old", To: "new"}})
	require.NoError(t, err)
	var versionOption Option
	require.NoError(t, DB.Where("key = ?", groupConfigVersionOptionKey).First(&versionOption).Error)
	version, err := strconv.ParseInt(versionOption.Value, 10, 64)
	require.NoError(t, err)
	require.Positive(t, version)

	previousVersion := groupConfigAppliedVersion.Load()
	t.Cleanup(func() { groupConfigAppliedVersion.Store(previousVersion) })

	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"old":0.8}`))
	groupConfigAppliedVersion.Store(version - 1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pubsub := common.RDB.Subscribe(ctx, groupConfigTopic(common.RDB.Options().DB))
	defer pubsub.Close()
	_, err = pubsub.Receive(ctx)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		receiveGroupConfigEvents(ctx, pubsub.Channel())
	}()
	require.NoError(t, PublishGroupConfigInvalidation(version, []GroupRenameMapping{{From: "old", To: "new"}}))
	require.Eventually(t, func() bool {
		return ratio_setting.ContainsGroupRatio("new") && !ratio_setting.ContainsGroupRatio("old") &&
			groupConfigAppliedVersion.Load() == version
	}, 5*time.Second, 10*time.Millisecond)

	cancel()
	<-done

	// Simulate a missed Pub/Sub message. The periodic option loader must recover
	// runtime state and dependent caches from the persisted version.
	require.NoError(t, ratio_setting.UpdateGroupRatioByJSONString(`{"old":0.8}`))
	groupConfigAppliedVersion.Store(version - 1)
	loadOptionsFromDatabase()
	assert.True(t, ratio_setting.ContainsGroupRatio("new"))
	assert.False(t, ratio_setting.ContainsGroupRatio("old"))
	assert.Equal(t, version, groupConfigAppliedVersion.Load())
}
