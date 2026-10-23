package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/QuantumNous/new-api/common"
)

// This is a purpose-limited alternative envelope, never a generic bulk option API.
type affiliateRewardOptionRequest struct {
	Enabled bool
	Rate    float64
}

var errAffiliateOptionPayload = errors.New("invalid affiliate reward option payload")

func decodeOptionUpdatePayload(reader io.Reader) (OptionUpdateRequest, *affiliateRewardOptionRequest, error) {
	decoder := json.NewDecoder(reader)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return OptionUpdateRequest{}, nil, err
	}
	var envelope map[string]json.RawMessage
	if err := common.Unmarshal(raw, &envelope); err != nil {
		return OptionUpdateRequest{}, nil, err
	}
	if _, pairRequest := envelope["affiliate_reward"]; !pairRequest {
		// Keep the existing legacy shape and permissive unknown/trailing behavior.
		var option OptionUpdateRequest
		err := common.Unmarshal(raw, &option)
		return option, nil, err
	}
	pair, err := decodeAffiliateRewardPair(raw)
	if err != nil {
		return OptionUpdateRequest{}, nil, err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		return OptionUpdateRequest{}, nil, errAffiliateOptionPayload
	}
	return OptionUpdateRequest{}, pair, nil
}

// Token parsing enforces exact membership as well as duplicate rejection; a
// normal struct decoder's DisallowUnknownFields does not reject duplicate keys.
func decodeAffiliateRewardPair(raw []byte) (*affiliateRewardOptionRequest, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errAffiliateOptionPayload
	}
	if !decoder.More() {
		return nil, errAffiliateOptionPayload
	}
	if key, err := decoder.Token(); err != nil || key != "affiliate_reward" {
		return nil, errAffiliateOptionPayload
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return nil, errAffiliateOptionPayload
	}
	var enabled *bool
	var rate *float64
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, errAffiliateOptionPayload
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return nil, errAffiliateOptionPayload
		}
		seen[key] = true
		switch key {
		case "enabled":
			if err := decoder.Decode(&enabled); err != nil || enabled == nil {
				return nil, errAffiliateOptionPayload
			}
		case "rate":
			if err := decoder.Decode(&rate); err != nil || rate == nil {
				return nil, errAffiliateOptionPayload
			}
		default:
			return nil, errAffiliateOptionPayload
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errAffiliateOptionPayload
	}
	if enabled == nil || rate == nil || math.IsNaN(*rate) || math.IsInf(*rate, 0) || *rate < 0 || *rate > 1 {
		return nil, errAffiliateOptionPayload
	}
	if decoder.More() {
		return nil, errAffiliateOptionPayload
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errAffiliateOptionPayload
	}
	return &affiliateRewardOptionRequest{Enabled: *enabled, Rate: *rate}, nil
}
