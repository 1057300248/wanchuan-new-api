package model

import (
	"errors"
	"maps"
	"path/filepath"
	"testing"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func affiliateOptionsFixture(t *testing.T) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "affiliate-options.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	pool.SetMaxOpenConns(4)
	require.NoError(t, db.AutoMigrate(&Option{}))
	previousDB := DB
	DB = db
	common.OptionMapRWMutex.Lock()
	previousMap := common.OptionMap
	previousEnabled, previousRate := common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.OptionMap = map[string]string{}
	common.AffiliateRewardEnabled, common.AffiliateRewardRate = false, 0.05
	common.OptionMapRWMutex.Unlock()
	payment := operation_setting.GetPaymentSetting()
	previousPayment := *payment
	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		DB = previousDB
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousMap
		common.AffiliateRewardEnabled, common.AffiliateRewardRate = previousEnabled, previousRate
		common.OptionMapRWMutex.Unlock()
		*payment = previousPayment
		require.NoError(t, pool.Close())
	})
}

func seedAffiliateOptions(t *testing.T, values map[string]string) {
	t.Helper()
	for key, value := range values {
		require.NoError(t, DB.Save(&Option{Key: key, Value: value}).Error)
	}
}

func requireAffiliatePair(t *testing.T, enabled bool, rate float64) {
	t.Helper()
	actualEnabled, actualRate := common.AffiliateRewardSnapshot()
	require.Equal(t, enabled, actualEnabled)
	require.Equal(t, rate, actualRate)
}

func TestAffiliateOptionsRejectMalformedExplicitValuesAtomically(t *testing.T) {
	for _, tc := range []struct{ key, value string }{
		{affiliateEnabledOption, "TRUE"}, {affiliateEnabledOption, "1"}, {affiliateEnabledOption, " false "},
		{affiliateRateOption, "NaN"}, {affiliateRateOption, "+Inf"}, {affiliateRateOption, "-Inf"},
		{affiliateRateOption, "-0.01"}, {affiliateRateOption, "1.01"}, {affiliateRateOption, "bad"},
	} {
		t.Run(tc.key+"/"+tc.value, func(t *testing.T) {
			affiliateOptionsFixture(t)
			seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "0.2"})
			require.NoError(t, reloadAffiliateOptions())
			require.Error(t, UpdateOption(tc.key, tc.value))
			require.Error(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "false", tc.key: tc.value, "affiliate-test-unrelated": "must-not-persist"}))
			requireAffiliatePair(t, true, 0.2)
			var count int64
			require.NoError(t, DB.Model(&Option{}).Where(map[string]any{"key": "affiliate-test-unrelated"}).Count(&count).Error)
			require.Zero(t, count)
		})
	}
}

func TestAffiliateOptionsColdInvalidPairAndExplicitRepair(t *testing.T) {
	affiliateOptionsFixture(t)
	seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "bad"})
	require.Error(t, reloadAffiliateOptions())
	requireAffiliatePair(t, false, 0.05)
	require.Error(t, UpdateOption(affiliateEnabledOption, "true"), "runtime fallback must not conceal an invalid persisted counterpart")
	require.NoError(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "0.3"}))
	requireAffiliatePair(t, true, 0.3)
	for _, rate := range []string{"0", "1"} {
		require.NoError(t, UpdateOption(affiliateRateOption, rate))
	}
	requireAffiliatePair(t, true, 1)
}

func TestAffiliateOptionsFalseStopsWithInvalidStoredRate(t *testing.T) {
	for _, mode := range []string{"load", "single", "bulk"} {
		t.Run(mode, func(t *testing.T) {
			affiliateOptionsFixture(t)
			seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "0.2"})
			require.NoError(t, reloadAffiliateOptions())
			seedAffiliateOptions(t, map[string]string{affiliateRateOption: "corrupt"})
			operation_setting.GetPaymentSetting().ComplianceConfirmed = false
			switch mode {
			case "load":
				seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "false"})
				require.Error(t, reloadAffiliateOptions())
			case "single":
				require.NoError(t, UpdateOption(affiliateEnabledOption, "false"))
			case "bulk":
				require.NoError(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "false"}))
			}
			requireAffiliatePair(t, false, 0.2)
			var stored Option
			require.NoError(t, DB.First(&stored, map[string]any{"key": affiliateRateOption}).Error)
			require.Equal(t, "corrupt", stored.Value, "disable must not silently repair the invalid row")
		})
	}
}

