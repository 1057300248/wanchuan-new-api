package model

import (
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// Every public eligibility writer must evict both layers after its transaction,
// including an unknown COMMIT outcome. A cold grant cache alone can otherwise
// republish a still-fresh positive entry from the pricing snapshot.
func TestBonusEligibilityMutationCommitBoundaries(t *testing.T) {
	for _, scenario := range []string{"batch_commit", "batch_lost_ack", "delete_lost_ack", "delete_pricing_lost_ack", "sync_bonus_lost_ack", "sync_name_rule_lost_ack"} {
		t.Run(scenario, func(t *testing.T) {
			row := prepareRebuildBonusCache(t)
			probe := row.ModelName
			if scenario == "sync_name_rule_lost_ack" {
				row.NameRule = NameRulePrefix
				require.NoError(t, row.Update())
				probe += "-child"
				insertPricingEndpointAbility(t, 19001, probe)
				InitChannelCache()
			}
			if scenario == "batch_commit" || scenario == "batch_lost_ack" {
				require.NoError(t, DB.AutoMigrate(&ModelBonusEligibleAudit{}))
				t.Cleanup(func() {
					require.NoError(t, DB.Where("model_id = ?", row.Id).Delete(&ModelBonusEligibleAudit{}).Error)
				})
			}
			if scenario == "delete_pricing_lost_ack" {
				// The delete+pricing branch persists option maps in the same real
				// transaction. Restore only its exact keys; never leak test options.
				var previous []Option
				require.NoError(t, DB.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Find(&previous).Error)
				t.Cleanup(func() {
					require.NoError(t, DB.Transaction(func(tx *gorm.DB) error {
						if err := tx.Where(commonKeyCol+" IN ?", modelPricingOptionKeys).Delete(&Option{}).Error; err != nil {
							return err
						}
						if len(previous) > 0 {
							return tx.Create(&previous).Error
						}
						return nil
					}))
				})
			}
			var original Model
			require.NoError(t, DB.First(&original, row.Id).Error)
			require.True(t, IsModelBonusEligible(probe))
			modelBonusEligibleLock.RLock()
			warmGrant := modelBonusEligibleMap[probe] && modelBonusEligibleCacheFresh()
			modelBonusEligibleLock.RUnlock()
			require.True(t, warmGrant, "the original positive cache, not exact-row fallback, must be exercised")

			apply := func() error {
				switch scenario {
				case "batch_commit", "batch_lost_ack":
					changed, err := BatchUpdateModelBonusEligible([]int{row.Id}, boolPtr(false), 42, "fixture-admin", t.Name())
					if err == nil {
						require.Equal(t, 1, changed)
					}
					return err
				case "delete_lost_ack", "delete_pricing_lost_ack":
					_, err := DeleteModelMetadata([]int{row.Id}, false, scenario == "delete_pricing_lost_ack")
					return err
				default:
					field, values := "bonus_eligible", MetadataValues{BonusEligible: boolPtr(false)}
					if scenario == "sync_name_rule_lost_ack" {
						field, values = "name_rule", MetadataValues{NameRule: NameRuleExact}
					}
					_, err := ApplyMetadataSync([]MetadataSyncUpdate{{MetadataSyncSelection: MetadataSyncSelection{
						ModelName: row.ModelName, RecordVersion: MetadataRecordVersion(&original, nil, nil), Fields: []string{field},
					}, Values: values}}, nil)
					return err
				}
			}
			if scenario == "batch_commit" {
				require.NoError(t, apply())
			} else {
				subscriptionLoseCommitAck(t, DB, apply) // proves one real COMMIT before the error
			}

			var stored Model
			if scenario == "delete_lost_ack" || scenario == "delete_pricing_lost_ack" {
				require.ErrorIs(t, DB.First(&stored, row.Id).Error, gorm.ErrRecordNotFound)
			} else {
				require.NoError(t, DB.First(&stored, row.Id).Error)
				if scenario == "sync_name_rule_lost_ack" {
					require.Equal(t, NameRuleExact, stored.NameRule)
					require.NotNil(t, stored.BonusEligible)
					require.True(t, *stored.BonusEligible, "only matching policy changed, not the explicit grant")
				} else {
					require.NotNil(t, stored.BonusEligible)
					require.False(t, *stored.BonusEligible)
				}
			}
			if scenario == "batch_commit" || scenario == "batch_lost_ack" {
				var audits int64
				require.NoError(t, DB.Model(&ModelBonusEligibleAudit{}).Where("model_id = ?", row.Id).Count(&audits).Error)
				require.EqualValues(t, 1, audits, "the original audit transaction remains committed exactly once")
			}
			modelBonusEligibleLock.RLock()
			grantCold := modelBonusEligibleTime.IsZero()
			modelBonusEligibleLock.RUnlock()
			updatePricingLock.Lock()
			pricingCold := lastGetPricingTime.IsZero()
			updatePricingLock.Unlock()
			require.False(t, IsModelBonusEligible(probe), "a committed revocation must not wait for either cache TTL")
			require.True(t, grantCold, "the eligibility layer must have been evicted before readback")
			require.True(t, pricingCold, "the pricing layer must have been evicted before readback")
		})
	}
}
