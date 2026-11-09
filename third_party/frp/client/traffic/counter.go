// SPDX-License-Identifier: Apache-2.0

// Package traffic measures client business payloads independently of frp's
// transport, control messages, and configured bandwidth limits.
package traffic

import (
	"errors"
	"math"
	"sync"
)

// Counter receives payload bytes at a single rule's forwarding boundary.
// Upload is from the client towards the remote peer, and download is from
// the remote peer towards the client. Implementations must be concurrency-safe.
type Counter interface {
	AddUpload(uint64)
	AddDownload(uint64)
}

// Bytes is a count of uncompressed business payload bytes, not wire bytes.
type Bytes struct {
	Upload   uint64 `json:"upload"`
	Download uint64 `json:"download"`
}

// Snapshot is a consistent view of one instance run. Rule keys are the
// manager's stable rule IDs, rather than frp names, which can be changed or
// shared by proxy and visitor rules.
type Snapshot struct {
	InstanceID string           `json:"instanceId"`
	Generation uint64           `json:"generation"`
	Active     bool             `json:"active"`
	Total      Bytes            `json:"total"`
	Rules      map[string]Bytes `json:"rules"`
}

// Book tracks the most recent run of each instance. Its zero value is ready
// for use. Generations are unique for the lifetime of a Book, including across
// instances; they are not persistent across process restarts.
type Book struct {
	mu   sync.RWMutex
	next uint64
	byID map[string]*Run
}

var (
	errEmptyInstance = errors.New("traffic: empty instance ID")
	errEmptyRule     = errors.New("traffic: empty rule ID")
	errStopped       = errors.New("traffic: instance run stopped")
	errGenerations   = errors.New("traffic: generation counter exhausted")
)

// Begin starts a new instance generation. Reconnects within the same client
// run should keep the same Run. A new process start or replacement should call
// Begin again. An older run keeps its own counters until its forwarding paths
// have stopped and its owner calls Close; it never writes into the new run.
// Keep the old Run if its final snapshot is needed after a replacement.
func (b *Book) Begin(instanceID string) (*Run, error) {
	if instanceID == "" {
		return nil, errEmptyInstance
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.next == math.MaxUint64 {
		return nil, errGenerations
	}
	if b.byID == nil {
		b.byID = make(map[string]*Run)
	}
	b.next++
	run := &Run{
		instanceID: instanceID,
		generation: b.next,
		active:     true,
		rules:      make(map[string]*Rule),
	}
	b.byID[instanceID] = run
	return run, nil
}

// Snapshot returns the most recent run, including a stopped run, if present.
func (b *Book) Snapshot(instanceID string) (Snapshot, bool) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	run := b.byID[instanceID]
	if run == nil {
		return Snapshot{}, false
	}
	return run.Snapshot(), true
}

// Forget removes a stopped run only if its generation still matches. It will
// not remove a replacement run when an old shutdown completes late.
func (b *Book) Forget(instanceID string, generation uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	run := b.byID[instanceID]
	if run == nil || run.generation != generation {
		return false
	}
	run.mu.RLock()
	defer run.mu.RUnlock()
	if run.active {
		return false
	}
	delete(b.byID, instanceID)
	return true
}

// Run belongs to exactly one instance generation. Do not copy it.
type Run struct {
	instanceID string
	generation uint64

	mu      sync.RWMutex
	active  bool
	claimed bool
	total   Bytes
	rules   map[string]*Rule
}

// Rule returns a handle for the manager's stable rule ID. The caller should
// resolve the frp proxy or visitor name to that ID when the rule is created,
// and retain this handle for the lifetime of the forwarding path. Calls for
// the same ID in one run share a counter.
func (r *Run) Rule(ruleID string) (*Rule, error) {
	if ruleID == "" {
		return nil, errEmptyRule
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return nil, errStopped
	}
	if rule := r.rules[ruleID]; rule != nil {
		return rule, nil
	}
	rule := &Rule{run: r}
	r.rules[ruleID] = rule
	return rule, nil
}

// Close freezes the run's counts. Call it after this run's forwarding paths
// have stopped. Existing handles then discard late samples and cannot add them
// to any subsequent run. Close is idempotent.
func (r *Run) Close() {
	r.mu.Lock()
	r.active = false
	r.mu.Unlock()
}

// Snapshot returns a consistent copy of this run and its per-rule counts.
func (r *Run) Snapshot() Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := Snapshot{
		InstanceID: r.instanceID,
		Generation: r.generation,
		Active:     r.active,
		Total:      r.total,
		Rules:      make(map[string]Bytes, len(r.rules)),
	}
	for id, rule := range r.rules {
		s.Rules[id] = rule.counts
	}
	return s
}

// Rule is a concurrency-safe counter bound to an instance generation.
type Rule struct {
	run    *Run
	counts Bytes // guarded by run.mu
}

// AddUpload records payload sent towards the remote peer.
func (r *Rule) AddUpload(n uint64) {
	if n == 0 {
		return
	}
	r.run.mu.Lock()
	defer r.run.mu.Unlock()
	if r.run.active {
		r.counts.Upload = addSaturating(r.counts.Upload, n)
		r.run.total.Upload = addSaturating(r.run.total.Upload, n)
	}
}

// AddDownload records payload received from the remote peer.
func (r *Rule) AddDownload(n uint64) {
	if n == 0 {
		return
	}
	r.run.mu.Lock()
	defer r.run.mu.Unlock()
	if r.run.active {
		r.counts.Download = addSaturating(r.counts.Download, n)
		r.run.total.Download = addSaturating(r.run.total.Download, n)
	}
}

func addSaturating(a, b uint64) uint64 {
	if math.MaxUint64-a < b {
		return math.MaxUint64
	}
	return a + b
}
