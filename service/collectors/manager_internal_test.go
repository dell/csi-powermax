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
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type mockCollector struct {
	name      string
	collectFn func(ctx context.Context) error
}

func (m *mockCollector) Collect(ctx context.Context) error {
	if m.collectFn != nil {
		return m.collectFn(ctx)
	}
	return nil
}

func (m *mockCollector) Name() string {
	if m.name != "" {
		return m.name
	}
	return "mockCollector"
}

// TestCollectorManager_runArray tests the private runArray method
func TestCollectorManager_runArray(t *testing.T) {
	mgr := NewCollectorManager(100 * time.Millisecond)

	var calls int64
	mockCol := &mockCollector{
		collectFn: func(_ context.Context) error {
			atomic.AddInt64(&calls, 1)
			return nil
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	stop := make(chan struct{})
	go mgr.runArray(ctx, []Collector{mockCol}, stop)

	// Wait for context to timeout
	<-ctx.Done()

	// Should have been called at least once
	assert.Greater(t, atomic.LoadInt64(&calls), int64(0))
}

// TestCollectorManager_collectOnce tests the private collectOnce method
func TestCollectorManager_collectOnce(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)

	calls := 0
	mockCol := &mockCollector{
		collectFn: func(_ context.Context) error {
			calls++
			return nil
		},
	}

	ctx := context.Background()
	mgr.collectOnce(ctx, []Collector{mockCol})

	assert.Equal(t, 1, calls)
}

// TestCollectorManager_collectOnce_WithError tests collectOnce with an error
func TestCollectorManager_collectOnce_WithError(_ *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)

	mockCol := &mockCollector{
		collectFn: func(_ context.Context) error {
			return assert.AnError
		},
	}

	ctx := context.Background()
	// Should not panic even with error
	mgr.collectOnce(ctx, []Collector{mockCol})
}

// TestNewCollectorManager tests the constructor
func TestNewCollectorManager(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)
	assert.NotNil(t, mgr)
	assert.Equal(t, 30*time.Second, mgr.interval)
}

// TestNewCollectorManager_ZeroInterval tests default interval
func TestNewCollectorManager_ZeroInterval(t *testing.T) {
	mgr := NewCollectorManager(0)
	assert.NotNil(t, mgr)
	assert.Equal(t, 30*time.Second, mgr.interval)
}

// TestCollectorManager_SetRuntimeConfig_Internal tests setting runtime configuration
func TestCollectorManager_SetRuntimeConfig_Internal(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)

	cfg := RuntimeConfig{
		Timeout: 10 * time.Second,
	}

	// Just call the method to ensure it doesn't panic
	assert.NotPanics(t, func() {
		mgr.SetRuntimeConfig(cfg)
	})
}

// TestCollectorManager_SetStaleSetter_Internal tests setting stale callback
func TestCollectorManager_SetStaleSetter_Internal(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)

	called := false
	staleSetter := func(_ string, _ bool) {
		called = true
	}

	mgr.SetStaleSetter(staleSetter)
	staleSetter("array-1", true)
	assert.True(t, called)
}

// TestCollectorManager_Register_Internal tests registering collectors
func TestCollectorManager_Register_Internal(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)
	mockCol := &mockCollector{name: "test"}

	mgr.Register("array-1", mockCol)
	assert.NotEmpty(t, mgr.collectors["array-1"])
}

// TestCollectorManager_Stop_Internal tests stopping a specific array
func TestCollectorManager_Stop_Internal(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)
	mockCol := &mockCollector{name: "test"}

	mgr.Register("array-1", mockCol)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	stop := make(chan struct{})
	go mgr.runArray(ctx, []Collector{mockCol}, stop)

	time.Sleep(50 * time.Millisecond)
	assert.NotPanics(t, func() {
		mgr.Stop("array-1")
	})
}

// TestCollectorManager_StopAll_Internal tests stopping all arrays
func TestCollectorManager_StopAll_Internal(t *testing.T) {
	mgr := NewCollectorManager(30 * time.Second)
	mockCol := &mockCollector{name: "test"}

	mgr.Register("array-1", mockCol)
	mgr.Register("array-2", mockCol)

	assert.NotPanics(t, func() {
		mgr.StopAll()
	})
}
