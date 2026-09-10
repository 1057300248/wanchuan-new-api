package paymentport

import (
	"errors"
	"sync"
)

// Contribution is the provider-owned portion of the generic top-up catalogue.
// Methods are merged by their "type" key; Fields carries backwards-compatible
// provider metadata without teaching the host controller concrete provider names.
type Contribution struct {
	Methods []map[string]string
	Fields  map[string]any
}

func (c Contribution) Clone() Contribution {
	out := Contribution{Fields: make(map[string]any, len(c.Fields))}
	for _, method := range c.Methods {
		copyMethod := make(map[string]string, len(method))
		for key, value := range method {
			copyMethod[key] = value
		}
		out.Methods = append(out.Methods, copyMethod)
	}
	for key, value := range c.Fields {
		out.Fields[key] = value
	}
	return out
}

type Catalogue interface {
	Contribution() Contribution
}

var (
	mu               sync.RWMutex
	defaultCatalogue Catalogue
)

// SetDefault installs one composition-time provider catalogue. It is write-once
// so runtime handlers always observe one stable implementation identity.
func SetDefault(catalogue Catalogue) error {
	if catalogue == nil {
		return errors.New("paymentport: catalogue must not be nil")
	}
	mu.Lock()
	defer mu.Unlock()
	if defaultCatalogue != nil {
		return errors.New("paymentport: catalogue already configured")
	}
	defaultCatalogue = catalogue
	return nil
}

// Snapshot returns an empty contribution when no optional provider module is
// composed, allowing an upstream-only host to use the same controller code.
func Snapshot() Contribution {
	mu.RLock()
	catalogue := defaultCatalogue
	mu.RUnlock()
	if catalogue == nil {
		return Contribution{Fields: map[string]any{}}
	}
	return catalogue.Contribution().Clone()
}
