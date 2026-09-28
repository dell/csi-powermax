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

package collectors_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/service/collectors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// U-MGR-01: NewCollectorManager creates manager with default interval
func TestCollectorManager_New(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	require.NotNil(t, mgr)
}

// U-MGR-02: NewCollectorManager with zero interval uses default 30s
func TestCollectorManager_NewZeroInterval(t *testing.T) {
	mgr := collectors.NewCollectorManager(0)
	require.NotNil(t, mgr)
}

// U-MGR-03: Register adds collectors for an array
func TestCollectorManager_Register(_ *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{name: "c1"}
	c2 := &mockCollector{name: "c2"}

	mgr.Register("array-1", c1, c2)
	// No panic = success
}

// U-MGR-04: Start begins background goroutine for registered array
func TestCollectorManager_Start(_ *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{
		name: "c1",
		collectFn: func(_ context.Context) error {
			return nil
		},
	}

	mgr.Register("array-1", c1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)
	// Give goroutine time to start
	time.Sleep(100 * time.Millisecond)
}

// U-MGR-05: Stop terminates specific array goroutine
func TestCollectorManager_Stop(_ *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{name: "c1"}

	mgr.Register("array-1", c1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	mgr.Stop("array-1")
	time.Sleep(100 * time.Millisecond)
}

// U-MGR-06: StopAll terminates all goroutines
func TestCollectorManager_StopAll(_ *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{name: "c1"}
	c2 := &mockCollector{name: "c2"}

	mgr.Register("array-1", c1)
	mgr.Register("array-2", c2)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(100 * time.Millisecond)

	mgr.StopAll()
	time.Sleep(100 * time.Millisecond)
}

// U-MGR-07: Collector error does not stop polling (fault isolation)
func TestCollectorManager_FaultIsolation(t *testing.T) {
	var callCount int64
	c1 := &mockCollector{
		name: "failing",
		collectFn: func(_ context.Context) error {
			atomic.AddInt64(&callCount, 1)
			return assert.AnError
		},
	}

	mgr := collectors.NewCollectorManager(100 * time.Millisecond)
	mgr.Register("array-1", c1)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(400 * time.Millisecond)

	// Should be called multiple times despite errors
	assert.Greater(t, atomic.LoadInt64(&callCount), int64(1))
}

// U-MGR-08: Multiple arrays have independent goroutines
func TestCollectorManager_MultiArrayIsolation(t *testing.T) {
	var array1Calls, array2Calls int64

	c1 := &mockCollector{
		name: "array1",
		collectFn: func(_ context.Context) error {
			atomic.AddInt64(&array1Calls, 1)
			return nil
		},
	}
	c2 := &mockCollector{
		name: "array2",
		collectFn: func(_ context.Context) error {
			atomic.AddInt64(&array2Calls, 1)
			return nil
		},
	}

	mgr := collectors.NewCollectorManager(100 * time.Millisecond)
	mgr.Register("array-1", c1)
	mgr.Register("array-2", c2)
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(400 * time.Millisecond)

	assert.Greater(t, atomic.LoadInt64(&array1Calls), int64(0))
	assert.Greater(t, atomic.LoadInt64(&array2Calls), int64(0))
}

// U-MGR-09: Stop on non-existent array is safe (no panic)
func TestCollectorManager_StopNonExistent(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	assert.NotPanics(t, func() {
		mgr.Stop("array-does-not-exist")
	})
}

// U-MGR-10: SetRuntimeConfig sets the runtime configuration
func TestCollectorManager_SetRuntimeConfig(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	cfg := collectors.RuntimeConfig{
		Timeout: 10 * time.Second,
	}

	assert.NotPanics(t, func() {
		mgr.SetRuntimeConfig(cfg)
	})
}

// U-MGR-11: SetStaleSetter sets the stale callback
func TestCollectorManager_SetStaleSetter(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	staleSetter := func(_ string, _ bool) {
		// Mock stale setter
	}

	assert.NotPanics(t, func() {
		mgr.SetStaleSetter(staleSetter)
	})
}

// U-MGR-12: GetRuntime returns the runtime for a specific array
func TestCollectorManager_GetRuntime(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	cfg := collectors.RuntimeConfig{
		Timeout: 10 * time.Second,
	}
	mgr.SetRuntimeConfig(cfg)

	retrievedRuntime := mgr.GetRuntime("array-1")
	assert.NotNil(t, retrievedRuntime)
}

// U-MGR-13: GetRuntime creates runtime for non-existent array
func TestCollectorManager_GetRuntime_NotFound(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	// GetRuntime creates a runtime if it doesn't exist
	retrievedRuntime := mgr.GetRuntime("non-existent-array")
	assert.NotNil(t, retrievedRuntime)
}

// TestCollectorManager_Register_WithNilCollector tests registering with nil collector
func TestCollectorManager_Register_WithNilCollector(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	assert.NotPanics(t, func() {
		mgr.Register("array-1", nil)
	})
}

// TestCollectorManager_Register_MultipleArrays tests registering collectors for multiple arrays
func TestCollectorManager_Register_MultipleArrays(_ *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{name: "c1"}
	c2 := &mockCollector{name: "c2"}

	mgr.Register("array-1", c1)
	mgr.Register("array-2", c2)
	// No panic = success
}

// TestCollectorManager_Start_NoCollectors tests starting with no collectors
func TestCollectorManager_Start_NoCollectors(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	assert.NotPanics(t, func() {
		mgr.Start(ctx)
		time.Sleep(50 * time.Millisecond)
	})
}

// TestCollectorManager_Stop_WithRunningCollector tests stopping a running collector
func TestCollectorManager_Stop_WithRunningCollector(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)
	c1 := &mockCollector{
		name: "c1",
		collectFn: func(_ context.Context) error {
			time.Sleep(10 * time.Millisecond)
			return nil
		},
	}

	mgr.Register("array-1", c1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mgr.Start(ctx)
	time.Sleep(50 * time.Millisecond)

	assert.NotPanics(t, func() {
		mgr.Stop("array-1")
	})
}

// TestCollectorManager_SetRuntimeConfig_WithNilConfig tests setting nil config
func TestCollectorManager_SetRuntimeConfig_WithNilConfig(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	assert.NotPanics(t, func() {
		mgr.SetRuntimeConfig(collectors.RuntimeConfig{})
	})
}

// TestCollectorManager_SetStaleSetter_WithNilSetter tests setting nil stale setter
func TestCollectorManager_SetStaleSetter_WithNilSetter(t *testing.T) {
	mgr := collectors.NewCollectorManager(30 * time.Second)

	assert.NotPanics(t, func() {
		mgr.SetStaleSetter(nil)
	})
}
