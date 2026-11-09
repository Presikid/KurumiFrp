// SPDX-License-Identifier: Apache-2.0

package traffic

import (
	"errors"
	"fmt"
	"sync"
)

// Bindings maps unprefixed frp names, separately for proxies and visitors, to
// the manager's stable rule IDs in one instance generation. Do not copy it.
type Bindings struct {
	run      *Run
	mu       sync.RWMutex
	proxies  map[string]*Rule
	visitors map[string]*Rule
}

var errUninitializedBindings = errors.New("traffic: bindings must be created with NewBindings")

// NewBindings creates an initially complete name-to-ID mapping for one run.
// Different names and kinds must not share an ID: that would merge rules.
func NewBindings(run *Run, proxies, visitors map[string]string) (*Bindings, error) {
	if run == nil {
		return nil, errors.New("traffic: nil run")
	}
	b := &Bindings{run: run}
	if err := b.Replace(proxies, visitors); err != nil {
		return nil, err
	}
	return b, nil
}

// Replace atomically publishes mappings for the next service configuration.
// Existing forwarding paths retain their original rule handles. The manager
// must publish mappings before applying the corresponding frp configuration.
func (b *Bindings) Replace(proxies, visitors map[string]string) error {
	if b == nil || b.run == nil {
		return errUninitializedBindings
	}
	seen := make(map[string]string, len(proxies)+len(visitors))
	validate := func(kind string, names map[string]string) error {
		for name, id := range names {
			if name == "" || id == "" {
				return fmt.Errorf("traffic: empty %s name or rule ID", kind)
			}
			if previous, ok := seen[id]; ok {
				return fmt.Errorf("traffic: rule ID %q is shared by %s and %s %q", id, previous, kind, name)
			}
			seen[id] = fmt.Sprintf("%s %q", kind, name)
		}
		return nil
	}
	if err := validate("proxy", proxies); err != nil {
		return err
	}
	if err := validate("visitor", visitors); err != nil {
		return err
	}
	newProxies := make(map[string]*Rule, len(proxies))
	newVisitors := make(map[string]*Rule, len(visitors))
	for name, id := range proxies {
		rule, err := b.run.Rule(id)
		if err != nil {
			return err
		}
		newProxies[name] = rule
	}
	for name, id := range visitors {
		rule, err := b.run.Rule(id)
		if err != nil {
			return err
		}
		newVisitors[name] = rule
	}
	b.mu.Lock()
	b.proxies, b.visitors = newProxies, newVisitors
	b.mu.Unlock()
	return nil
}

// Claim associates a run with one service for its entire lifetime. A fresh
// run is required for a replacement service, even when construction succeeds
// but the service is never started.
func (b *Bindings) Claim() error {
	if b == nil || b.run == nil {
		return errUninitializedBindings
	}
	b.run.mu.Lock()
	defer b.run.mu.Unlock()
	if !b.run.active {
		return errStopped
	}
	if b.run.claimed {
		return errors.New("traffic: run already belongs to a service")
	}
	b.run.claimed = true
	return nil
}

// BindingSnapshot is an immutable set of handles for one applied frp config.
// It remains valid after Replace and prevents name reuse from moving old flows.
type BindingSnapshot struct {
	proxies  map[string]*Rule
	visitors map[string]*Rule
}

// Snapshot rejects an incomplete mapping instead of presenting missing
// instrumentation as zero traffic. Extra mappings are allowed.
func (b *Bindings) Snapshot(proxyNames, visitorNames []string) (*BindingSnapshot, error) {
	if b == nil || b.run == nil {
		return nil, errUninitializedBindings
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	s := &BindingSnapshot{
		proxies:  make(map[string]*Rule, len(proxyNames)),
		visitors: make(map[string]*Rule, len(visitorNames)),
	}
	for _, name := range proxyNames {
		rule := b.proxies[name]
		if rule == nil {
			return nil, fmt.Errorf("traffic: no rule binding for proxy %q", name)
		}
		s.proxies[name] = rule
	}
	for _, name := range visitorNames {
		rule := b.visitors[name]
		if rule == nil {
			return nil, fmt.Errorf("traffic: no rule binding for visitor %q", name)
		}
		s.visitors[name] = rule
	}
	return s, nil
}

func (s *BindingSnapshot) Proxy(name string) *Rule {
	if s == nil {
		return nil
	}
	return s.proxies[name]
}

func (s *BindingSnapshot) Visitor(name string) *Rule {
	if s == nil {
		return nil
	}
	return s.visitors[name]
}
