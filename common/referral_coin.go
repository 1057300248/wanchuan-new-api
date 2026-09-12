package common

import (
	"errors"
	"math"
	"math/big"

	"github.com/shopspring/decimal"
)

// ReferralCoinScale keeps fractional coins exact in storage and JSON. One
// displayed Coin equals one unit of wallet credit (USD), split into microcoins.
const ReferralCoinScale = 1_000_000

// The new top-up commission program is independent from the retired paid-usage
// affiliate bonus. Operators enable it explicitly after setting the policy.
var ReferralCoinEnabled = false
var ReferralCoinRate = 0.01
var ReferralCoinFreezeDays = 7

var ErrReferralCoinAmount = errors.New("invalid referral coin amount or precision")

// ReferralCoinRedemptionStep is the smallest microcoin amount that converts
// exactly to whole quota units. Reject fractions instead of rounding each
// redemption: splitting an amount must never manufacture wallet credit.
func ReferralCoinRedemptionStep() (int, error) {
	if QuotaPerUnit <= 0 || math.IsNaN(QuotaPerUnit) || math.IsInf(QuotaPerUnit, 0) {
		return 0, ErrReferralCoinAmount
	}
	rate, ok := new(big.Rat).SetString(decimal.NewFromFloat(QuotaPerUnit).String())
	if !ok {
		return 0, ErrReferralCoinAmount
	}
	rate.Quo(rate, big.NewRat(ReferralCoinScale, 1))
	step := rate.Denom()
	if !step.IsInt64() || step.Int64() > MaxWalletQuota {
		return 0, ErrReferralCoinAmount
	}
	return int(step.Int64()), nil
}

func ReferralCoinsToQuota(coins int) (int, error) {
	step, err := ReferralCoinRedemptionStep()
	if err != nil || coins <= 0 || coins > MaxWalletQuota || coins%step != 0 {
		return 0, ErrReferralCoinAmount
	}
	value := decimal.NewFromInt(int64(coins)).Mul(decimal.NewFromFloat(QuotaPerUnit)).Div(decimal.NewFromInt(ReferralCoinScale))
	quota, err := WalletQuotaFromDecimalStrict(value)
	if err != nil || quota <= 0 {
		return 0, ErrReferralCoinAmount
	}
	return quota, nil
}
