/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
      http://www.apache.org/licenses/LICENSE-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package collectors

import (
	"context"
	"sync"
	"time"
)

// Collector is the interface every PowerMax metrics collector must implement.
type Collector interface {
	Collect(ctx context.Context) error
	Name() string
}

// CollectorManager manages per-array collector goroutines with independent
// lifecycles — failure on one array does not affect others.
type CollectorManager struct {
	mu            sync.Mutex
	collectors    map[string][]Collector
	stopChans     map[string]chan struct{}
	interval      time.Duration
	runtimeConfig RuntimeConfig
	runtimes      map[string]*MetricsRuntime
	staleSetter   func(arrayID string, stale bool)
}

// NewCollectorManager creates a CollectorManager with the given poll interval.
func NewCollectorManager(interval time.Duration) *CollectorManager {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &CollectorManager{
		collectors: make(map[string][]Collector),
		stopChans:  make(map[string]chan struct{}),
		interval:   interval,
		runtimes:   make(map[string]*MetricsRuntime),
		staleSetter: func(_ string, _ bool) {
			// Default no-op stale setter
		},
	}
}

// SetRuntimeConfig sets the MetricsRuntime configuration for all arrays.
func (m *CollectorManager) SetRuntimeConfig(cfg RuntimeConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runtimeConfig = cfg
	// Update interval from config if set
	if cfg.Interval > 0 {
		m.interval = cfg.Interval
	}
	// Configure stale reporter
	m.runtimeConfig.StaleReporter = m.staleSetter
}

// SetStaleSetter sets the function to call when metrics become stale.
func (m *CollectorManager) SetStaleSetter(fn func(arrayID string, stale bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.staleSetter = fn
	m.runtimeConfig.StaleReporter = fn
}

// GetRuntime returns the MetricsRuntime for a given arrayID, creating it if needed.
func (m *CollectorManager) GetRuntime(arrayID string) *MetricsRuntime {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rt, exists := m.runtimes[arrayID]; exists {
		return rt
	}
	rt := NewMetricsRuntime(arrayID, m.runtimeConfig)
	m.runtimes[arrayID] = rt
	return rt
}

// Register adds one or more collectors for a given arrayID.
func (m *CollectorManager) Register(arrayID string, cols ...Collector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.collectors[arrayID] = append(m.collectors[arrayID], cols...)
}

// Start begins background polling goroutines for every registered arrayID.
// Each array gets its own independent goroutine — per-array fault isolation.
func (m *CollectorManager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for arrayID, cols := range m.collectors {
		if _, running := m.stopChans[arrayID]; running {
			continue
		}
		stop := make(chan struct{})
		m.stopChans[arrayID] = stop
		go m.runArray(ctx, cols, stop)
	}
}

// Stop terminates the background goroutine for a specific array.
func (m *CollectorManager) Stop(arrayID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if ch, ok := m.stopChans[arrayID]; ok {
		close(ch)
		delete(m.stopChans, arrayID)
	}
}

// StopAll terminates all background goroutines.
func (m *CollectorManager) StopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for arrayID, ch := range m.stopChans {
		close(ch)
		delete(m.stopChans, arrayID)
	}
}

func (m *CollectorManager) runArray(ctx context.Context, cols []Collector, stop <-chan struct{}) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	// collect once immediately on startup
	m.collectOnce(ctx, cols)
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.collectOnce(ctx, cols)
		}
	}
}

func (m *CollectorManager) collectOnce(ctx context.Context, cols []Collector) {
	for _, c := range cols {
		if err := c.Collect(ctx); err != nil {
			// Log but do not stop — stale metric handling is inside each collector
			_ = err
		}
	}
}
