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
	"fmt"
	"os"
	"strconv"
	"time"

	csmcache "github.com/dell/csm-metrics-common/pkg/cache"
	"github.com/dell/csm-metrics-common/pkg/middleware"
)

// RuntimeConfig holds the tuning parameters for PowerMax metrics calls.
// Zero values are replaced with safe defaults in NewMetricsRuntime.
type RuntimeConfig struct {
	Timeout        time.Duration
	CacheTTL       time.Duration
	RateLimit      int
	CBThreshold    int
	CBResetTimeout time.Duration
	Interval       time.Duration
	StaleReporter  func(arrayID string, stale bool)
}

// MetricsRuntime wraps PowerMax metrics calls with timeout, rate limiting,
// circuit breaking, and response caching. It is scoped per arrayID so that
// failures on one array do not affect others.
type MetricsRuntime struct {
	arrayID        string
	timeout        time.Duration
	cache          *csmcache.ResponseCache
	rateLimiter    *middleware.RateLimiter
	circuitBreaker *middleware.CircuitBreaker
	staleReporter  func(arrayID string, stale bool)
}

// NewMetricsRuntime creates a MetricsRuntime for the given arrayID using
// the provided configuration. Zero or negative config values are replaced
// with safe defaults so callers never need to guard against invalid opts.
func NewMetricsRuntime(arrayID string, cfg RuntimeConfig) *MetricsRuntime {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 60 * time.Second
	}
	if cfg.RateLimit <= 0 {
		cfg.RateLimit = 100
	}
	if cfg.CBThreshold <= 0 {
		cfg.CBThreshold = 3
	}
	if cfg.CBResetTimeout <= 0 {
		cfg.CBResetTimeout = 30 * time.Second
	}
	return &MetricsRuntime{
		arrayID:        arrayID,
		timeout:        cfg.Timeout,
		cache:          csmcache.NewResponseCache(cfg.CacheTTL),
		rateLimiter:    middleware.NewRateLimiter(cfg.RateLimit),
		circuitBreaker: middleware.NewCircuitBreaker(arrayID, cfg.CBThreshold, cfg.CBResetTimeout),
		staleReporter:  cfg.StaleReporter,
	}
}

// Do executes fn through rate limiting, timeout, and circuit breaking, then
// caches the result. On failure, a cached result is served when available and
// the stale indicator is set. On success the stale indicator is cleared.
//
// endpoint is a logical name used as the rate-limiter bucket (e.g. "volumes").
// cacheKey must be unique per endpoint+parameter combination within an arrayID.
func (r *MetricsRuntime) Do(ctx context.Context, endpoint, cacheKey string, fn func(context.Context) (any, error)) (any, error) {
	// 1. Rate-limit before acquiring timeout budget.
	if err := r.rateLimiter.Wait(ctx, endpoint); err != nil {
		return r.serveStaleOrError(cacheKey, fmt.Errorf("rate limiter cancelled for %s/%s: %w", r.arrayID, endpoint, err))
	}

	// 2. Derive a timeout-bound context for the actual PowerMax call.
	callCtx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	// 3. Execute through the circuit breaker.
	var result any
	cbErr := r.circuitBreaker.Call(func() error {
		v, err := fn(callCtx)
		if err != nil {
			return err
		}
		result = v
		return nil
	})

	if cbErr != nil {
		return r.serveStaleOrError(cacheKey, fmt.Errorf("%s/%s: %w", r.arrayID, endpoint, cbErr))
	}

	// 4. Success: cache result and clear stale flag.
	r.cache.Set(cacheKey, result)
	r.setStale(false)
	return result, nil
}

// serveStaleOrError returns a cached result (marking metrics stale) when one
// is available, or propagates err when the cache is empty.
func (r *MetricsRuntime) serveStaleOrError(cacheKey string, err error) (any, error) {
	r.setStale(true)
	if cached, ok := r.cache.Get(cacheKey); ok {
		return cached, nil
	}
	return nil, err
}

func (r *MetricsRuntime) setStale(stale bool) {
	if r.staleReporter != nil {
		r.staleReporter(r.arrayID, stale)
	}
}

// GetRuntimeConfig reads MetricsRuntime configuration from environment variables.
// Defaults: Timeout=30s, CacheTTL=25s, RateLimit=100, CBThreshold=3, CBResetTimeout=30s, Interval=30s
func GetRuntimeConfig() RuntimeConfig {
	timeout := 30 * time.Second
	if v := os.Getenv("X_CSI_METRICS_ARRAY_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			timeout = d
		}
	}

	cacheTTL := 25 * time.Second
	if v := os.Getenv("X_CSI_METRICS_COLLECTION_CACHE_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cacheTTL = d
		}
	}

	rateLimit := 100
	if v := os.Getenv("X_CSI_METRICS_ARRAY_RATE_LIMIT"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			rateLimit = i
		}
	}

	cbThreshold := 3
	if v := os.Getenv("X_CSI_METRICS_ARRAY_CB_THRESHOLD"); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			cbThreshold = i
		}
	}

	cbResetTimeout := 30 * time.Second
	if v := os.Getenv("X_CSI_METRICS_ARRAY_CB_RESET_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			cbResetTimeout = d
		}
	}

	interval := 30 * time.Second
	if v := os.Getenv("X_CSI_METRICS_COLLECTION_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			interval = d
		}
	}

	return RuntimeConfig{
		Timeout:        timeout,
		CacheTTL:       cacheTTL,
		RateLimit:      rateLimit,
		CBThreshold:    cbThreshold,
		CBResetTimeout: cbResetTimeout,
		Interval:       interval,
	}
}
