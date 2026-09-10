package paymentport

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Contribution is the provider-owned portion of the generic top-up catalogue.
// Methods are rendered in provider registration order. Fields carries
// backwards-compatible provider metadata without teaching the host controller
// concrete provider names.
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

// Descriptor declares the stable response surface owned by one provider. A
// disabled provider may omit declared output, but it may never emit undeclared
// method types or field keys.
type Descriptor struct {
	MethodTypes []string
	FieldKeys   []string
}

func (d Descriptor) Clone() Descriptor {
	return Descriptor{
		MethodTypes: append([]string(nil), d.MethodTypes...),
		FieldKeys:   append([]string(nil), d.FieldKeys...),
	}
}

type Catalogue interface {
	Descriptor() Descriptor
	Contribution() Contribution
}

var (
	ErrRegistryFrozen    = errors.New("paymentport: provider registry is frozen")
	ErrRegistryNotFrozen = errors.New("paymentport: provider registry is not frozen")
)

type providerEntry struct {
	id         string
	catalogue  Catalogue
	descriptor Descriptor
}

// Registry is an explicit composition-time provider registry. Registration
// order is stable and becomes the method rendering order after Freeze.
type Registry struct {
	mu           sync.RWMutex
	frozen       bool
	providers    []providerEntry
	providerIDs  map[string]struct{}
	methodOwners map[string]string
	fieldOwners  map[string]string
}

func NewRegistry() *Registry {
	return &Registry{
		providerIDs:  make(map[string]struct{}),
		methodOwners: make(map[string]string),
		fieldOwners:  make(map[string]string),
	}
}

func normalizeProviderID(id string) string { return strings.ToLower(strings.TrimSpace(id)) }
func normalizeMethodType(methodType string) string {
	return strings.ToLower(strings.TrimSpace(methodType))
}

func normalizeDescriptor(id string, descriptor Descriptor) (Descriptor, error) {
	out := Descriptor{}
	seenMethods := map[string]struct{}{}
	for _, raw := range descriptor.MethodTypes {
		methodType := normalizeMethodType(raw)
		if methodType == "" {
			return Descriptor{}, fmt.Errorf("paymentport: provider %q declares an empty method type", id)
		}
		if _, exists := seenMethods[methodType]; exists {
			return Descriptor{}, fmt.Errorf("paymentport: provider %q declares duplicate method type %q", id, methodType)
		}
		seenMethods[methodType] = struct{}{}
		out.MethodTypes = append(out.MethodTypes, methodType)
	}
	seenFields := map[string]struct{}{}
	for _, raw := range descriptor.FieldKeys {
		key := strings.TrimSpace(raw)
		if key == "" {
			return Descriptor{}, fmt.Errorf("paymentport: provider %q declares an empty field key", id)
		}
		if _, exists := seenFields[key]; exists {
			return Descriptor{}, fmt.Errorf("paymentport: provider %q declares duplicate field key %q", id, key)
		}
		seenFields[key] = struct{}{}
		out.FieldKeys = append(out.FieldKeys, key)
	}
	return out, nil
}

func (r *Registry) Register(id string, catalogue Catalogue) error {
	if r == nil {
		return errors.New("paymentport: nil provider registry")
	}
	id = normalizeProviderID(id)
	if id == "" {
		return errors.New("paymentport: provider id is empty")
	}
	if catalogue == nil {
		return fmt.Errorf("paymentport: provider %q catalogue is nil", id)
	}
	descriptor, err := normalizeDescriptor(id, catalogue.Descriptor())
	if err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.frozen {
		return ErrRegistryFrozen
	}
	if _, exists := r.providerIDs[id]; exists {
		return fmt.Errorf("paymentport: duplicate provider id %q", id)
	}
	for _, methodType := range descriptor.MethodTypes {
		if owner, exists := r.methodOwners[methodType]; exists {
			return fmt.Errorf("paymentport: method type %q owned by both %q and %q", methodType, owner, id)
		}
	}
	for _, key := range descriptor.FieldKeys {
		if owner, exists := r.fieldOwners[key]; exists {
			return fmt.Errorf("paymentport: field key %q owned by both %q and %q", key, owner, id)
		}
	}

	r.providerIDs[id] = struct{}{}
	for _, methodType := range descriptor.MethodTypes {
		r.methodOwners[methodType] = id
	}
	for _, key := range descriptor.FieldKeys {
		r.fieldOwners[key] = id
	}
	r.providers = append(r.providers, providerEntry{id: id, catalogue: catalogue, descriptor: descriptor.Clone()})
	return nil
}

