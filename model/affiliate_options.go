package model

import (
	"errors"
	"math"
	"strconv"
	"sync"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"gorm.io/gorm"
)

const affiliateEnabledOption = "AffiliateRewardEnabled"
const affiliateRateOption = "AffiliateRewardRate"

// Lock order: requestPolicyOptionMutex -> passkeyOptionMutex (if needed) ->
// affiliateOptionMutex -> SQL. OptionMapRWMutex is held only for short snapshots
// or publication, never while SQL runs. This orders this process, not all nodes.
var affiliateOptionMutex sync.Mutex

var errAffiliateOptionInvalid = errors.New("invalid affiliate reward option pair")

type affiliateOptionPair struct {
	enabled bool
	rate    float64
}

func isAffiliateRewardOption(key string) bool {
	return key == affiliateEnabledOption || key == affiliateRateOption
}

func hasAffiliateRewardOptions(values map[string]string) bool {
	for key := range values {
		if isAffiliateRewardOption(key) {
			return true
		}
	}
	return false
}

func validateAffiliateRewardOption(key, value string) error {
	switch key {
	case affiliateEnabledOption:
		if value != "true" && value != "false" {
			return errAffiliateOptionInvalid
		}
	case affiliateRateOption:
		rate, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || rate > 1 {
			return errAffiliateOptionInvalid
		}
	}
	return nil
}

// readAffiliateOptions reads fresh persisted state, not the last runtime default.
// A missing key uses the historical source default; an invalid row is not missing.
func readAffiliateOptions(tx *gorm.DB) (map[string]string, error) {
	values := map[string]string{affiliateEnabledOption: "false", affiliateRateOption: "0.05"}
	var rows []Option
	if err := tx.Where(map[string]any{"key": []string{affiliateEnabledOption, affiliateRateOption}}).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		values[row.Key] = row.Value
	}
	return values, nil
}

func parseAffiliateOptionPair(values map[string]string) (affiliateOptionPair, error) {
	if err := validateAffiliateRewardOption(affiliateEnabledOption, values[affiliateEnabledOption]); err != nil {
		return affiliateOptionPair{}, err
	}
	if err := validateAffiliateRewardOption(affiliateRateOption, values[affiliateRateOption]); err != nil {
		return affiliateOptionPair{}, err
	}
	rate, _ := strconv.ParseFloat(values[affiliateRateOption], 64)
	return affiliateOptionPair{enabled: values[affiliateEnabledOption] == "true", rate: rate}, nil
}

// prepareAffiliateOptionsTx runs before any option is persisted. Explicit bad
// values reject the whole request. A valid false is an emergency stop even if a
// pre-existing counterpart row is invalid; it never repairs that row implicitly.
func prepareAffiliateOptionsTx(tx *gorm.DB, input map[string]string) (affiliateOptionPair, error) {
	for key, value := range input {
		if err := validateAffiliateRewardOption(key, value); err != nil {
			return affiliateOptionPair{}, err
		}
	}
	values, err := readAffiliateOptions(tx)
	if err != nil {
		return affiliateOptionPair{}, err
	}
	for key, value := range input {
		if isAffiliateRewardOption(key) {
			values[key] = value
		}
	}
	pair, err := parseAffiliateOptionPair(values)
	if err != nil {
		if input[affiliateEnabledOption] != "false" {
			return affiliateOptionPair{}, err
		}
		_, rate := common.AffiliateRewardSnapshot()
		pair = affiliateOptionPair{enabled: false, rate: rate}
	}
	// Disabling, including a valid combined false + positive-rate form, must
	// remain available when compliance is unconfirmed. No grant is enabled.
	positiveRateChange := false
	if value, changed := input[affiliateRateOption]; changed {
		rate, _ := strconv.ParseFloat(value, 64) // explicit values were validated above
		positiveRateChange = rate > 0 && input[affiliateEnabledOption] != "false"
	}
	if (pair.enabled || positiveRateChange) && !operation_setting.IsPaymentComplianceConfirmed() {
		return affiliateOptionPair{}, common.NewMessage("Payment, redemption, subscription, and invitation reward features are disabled. The administrator must confirm compliance terms before enabling them.")
	}
	return pair, nil
}

func publishAffiliateOptionPair(pair affiliateOptionPair) {
	common.OptionMapRWMutex.Lock()
	defer common.OptionMapRWMutex.Unlock()
	common.AffiliateRewardEnabled, common.AffiliateRewardRate = pair.enabled, pair.rate
	common.OptionMap[affiliateEnabledOption] = strconv.FormatBool(pair.enabled)
	common.OptionMap[affiliateRateOption] = strconv.FormatFloat(pair.rate, 'f', -1, 64)
}

// reloadAffiliateOptions is called after the existing loader locks. The fresh
// read is inside the pair lock, so an older load cannot overwrite a later disable.
func reloadAffiliateOptions() error {
	affiliateOptionMutex.Lock()
	defer affiliateOptionMutex.Unlock()
	values, err := readAffiliateOptions(DB)
	if err != nil {
		return err
	}
	pair, err := parseAffiliateOptionPair(values)
	if err == nil {
		publishAffiliateOptionPair(pair)
		return nil
	}
	if values[affiliateEnabledOption] == "false" {
		_, rate := common.AffiliateRewardSnapshot()
		publishAffiliateOptionPair(affiliateOptionPair{enabled: false, rate: rate})
	}
	// Other malformed persisted pairs retain the last valid runtime pair;
	// InitOptionMap starts disabled. No raw option values or credentials log.
	return errAffiliateOptionInvalid
}
