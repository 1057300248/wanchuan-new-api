package moneyport

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type factoryTestFunding struct{ source string }

func (f *factoryTestFunding) Source() string     { return f.source }
func (*factoryTestFunding) PreConsume(int) error { return nil }
func (*factoryTestFunding) Settle(int) error     { return nil }
func (*factoryTestFunding) Refund() error        { return nil }

func TestFactoryRegistryLifecycleAndRequestIsolation(t *testing.T) {
	registry := NewFactoryRegistry()
	assert.False(t, registry.Frozen())
	var requests []FactoryRequest
	var created []*factoryTestFunding
	factory := FundingFactoryFunc(func(request FactoryRequest) (FundingSource, error) {
		requests = append(requests, request.Clone())
		request.AccountOrder[0] = "factory-mutated"
		funding := &factoryTestFunding{source: " WALLET "}
		created = append(created, funding)
		return funding, nil
	})
	require.NoError(t, registry.Register(" WALLET ", factory))
	assert.Error(t, registry.Register(SourceWallet, factory), "canonical duplicate must not replace a factory")
	request := FactoryRequest{
		UserID: 7, RequestID: "original-request", BillingModel: "model",
		Amount: 19, AccountOrder: []string{"bonus", "paid", "reward"},
	}
	funding, err := registry.Create(SourceWallet, request)
	assert.Error(t, err, "request handling must not observe mutable registration")
	assert.Nil(t, funding)
	assert.Empty(t, requests, "unfrozen registry must not invoke a factory")
	require.NoError(t, registry.Freeze())
	require.NoError(t, registry.Freeze(), "freezing an already frozen registry is harmless")
	assert.True(t, registry.Frozen())
	assert.ErrorIs(t, registry.Register("other", factory), ErrFactoryRegistryFrozen)

	first, err := registry.Create(" wallet ", request)
	require.NoError(t, err)
	second, err := registry.Create(SourceWallet, request)
	require.NoError(t, err)
	require.Len(t, created, 2)
	assert.Same(t, created[0], first)
	assert.Same(t, created[1], second)
	assert.NotSame(t, first, second, "registry must not cache per-request funding instances")
	assert.Equal(t, []FactoryRequest{request, request}, requests)
	assert.Equal(t, []string{"bonus", "paid", "reward"}, request.AccountOrder)
}

func TestFactoryRegistryRejectsInvalidConstruction(t *testing.T) {
	factoryFailure := errors.New("factory rejected construction")
	cases := []struct {
		name    string
		factory FundingFactory
		wantErr error
	}{
		{name: "unregistered", wantErr: ErrFactoryNotRegistered},
		{name: "nil funding", factory: FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) {
			return nil, nil
		})},
		{name: "wrong source", factory: FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) {
			return &factoryTestFunding{source: SourceSubscription}, nil
		})},
		{name: "factory error", wantErr: factoryFailure, factory: FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) {
			return &factoryTestFunding{source: SourceWallet}, factoryFailure
		})},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			registry := NewFactoryRegistry()
			if tc.factory != nil {
				require.NoError(t, registry.Register(SourceWallet, tc.factory))
			}
			require.NoError(t, registry.Freeze())
			funding, err := registry.Create(SourceWallet, FactoryRequest{})
			require.Error(t, err)
			assert.Nil(t, funding, "invalid funding must never escape to billing orchestration")
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}
		})
	}
	t.Run("registration errors leave no entry", func(t *testing.T) {
		registry := NewFactoryRegistry()
		valid := FundingFactoryFunc(func(FactoryRequest) (FundingSource, error) {
			return &factoryTestFunding{source: SourceWallet}, nil
		})
		assert.Error(t, registry.Register(" \t", valid))
		assert.Error(t, registry.Register(SourceWallet, nil))
		require.NoError(t, registry.Freeze())
		funding, err := registry.Create(SourceWallet, FactoryRequest{})
		assert.ErrorIs(t, err, ErrFactoryNotRegistered)
		assert.Nil(t, funding)
	})
	t.Run("nil registry", func(t *testing.T) {
		var registry *FactoryRegistry
		assert.False(t, registry.Frozen())
		assert.Error(t, registry.Register(SourceWallet, nil))
		assert.Error(t, registry.Freeze())
		funding, err := registry.Create(SourceWallet, FactoryRequest{})
		assert.Error(t, err)
		assert.Nil(t, funding)
	})
}

func TestFundingDTOCopiesPreserveOriginalReceiptFacts(t *testing.T) {
	t.Run("factory request", func(t *testing.T) {
		original := FactoryRequest{
			UserID: 9, RequestID: "receipt-owner", BillingModel: "model", Amount: 37,
			AccountOrder: []string{"reward", "bonus", "paid"},
		}
		copy := original.Clone()
		assert.Equal(t, original, copy)
		copy.AccountOrder[0] = "changed-copy"
		original.AccountOrder[1] = "changed-original"
		assert.Equal(t, []string{"reward", "changed-original", "paid"}, original.AccountOrder)
		assert.Equal(t, []string{"changed-copy", "bonus", "paid"}, copy.AccountOrder)
	})
	t.Run("settlement shares", func(t *testing.T) {
		original := SettlementResult{
			Replayed: true, TotalAfter: 107,
			DebitShares: []AccountShare{{Account: "paid", Amount: 11}, {Account: "bonus", Amount: 3}, {Account: "reward", Amount: 5}},
		}
		copy := original.Clone()
		assert.Equal(t, original, copy)
		copy.DebitShares[0].Amount = 999
		copy.DebitShares[0].Account = "changed-copy"
		original.DebitShares[1].Amount = 7
		assert.Equal(t, AccountShare{Account: "paid", Amount: 11}, original.DebitShares[0])
		assert.Equal(t, AccountShare{Account: "bonus", Amount: 3}, copy.DebitShares[1])
		assert.Equal(t, AccountShare{Account: "reward", Amount: 5}, copy.DebitShares[2])
	})
	t.Run("absent slices", func(t *testing.T) {
		assert.Nil(t, (FactoryRequest{}).Clone().AccountOrder)
		assert.Nil(t, (SettlementResult{}).Clone().DebitShares)
	})
}
