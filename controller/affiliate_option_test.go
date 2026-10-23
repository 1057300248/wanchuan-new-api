package controller

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func affiliateOptionHTTPFixture(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "options.db")+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"), &gorm.Config{})
	require.NoError(t, err)
	pool, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Option{}, &model.User{}, &model.AuditLog{}))
	oldDB, oldLog := model.DB, model.LOG_DB
	model.DB, model.LOG_DB = db, db
	oldRedis := common.RedisEnabled
	common.RedisEnabled = false
	common.OptionMapRWMutex.Lock()
	oldMap, oldEnabled, oldRate := common.OptionMap, common.AffiliateRewardEnabled, common.AffiliateRewardRate
	common.OptionMap = map[string]string{}
	common.AffiliateRewardEnabled, common.AffiliateRewardRate = false, 0.2
	common.OptionMapRWMutex.Unlock()
	payment := operation_setting.GetPaymentSetting()
	oldPayment := *payment
	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
	t.Cleanup(func() {
		model.DB, model.LOG_DB = oldDB, oldLog
		common.RedisEnabled = oldRedis
		common.OptionMapRWMutex.Lock()
		common.OptionMap, common.AffiliateRewardEnabled, common.AffiliateRewardRate = oldMap, oldEnabled, oldRate
		common.OptionMapRWMutex.Unlock()
		*payment = oldPayment
		require.NoError(t, pool.Close())
	})
	return db
}

func affiliateOptionHTTPRequest(payload string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPut, "/api/option/", strings.NewReader(payload))
	c.Set("role", common.RoleRootUser)
	UpdateOption(c)
	return recorder
}

func TestAffiliateOptionPayloadStrictAndLegacyCompatible(t *testing.T) {
	for _, payload := range []string{
		`{"affiliate_reward":{"enabled":true,"rate":0.2}}`,
		`{"affiliate_reward":{"rate":0,"enabled":false}}`,
		`{"affiliate_reward":{"enabled":false,"rate":1}}`,
	} {
		option, pair, err := decodeOptionUpdatePayload(strings.NewReader(payload))
		require.NoError(t, err)
		require.NotNil(t, pair)
		require.Empty(t, option.Key)
	}
	for _, payload := range []string{
		`{"affiliate_reward":null}`, `{"affiliate_reward":[]}`,
		`{"affiliate_reward":{"enabled":true}}`, `{"affiliate_reward":{"rate":0.2}}`,
		`{"affiliate_reward":{"enabled":null,"rate":0.2}}`, `{"affiliate_reward":{"enabled":true,"rate":null}}`,
		`{"affiliate_reward":{"enabled":"false","rate":0.2}}`, `{"affiliate_reward":{"enabled":false,"rate":"0.2"}}`,
		`{"affiliate_reward":{"enabled":false,"rate":-0.1}}`, `{"affiliate_reward":{"enabled":false,"rate":1.1}}`,
		`{"affiliate_reward":{"enabled":false,"rate":1e309}}`,
		`{"affiliate_reward":{"enabled":true,"enabled":false,"rate":0.2}}`,
		`{"affiliate_reward":{"enabled":true,"rate":0.1,"rate":0.2}}`,
		`{"affiliate_reward":{"enabled":false,"rate":0.2,"other":1}}`,
		`{"affiliate_reward":{"enabled":false,"rate":0.2},"key":"TopUpLink","value":"changed"}`,
		`{"key":"TopUpLink","affiliate_reward":{"enabled":false,"rate":0.2}}`,
		`{"affiliate_reward":{"enabled":false,"rate":0.2},"affiliate_reward":{"enabled":true,"rate":0.2}}`,
		`{"affiliate_reward":{"enabled":false,"rate":0.2}} {}`,
	} {
		t.Run(payload, func(t *testing.T) {
			_, _, err := decodeOptionUpdatePayload(strings.NewReader(payload))
			require.Error(t, err)
		})
	}
	// The legacy route retains its prior single-decode, permissive-unknown shape.
	option, pair, err := decodeOptionUpdatePayload(strings.NewReader(`{"key":"TopUpLink","value":"legacy","ignored":true} {}`))
	require.NoError(t, err)
	require.Nil(t, pair)
	require.Equal(t, "TopUpLink", option.Key)
	require.Equal(t, "legacy", option.Value)
}