func TestAffiliateOptionsComplianceAndLegacyCompatibility(t *testing.T) {
	affiliateOptionsFixture(t)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	require.Error(t, UpdateOption(affiliateEnabledOption, "true"))
	require.Error(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "0"}))
	require.Error(t, UpdateOption(affiliateRateOption, "0.2"))
	require.NoError(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "false", affiliateRateOption: "0.2"}))
	requireAffiliatePair(t, false, 0.2)
	require.NoError(t, UpdateOption(affiliateRateOption, "0"))
	require.Error(t, updateOptionMap(affiliateEnabledOption, "true"), "internal single-key publication is not a bypass")
	// These persisted compatibility keys do not control the actual fixed LinuxDO policy.
	oldSignup, oldQuota := common.SignupBonusEnabled, common.QuotaForSignupBonus
	t.Cleanup(func() { common.SignupBonusEnabled, common.QuotaForSignupBonus = oldSignup, oldQuota })
	require.NoError(t, UpdateOption("SignupBonusEnabled", "true"))
	require.NoError(t, UpdateOption("QuotaForSignupBonus", "987"))
	requireAffiliatePair(t, false, 0)
	require.Equal(t, 3, common.LinuxDOSignupBonusUSD)
}

func TestAffiliateOptionsDatabaseFailureDoesNotPublish(t *testing.T) {
	affiliateOptionsFixture(t)
	seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "false", affiliateRateOption: "0.2"})
	require.NoError(t, reloadAffiliateOptions())
	const callback = "test:affiliate-option-write-failure"
	require.NoError(t, DB.Callback().Update().Before("gorm:update").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "options" {
			tx.AddError(errors.New("forced option save failure"))
		}
	}))
	t.Cleanup(func() { require.NoError(t, DB.Callback().Update().Remove(callback)) })
	require.Error(t, UpdateOptionsBulk(map[string]string{affiliateEnabledOption: "true", affiliateRateOption: "0.4"}))
	requireAffiliatePair(t, false, 0.2)
	values, err := readAffiliateOptions(DB)
	require.NoError(t, err)
	require.Equal(t, "false", values[affiliateEnabledOption])
	require.Equal(t, "0.2", values[affiliateRateOption])
}

func TestAffiliateOptionsLateEnablePublishCannotOvertakeDisable(t *testing.T) {
	affiliateOptionsFixture(t)
	seedAffiliateOptions(t, map[string]string{affiliateEnabledOption: "false", affiliateRateOption: "0.2"})
	require.NoError(t, reloadAffiliateOptions())
	// Hold only publication after the enable transaction commits. A second
	// writer must remain behind the pair mutex until that publication finishes.
	common.OptionMapRWMutex.RLock()
	locked := true
	enabledDone := make(chan error, 1)
	disabledDone := make(chan error, 1)
	enableJoined, disableStarted, disableJoined := false, false, false
	defer func() {
		if locked {
			common.OptionMapRWMutex.RUnlock()
		}
		if !enableJoined {
			select {
			case <-enabledDone:
			case <-time.After(3 * time.Second):
				t.Error("enable cleanup did not join")
			}
		}
		if disableStarted && !disableJoined {
			select {
			case <-disabledDone:
			case <-time.After(3 * time.Second):
				t.Error("disable cleanup did not join")
			}
		}
	}()
	go func() { enabledDone <- UpdateOption(affiliateEnabledOption, "true") }()
	deadline := time.Now().Add(3 * time.Second)
	for {
		values, err := readAffiliateOptions(DB)
		require.NoError(t, err)
		if values[affiliateEnabledOption] == "true" {
			break
		}
		require.True(t, time.Now().Before(deadline), "enable transaction never reached commit")
		time.Sleep(time.Millisecond)
	}
	started := make(chan struct{})
	disableStarted = true
	go func() { close(started); disabledDone <- UpdateOption(affiliateEnabledOption, "false") }()
	<-started
	common.OptionMapRWMutex.RUnlock()
	locked = false
	select {
	case err := <-enabledDone:
		enableJoined = true
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("enable did not join")
	}
	select {
	case err := <-disabledDone:
		disableJoined = true
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("disable did not join")
	}
	requireAffiliatePair(t, false, 0.2)
	require.NoError(t, reloadAffiliateOptions())
	requireAffiliatePair(t, false, 0.2)
	common.OptionMapRWMutex.RLock()
	snapshot := maps.Clone(common.OptionMap)
	common.OptionMapRWMutex.RUnlock()
	require.Equal(t, "false", snapshot[affiliateEnabledOption])
}

func TestAffiliateOptionsPasskeyDispatcherDoesNotBypassPairChecks(t *testing.T) {
	affiliateOptionsFixture(t)
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	for _, values := range []map[string]string{
		{"passkey.rp_id": "localhost", affiliateEnabledOption: "true", affiliateRateOption: "0.2"},
		{"passkey.rp_id": "localhost", affiliateEnabledOption: "false", affiliateRateOption: "NaN"},
	} {
		require.Error(t, UpdateOptionsBulk(values))
		requireAffiliatePair(t, false, 0.05)
		var count int64
		require.NoError(t, DB.Model(&Option{}).Count(&count).Error)
		require.Zero(t, count)
	}
}
