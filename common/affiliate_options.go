package common

// AffiliateRewardSnapshot reads one coherent runtime pair. Callers release this
// lock before SQL; publication is serialized separately by the model option path.
func AffiliateRewardSnapshot() (enabled bool, rate float64) {
	OptionMapRWMutex.RLock()
	defer OptionMapRWMutex.RUnlock()
	return AffiliateRewardEnabled, AffiliateRewardRate
}