func TestAffiliateOptionHTTPRepairsPairAndPreservesResponse(t *testing.T) {
	db := affiliateOptionHTTPFixture(t)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardEnabled", Value: "true"}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardRate", Value: "bad-stored-rate"}).Error)
	for i := 0; i < 2; i++ {
		response := affiliateOptionHTTPRequest(`{"affiliate_reward":{"enabled":true,"rate":0.3}}`)
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `{"success":true,"message":""}`, response.Body.String())
	}
	enabled, rate := common.AffiliateRewardSnapshot()
	require.True(t, enabled)
	require.Equal(t, 0.3, rate)
	var rows []model.Option
	require.NoError(t, db.Order("key").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, "true", rows[0].Value)
	require.Equal(t, "0.3", rows[1].Value)
	var audits []model.AuditLog
	require.NoError(t, db.Find(&audits).Error)
	require.Len(t, audits, 2)
	for _, audit := range audits {
		require.Contains(t, audit.Content, "AffiliateRewardEnabled")
		require.Contains(t, audit.Content, "AffiliateRewardRate")
		require.NotContains(t, audit.Content, "bad-stored-rate")
		require.NotContains(t, audit.Content, "0.3")
		encoded, err := common.Marshal(audit.Other)
		require.NoError(t, err)
		require.Contains(t, string(encoded), "AffiliateRewardEnabled")
		require.Contains(t, string(encoded), "AffiliateRewardRate")
		require.NotContains(t, string(encoded), "bad-stored-rate")
		require.NotContains(t, string(encoded), "0.3")
	}
}

func TestAffiliateOptionHTTPDisableAndInvalidRequestAtomicity(t *testing.T) {
	db := affiliateOptionHTTPFixture(t)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardEnabled", Value: "true"}).Error)
	require.NoError(t, db.Create(&model.Option{Key: "AffiliateRewardRate", Value: "corrupt"}).Error)
	common.OptionMapRWMutex.Lock()
	common.AffiliateRewardEnabled = true
	common.OptionMapRWMutex.Unlock()
	operation_setting.GetPaymentSetting().ComplianceConfirmed = false
	// Explicit invalid rate rejects the whole request, including a false toggle.
	invalid := affiliateOptionHTTPRequest(`{"affiliate_reward":{"enabled":false,"rate":"bad"}}`)
	require.Equal(t, http.StatusBadRequest, invalid.Code)
	enabled, rate := common.AffiliateRewardSnapshot()
	require.True(t, enabled)
	require.Equal(t, 0.2, rate)
	var rows []model.Option
	require.NoError(t, db.Find(&rows).Error)
	values := map[string]string{}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	require.Equal(t, "corrupt", values["AffiliateRewardRate"])
	// Existing single-key emergency disable still works with an invalid counterpart.
	response := affiliateOptionHTTPRequest(`{"key":"AffiliateRewardEnabled","value":false}`)
	require.JSONEq(t, `{"success":true,"message":""}`, response.Body.String())
	enabled, rate = common.AffiliateRewardSnapshot()
	require.False(t, enabled)
	require.Equal(t, 0.2, rate)
	// A valid full false pair can explicitly repair the stored rate without compliance.
	response = affiliateOptionHTTPRequest(`{"affiliate_reward":{"enabled":false,"rate":0.4}}`)
	require.JSONEq(t, `{"success":true,"message":""}`, response.Body.String())
	response = affiliateOptionHTTPRequest(`{"affiliate_reward":{"enabled":true,"rate":0.1}}`)
	require.Contains(t, response.Body.String(), `"success":false`)
	enabled, rate = common.AffiliateRewardSnapshot()
	require.False(t, enabled)
	require.Equal(t, 0.4, rate)
}

func TestAffiliateOptionHTTPConcurrentPairsPublishCoherently(t *testing.T) {
	db := affiliateOptionHTTPFixture(t)
	// Concurrent requests may win in either order, but readers must never see a torn pair.
	const workers = 12
	start := make(chan struct{})
	results := make(chan *httptest.ResponseRecorder, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			enabled, rate := true, 0.1
			if i%2 == 1 {
				enabled, rate = false, 0.2
			}
			results <- affiliateOptionHTTPRequest(fmt.Sprintf(`{"affiliate_reward":{"enabled":%t,"rate":%g}}`, enabled, rate))
		}(i)
	}
	observerDone := make(chan struct{})
	observerJoined := make(chan bool, 1)
	go func() {
		valid := true
		for {
			select {
			case <-observerDone:
				observerJoined <- valid
				return
			default:
				enabled, rate := common.AffiliateRewardSnapshot()
				if !((enabled && rate == 0.1) || (!enabled && rate == 0.2)) {
					valid = false
				}
			}
		}
	}()
	close(start)
	wg.Wait()
	close(observerDone)
	coherent := <-observerJoined
	close(results)
	require.True(t, coherent)
	for response := range results {
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `{"success":true,"message":""}`, response.Body.String())
	}
	var rows []model.Option
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 2)
	values := map[string]string{}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	enabled, rate := common.AffiliateRewardSnapshot()
	require.Equal(t, fmt.Sprint(enabled), values["AffiliateRewardEnabled"])
	require.Equal(t, fmt.Sprint(rate), values["AffiliateRewardRate"])
}
