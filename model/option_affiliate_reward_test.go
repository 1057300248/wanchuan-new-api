package model

import (
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// 推荐返利已永久下线：任何把 AffiliateRewardEnabled 置为 true 的选项更新都必须
// 在持久化与内存状态变更之前被拒绝。UpdateOption 先写数据库再调用 updateOptionMap，
// 因此共享窄守卫 rejectRetiredAffiliateReward 同时挂在 validateOptionValue
// （UpdateOption/UpdateOptionsBulk 持久化前）与 updateOptionMap 入口（两条直接
// 调用路径：手动更新与启动/同步时从数据库加载历史记录）。其余选项不在
// updateOptionMap 上做通用 revalidation，false 与无法 ParseBool 的值语义不变。
func TestAffiliateRewardEnabledOptionIsPermanentlyRejected(t *testing.T) {
	require.NoError(t, DB.AutoMigrate(&Option{}))

	originalMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.AffiliateRewardEnabled = false
		common.OptionMap = originalMap
		DB.Where(&Option{Key: "AffiliateRewardEnabled"}).Delete(&Option{})
	})

	for _, value := range []string{"true", "TRUE", "True", "1", "t", "T"} {
		t.Run("validate rejects "+value, func(t *testing.T) {
			assert.Error(t, validateOptionValue("AffiliateRewardEnabled", value))
		})

		t.Run("updateOptionMap rejects "+value, func(t *testing.T) {
			require.Error(t, updateOptionMap("AffiliateRewardEnabled", value))

			assert.False(t, common.AffiliateRewardEnabled)
			_, present := common.OptionMap["AffiliateRewardEnabled"]
			assert.False(t, present, "rejected value must not leak into the option map")
		})
	}

	t.Run("UpdateOption rejects true before persisting", func(t *testing.T) {
		err := UpdateOption("AffiliateRewardEnabled", "true")

		require.Error(t, err)
		assert.False(t, common.AffiliateRewardEnabled)

		var option Option
		result := DB.Where(&Option{Key: "AffiliateRewardEnabled"}).First(&option)
		require.ErrorIs(t, result.Error, gorm.ErrRecordNotFound, "rejected value must not be persisted")
	})

	t.Run("false is still applied", func(t *testing.T) {
		common.OptionMapRWMutex.Lock()
		delete(common.OptionMap, "AffiliateRewardEnabled")
		common.OptionMapRWMutex.Unlock()

		require.NoError(t, updateOptionMap("AffiliateRewardEnabled", "false"))

		assert.False(t, common.AffiliateRewardEnabled)
		assert.Equal(t, "false", common.OptionMap["AffiliateRewardEnabled"])
	})
}