func (r *Registry) Freeze() error {
	if r == nil {
		return errors.New("paymentport: nil provider registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frozen = true
	return nil
}

func (r *Registry) Frozen() bool {
	if r == nil {
		return false
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.frozen
}

func (r *Registry) Snapshot() (Contribution, error) {
	if r == nil {
		return Contribution{}, errors.New("paymentport: nil provider registry")
	}
	r.mu.RLock()
	if !r.frozen {
		r.mu.RUnlock()
		return Contribution{}, ErrRegistryNotFrozen
	}
	providers := append([]providerEntry(nil), r.providers...)
	r.mu.RUnlock()

	out := Contribution{Fields: map[string]any{}}
	seenMethods := map[string]string{}
	seenFields := map[string]string{}
	for _, provider := range providers {
		declaredMethods := make(map[string]struct{}, len(provider.descriptor.MethodTypes))
		for _, methodType := range provider.descriptor.MethodTypes {
			declaredMethods[methodType] = struct{}{}
		}
		declaredFields := make(map[string]struct{}, len(provider.descriptor.FieldKeys))
		for _, key := range provider.descriptor.FieldKeys {
			declaredFields[key] = struct{}{}
		}

		contribution := provider.catalogue.Contribution().Clone()
		for _, method := range contribution.Methods {
			methodType := normalizeMethodType(method["type"])
			if methodType == "" {
				return Contribution{}, fmt.Errorf("paymentport: provider %q emitted a method without type", provider.id)
			}
			if _, declared := declaredMethods[methodType]; !declared {
				return Contribution{}, fmt.Errorf("paymentport: provider %q emitted undeclared method type %q", provider.id, methodType)
			}
			if owner, exists := seenMethods[methodType]; exists {
				return Contribution{}, fmt.Errorf("paymentport: runtime method type %q emitted by both %q and %q", methodType, owner, provider.id)
			}
			method["type"] = methodType
			seenMethods[methodType] = provider.id
			out.Methods = append(out.Methods, method)
		}
		for key, value := range contribution.Fields {
			if _, declared := declaredFields[key]; !declared {
				return Contribution{}, fmt.Errorf("paymentport: provider %q emitted undeclared field key %q", provider.id, key)
			}
			if owner, exists := seenFields[key]; exists {
				return Contribution{}, fmt.Errorf("paymentport: runtime field key %q emitted by both %q and %q", key, owner, provider.id)
			}
			seenFields[key] = provider.id
			out.Fields[key] = value
		}
	}
	return out, nil
}

var (
	activeMu       sync.RWMutex
	activeRegistry *Registry
)

// ConfigureRegistry freezes and installs the process registry once. An
// upstream-only host may omit configuration; Snapshot then returns an empty
// contribution.
func ConfigureRegistry(registry *Registry) error {
	if registry == nil {
		return errors.New("paymentport: nil provider registry")
	}
	if !registry.Frozen() {
		if err := registry.Freeze(); err != nil {
			return err
		}
	}
	activeMu.Lock()
	defer activeMu.Unlock()
	if activeRegistry != nil {
		return errors.New("paymentport: provider registry already configured")
	}
	activeRegistry = registry
	return nil
}

// Snapshot returns the merged optional-provider contribution. No configured
// registry is equivalent to an upstream-only host with no optional providers.
func Snapshot() (Contribution, error) {
	activeMu.RLock()
	registry := activeRegistry
	activeMu.RUnlock()
	if registry == nil {
		return Contribution{Fields: map[string]any{}}, nil
	}
	return registry.Snapshot()
}
