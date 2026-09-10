package affiliateport

import (
	"errors"
	"sync"
)

// RewardService owns ChuanAPI-specific referral reward policy. Core billing and
// controller packages call this narrow port instead of importing the module.
type RewardService interface {
	Record(sourceUserID, paidQuota int, referenceType, referenceID string) (bool, error)
	Mature(userID int) (int, error)
}

var (
	mu               sync.RWMutex
	defaultService   RewardService
	ErrNotConfigured = errors.New("affiliateport: reward service is not configured")
)

// SetDefault is composition-time only and write-once.
func SetDefault(service RewardService) error {
	if service == nil {
		return errors.New("affiliateport: reward service must not be nil")
	}
	mu.Lock()
	defer mu.Unlock()
	if defaultService != nil {
		return errors.New("affiliateport: reward service already configured")
	}
	defaultService = service
	return nil
}

func Default() (RewardService, error) {
	mu.RLock()
	defer mu.RUnlock()
	if defaultService == nil {
		return nil, ErrNotConfigured
	}
	return defaultService, nil
}
