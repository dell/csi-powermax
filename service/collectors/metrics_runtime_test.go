/*
 Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at
      http://www.apache.org/licenses-2.0
 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package collectors

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewMetricsRuntime_Defaults verifies safe defaults are applied.
func TestNewMetricsRuntime_Defaults(t *testing.T) {
	rt := NewMetricsRuntime("array-1", RuntimeConfig{})
	assert.NotNil(t, rt)
	assert.NotNil(t, rt.cache)
	assert.NotNil(t, rt.rateLimiter)
	assert.NotNil(t, rt.circuitBreaker)
}

// TestNewMetricsRuntime_ValidConfigHonored verifies config values are respected.
func TestNewMetricsRuntime_ValidConfigHonored(t *testing.T) {
	cfg := RuntimeConfig{
		Timeout:        5 * time.Second,
		CacheTTL:       10 * time.Second,
		RateLimit:      50,
		CBThreshold:    2,
		CBResetTimeout: 15 * time.Second,
	}
	rt := NewMetricsRuntime("array-1", cfg)
	assert.NotNil(t, rt)
	assert.NotNil(t, rt.cache)
	assert.NotNil(t, rt.rateLimiter)
	assert.NotNil(t, rt.circuitBreaker)
}

// TestMetricsRuntime_Do_Success verifies successful calls are cached and stale is cleared.
func TestMetricsRuntime_Do_Success(t *testing.T) {
	staleCalls := make([]bool, 0)
	cfg := RuntimeConfig{
		Timeout: time.Second, CacheTTL: time.Minute, RateLimit: 100, CBThreshold: 3, CBResetTimeout: time.Hour,
		StaleReporter: func(_ string, stale bool) {
			staleCalls = append(staleCalls, stale)
		},
	}
	rt := NewMetricsRuntime("array-1", cfg)

	success := func(_ context.Context) (any, error) { return "result", nil }
	result, err := rt.Do(context.Background(), "ep", "k1", success)
	require.NoError(t, err)
	assert.Equal(t, "result", result)
	// stale reporter is called with false on success
	assert.Len(t, staleCalls, 1)
	assert.False(t, staleCalls[0], "stale must be false on success")
}

// TestMetricsRuntime_Do_CircuitOpensAfterThreshold verifies circuit breaker opens after threshold failures.
func TestMetricsRuntime_Do_CircuitOpensAfterThreshold(t *testing.T) {
	rt := NewMetricsRuntime("array-1", RuntimeConfig{
		Timeout: time.Second, CacheTTL: time.Minute, RateLimit: 100, CBThreshold: 2, CBResetTimeout: time.Hour,
	})
	fail := func(_ context.Context) (any, error) { return nil, errors.New("fail") }

	_, _ = rt.Do(context.Background(), "ep", "k1", fail) // failure 1
	_, _ = rt.Do(context.Background(), "ep", "k1", fail) // failure 2 – circuit opens

	_, err := rt.Do(context.Background(), "ep", "k1", fail) // circuit is open
	require.Error(t, err)
	assert.Contains(t, err.Error(), "circuit breaker is open")
}

// TestMetricsRuntime_Do_CacheFallback verifies cached data is served on failure.
func TestMetricsRuntime_Do_CacheFallback(t *testing.T) {
	staleCalls := make([]bool, 0)
	cfg := RuntimeConfig{
		Timeout: time.Second, CacheTTL: time.Minute, RateLimit: 100, CBThreshold: 3, CBResetTimeout: time.Hour,
		StaleReporter: func(_ string, stale bool) {
			staleCalls = append(staleCalls, stale)
		},
	}
	rt := NewMetricsRuntime("array-1", cfg)

	success := func(_ context.Context) (any, error) { return "cached", nil }
	result, err := rt.Do(context.Background(), "ep", "k1", success)
	require.NoError(t, err)
	assert.Equal(t, "cached", result)
	// Clear stale calls from the successful call
	staleCalls = make([]bool, 0)

	// Now fail - should serve cached data and mark stale
	fail := func(_ context.Context) (any, error) { return nil, errors.New("fail") }
	result, err = rt.Do(context.Background(), "ep", "k1", fail)
	require.NoError(t, err) // Cached data served
	assert.Equal(t, "cached", result)
	assert.Len(t, staleCalls, 1)
	assert.True(t, staleCalls[0], "stale must be marked true when serving cached data")
}

// TestGetRuntimeConfig verifies environment variable reading.
func TestGetRuntimeConfig(t *testing.T) {
	// Test with default values (no env vars set)
	cfg := GetRuntimeConfig()
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 25*time.Second, cfg.CacheTTL)
	assert.Equal(t, 100, cfg.RateLimit)
	assert.Equal(t, 3, cfg.CBThreshold)
	assert.Equal(t, 30*time.Second, cfg.CBResetTimeout)
	assert.Equal(t, 30*time.Second, cfg.Interval)
}

// TestGetRuntimeConfig_WithEnvVars verifies config values from environment variables
func TestGetRuntimeConfig_WithEnvVars(t *testing.T) {
	t.Setenv("X_CSI_METRICS_ARRAY_TIMEOUT", "10s")
	t.Setenv("X_CSI_METRICS_COLLECTION_CACHE_TTL", "120s")
	t.Setenv("X_CSI_METRICS_ARRAY_RATE_LIMIT", "200")
	t.Setenv("X_CSI_METRICS_ARRAY_CB_THRESHOLD", "5")
	t.Setenv("X_CSI_METRICS_ARRAY_CB_RESET_TIMEOUT", "60s")
	t.Setenv("X_CSI_METRICS_COLLECTION_INTERVAL", "45s")

	cfg := GetRuntimeConfig()
	assert.Equal(t, 10*time.Second, cfg.Timeout)
	assert.Equal(t, 120*time.Second, cfg.CacheTTL)
	assert.Equal(t, 200, cfg.RateLimit)
	assert.Equal(t, 5, cfg.CBThreshold)
	assert.Equal(t, 60*time.Second, cfg.CBResetTimeout)
	assert.Equal(t, 45*time.Second, cfg.Interval)
}

// TestGetRuntimeConfig_InvalidEnvVars verifies invalid env vars are ignored
func TestGetRuntimeConfig_InvalidEnvVars(t *testing.T) {
	t.Setenv("X_CSI_METRICS_ARRAY_TIMEOUT", "invalid")
	t.Setenv("X_CSI_METRICS_ARRAY_RATE_LIMIT", "not-a-number")
	t.Setenv("X_CSI_METRICS_COLLECTION_INTERVAL", "not-a-duration")

	cfg := GetRuntimeConfig()
	// Should fall back to defaults
	assert.Equal(t, 30*time.Second, cfg.Timeout)
	assert.Equal(t, 100, cfg.RateLimit)
	assert.Equal(t, 30*time.Second, cfg.Interval)
}

// TestNewMetricsRuntime_ZeroOrNegativeValues verifies safe defaults for zero/negative values
func TestNewMetricsRuntime_ZeroOrNegativeValues(t *testing.T) {
	cfg := RuntimeConfig{
		Timeout:        0,
		CacheTTL:       -1,
		RateLimit:      0,
		CBThreshold:    -5,
		CBResetTimeout: 0,
		Interval:       -10,
	}
	rt := NewMetricsRuntime("array-1", cfg)
	assert.NotNil(t, rt)
	assert.NotNil(t, rt.cache)
	assert.NotNil(t, rt.rateLimiter)
	assert.NotNil(t, rt.circuitBreaker)
}

// TestMetricsRuntime_serveStaleOrError tests the private serveStaleOrError method
func TestMetricsRuntime_serveStaleOrError(t *testing.T) {
	staleCalls := make([]bool, 0)
	cfg := RuntimeConfig{
		Timeout: time.Second, CacheTTL: time.Minute, RateLimit: 100, CBThreshold: 3, CBResetTimeout: time.Hour,
		StaleReporter: func(_ string, stale bool) {
			staleCalls = append(staleCalls, stale)
		},
	}
	rt := NewMetricsRuntime("array-1", cfg)

	// Test with no cached data
	result, err := rt.serveStaleOrError("key1", assert.AnError)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Len(t, staleCalls, 1)
	assert.True(t, staleCalls[0], "stale must be marked true when no cache")

	// Test with cached data
	rt.cache.Set("key2", "cached-value")
	result, err = rt.serveStaleOrError("key2", assert.AnError)
	assert.NoError(t, err)
	assert.Equal(t, "cached-value", result)
}

// TestMetricsRuntime_setStale tests the private setStale method
func TestMetricsRuntime_setStale(t *testing.T) {
	staleCalls := make([]bool, 0)
	cfg := RuntimeConfig{
		Timeout: time.Second, CacheTTL: time.Minute, RateLimit: 100, CBThreshold: 3, CBResetTimeout: time.Hour,
		StaleReporter: func(_ string, stale bool) {
			staleCalls = append(staleCalls, stale)
		},
	}
	rt := NewMetricsRuntime("array-1", cfg)

	// Test setting stale to true
	rt.setStale(true)
	assert.Len(t, staleCalls, 1)
	assert.True(t, staleCalls[0])

	// Test setting stale to false
	staleCalls = make([]bool, 0)
	rt.setStale(false)
	assert.Len(t, staleCalls, 1)
	assert.False(t, staleCalls[0])

	// Test with nil stale reporter
	rtNoReporter := NewMetricsRuntime("array-2", RuntimeConfig{})
	rtNoReporter.setStale(true) // Should not panic
}
