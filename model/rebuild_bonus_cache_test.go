package model

import (
	"sync"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/relaykit/dto"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func prepareRebuildBonusCache(t *testing.T) Model {
	t.Helper()
	resetPricingEndpointTestTables(t)
	require.NoError(t, DB.AutoMigrate(&Option{}))
	InvalidateModelBonusEligibleCache()
	t.Cleanup(InvalidateModelBonusEligibleCache)
	row := Model{ModelName: "rebuild-bonus-cache", Status: 1, SyncOfficial: 1, BonusEligible: boolPtr(true)}
	require.NoError(t, row.Insert())
	insertPricingEndpointChannel(t, 19001, constant.ChannelTypeOpenAI, dto.ChannelOtherSettings{})
	insertPricingEndpointAbility(t, 19001, row.ModelName)
	InitChannelCache()
	InvalidatePricingCache()
	return row
}

func TestBonusEligibilityExpiresPreviouslyTrueCache(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	modelBonusEligibleLock.RLock()
	cached := modelBonusEligibleMap[row.ModelName]
	modelBonusEligibleLock.RUnlock()
	require.True(t, cached, "exercise the positive cache, not exact-row fallback")
	// An independent instance commits the revocation. Local cache clocks are
	// advanced past TTL without sleeping; this remains a real SQLite write/read.
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", row.Id).Update("bonus_eligible", false).Error)
	expired := time.Now().Add(-2 * modelBonusEligibleCacheTTL)
	modelBonusEligibleLock.Lock()
	modelBonusEligibleTime = expired
	modelBonusEligibleLock.Unlock()
	updatePricingLock.Lock()
	lastGetPricingTime = expired
	updatePricingLock.Unlock()
	require.False(t, IsModelBonusEligible(row.ModelName), "a previous true must not bypass TTL after a committed revocation")
}

func TestBonusEligibilityDoesNotRepublishInvalidatedSnapshot(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	captured := make(chan struct{})
	resume := make(chan struct{})
	var pauseOnce, resumeOnce sync.Once
	release := func() { resumeOnce.Do(func() { close(resume) }) }
	const callback = "rebuild_bonus_cache_capture"
	require.NoError(t, DB.Callback().Query().After("gorm:query").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "models" {
			pauseOnce.Do(func() { close(captured); <-resume })
		}
	}))
	result := make(chan bool, 1)
	joined := false
	t.Cleanup(func() {
		release()
		if !joined {
			select {
			case <-result:
			case <-time.After(3 * time.Second):
				t.Error("owned cache loader did not join during cleanup")
			}
		}
		_ = DB.Callback().Query().Remove(callback)
	})
	go func() { result <- IsModelBonusEligible(row.ModelName) }()
	select {
	case <-captured:
	case <-time.After(3 * time.Second):
		release()
		t.Fatal("pricing loader did not reach actual model query")
	}
	require.NoError(t, DB.Model(&Model{}).Where("id = ?", row.Id).Update("bonus_eligible", false).Error)
	InvalidateModelBonusEligibleCache()
	release()
	select {
	case eligible := <-result:
		joined = true
		require.False(t, eligible, "an older in-flight snapshot must not republish true after invalidation")
	case <-time.After(3 * time.Second):
		t.Fatal("pricing loader did not join")
	}
	RefreshPricing()
	require.False(t, IsModelBonusEligible(row.ModelName))
}

func TestBonusEligibilityCommittedMetadataRefreshControls(t *testing.T) {
	for _, operation := range []string{"update", "sync", "delete"} {
		t.Run(operation, func(t *testing.T) {
			row := prepareRebuildBonusCache(t)
			require.True(t, IsModelBonusEligible(row.ModelName))
			switch operation {
			case "update":
				row.BonusEligible = boolPtr(false)
				require.NoError(t, row.Update())
				RefreshPricing() // Existing public admin path.
			case "sync":
				_, err := ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: MetadataSyncSelection{
					ModelName: row.ModelName, RecordVersion: MetadataRecordVersion(&row, nil, nil), Fields: []string{"bonus_eligible"},
				}, Values: MetadataValues{Status: 1, BonusEligible: boolPtr(false)}}}, nil)
				require.NoError(t, err)
			case "delete":
				require.NoError(t, row.Delete())
			}
			require.False(t, IsModelBonusEligible(row.ModelName))
		})
	}
}

func TestBonusEligibilityDoesNotExtendPricingSnapshotLifetime(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	GetPricing()
	originalTime := time.Now().Add(-modelBonusEligibleCacheTTL / 2)
	updatePricingLock.Lock()
	lastGetPricingTime = originalTime
	updatePricingLock.Unlock()
	require.True(t, IsModelBonusEligible(row.ModelName))
	modelBonusEligibleLock.RLock()
	cacheTime := modelBonusEligibleTime
	modelBonusEligibleLock.RUnlock()
	require.Equal(t, originalTime, cacheTime, "eligibility must inherit the actual pricing snapshot age")
}

func TestBonusEligibilityModelUpdateInvalidatesAfterCommit(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.BonusEligible = boolPtr(false)
	require.NoError(t, row.Update())
	// No controller RefreshPricing is needed for a committed model mutation.
	require.False(t, IsModelBonusEligible(row.ModelName))
}

func TestBonusEligibilityRejectedMutationKeepsEligibility(t *testing.T) {
	row := prepareRebuildBonusCache(t)
	require.True(t, IsModelBonusEligible(row.ModelName))
	row.VendorID, row.BonusEligible = 999999, boolPtr(false)
	require.Error(t, row.Update())
	var stored Model
	require.NoError(t, DB.First(&stored, row.Id).Error)
	require.Zero(t, stored.VendorID, "rejected metadata mutation must not persist")
	require.NotNil(t, stored.BonusEligible)
	require.True(t, *stored.BonusEligible)
	require.True(t, IsModelBonusEligible(row.ModelName), "cold reload retains committed eligibility")
}
