/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package common

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dell/csmlog"
)

const (
	// MinimumFailureCountForFailover - threshold beyond which failover can be done to the backup server
	MinimumFailureCountForFailover = 10
	// MaximumFailureDurationForFailover - max duration after which failover is attempted
	MaximumFailureDurationForFailover = 1 * time.Minute
	// MinimumSuccessCount - minimum number of successes to reset the proxy health
	MinimumSuccessCount = 5
	// MaximumConsecutiveFailureCountForFailover - max consecutive failure count irrespective of time to do failover.
	MaximumConsecutiveFailureCountForFailover = 10
)

var log = csmlog.GetLogger()

// Credentials represent a pair of username and password
type Credentials struct {
	UserName string `json:"username"`
	Password string `json:"password"`
}

// ProxyHealth - interface which is implemented by the different proxies for
// tracking their health
type ProxyHealth interface {
	ReportFailure() bool
	ReportSuccess()
	SetThreshold(int, int, int, time.Duration)
	HasDeteriorated() bool
}

// NewProxyHealth - creates and returns a new proxyHealth instance
func NewProxyHealth() ProxyHealth {
	ph := &proxyHealth{
		minimumFailureCount:     MinimumFailureCountForFailover,
		maximumFailureDuration:  MaximumFailureDurationForFailover,
		minimumSuccessCount:     MinimumSuccessCount,
		maximumConsFailureCount: MaximumConsecutiveFailureCountForFailover,
	}
	ph.hasDeteriorated.Store(false)
	return ph
}

type proxyHealth struct {
	failureCount            int
	successCount            int
	consecutiveFailureCount int
	firstFailure            time.Time
	minimumFailureCount     int
	minimumSuccessCount     int
	maximumConsFailureCount int
	maximumFailureDuration  time.Duration
	healthMutex             sync.Mutex
	hasDeteriorated         atomic.Value
}

func (health *proxyHealth) reset() {
	health.hasDeteriorated.Store(false)
	health.successCount = 0
	health.failureCount = 0
}

// ReportSuccess resets the proxy health if the successCount
// reaches a certain threshold
func (health *proxyHealth) ReportSuccess() {
	health.healthMutex.Lock()
	defer health.healthMutex.Unlock()
	// reset the consecutive counter for success
	if health.consecutiveFailureCount > 0 {
		health.consecutiveFailureCount = 0
	}

	// if the proxy is already in a deteriorated state, check for success count and reset based on minimumSuccessCount
	if health.hasDeteriorated.Load().(bool) {
		if health.successCount < health.minimumSuccessCount {
			health.successCount++
		} else {
			health.reset()
		}
	}
}

// ReportFailure - updates the health of the proxy in case of failure
// or clears the failure count if the error threshold is not met after maximum
// failure duration
func (health *proxyHealth) ReportFailure() bool {
	health.healthMutex.Lock()
	defer health.healthMutex.Unlock()
	if health.failureCount == 0 {
		health.hasDeteriorated.Store(true)
		health.firstFailure = time.Now()
		health.failureCount = 1
		health.consecutiveFailureCount++
		return false
	}
	health.consecutiveFailureCount++
	// if the continuous failure count is greater than the maximum failure count, return true
	if health.consecutiveFailureCount > health.maximumConsFailureCount {
		log.Debugf("consecutive failure reached!")
		health.consecutiveFailureCount = 0
		health.reset()
		return true
	}
	timeElapsed := time.Since(health.firstFailure)
	// if the time elapsed is greater than the maximum failure duration, check for failure count
	if timeElapsed < health.maximumFailureDuration {
		health.failureCount++
		return false
	}
	failureCount := health.failureCount
	health.reset()
	// if the failure count is less than the minimum failure counts, return false as a threshold not met
	if failureCount < health.minimumFailureCount {
		return false
	}
	return true
}

// SetThreshold - sets the threshold values for proxy health
func (health *proxyHealth) SetThreshold(failureCount, consecutiveFC, successCount int, duration time.Duration) {
	health.maximumFailureDuration = duration
	health.minimumFailureCount = failureCount
	health.minimumSuccessCount = successCount
	health.maximumConsFailureCount = consecutiveFC
}

// HasDeteriorated returns true if the proxyHealth object has recorded even a single failure
func (health *proxyHealth) HasDeteriorated() bool {
	return health.hasDeteriorated.Load().(bool)
}

// Proxy - interface which is implemented by different types of Proxies
type Proxy struct {
	ReverseProxy *httputil.ReverseProxy
	URL          url.URL
	Limits       Limits
}

// SymmURL - represents a combination of a symmetrix id and the corresponding management URL
type SymmURL struct {
	SymmetrixID string
	URL         url.URL
}

// ModifyResponse - callback function which is called by the reverseproxy
// currently not implemented
func (proxy *Proxy) ModifyResponse(res *http.Response) error {
	return nil
}

// LockType represents the type of locks being taken against Unisphere
// currently the supported values are Read and Write
type LockType string

// Transport - represents the custom http transport implementation
type Transport struct {
	http.RoundTripper
	HealthHandler func(bool)
}

// RoundTrip - this method is called every time after the completion of the reverseproxy
// request
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	requestID := req.Header.Get("RequestID")
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		t.HealthHandler(false)
		log.Debugf("Request ID: %s - Reporing server error to proxy health", requestID)
	} else if resp.StatusCode == 401 || resp.StatusCode == 403 || int(resp.StatusCode/100) == 5 {
		t.HealthHandler(false)
		log.Debugf("Request ID: %s - Reporing non-200 code to proxy health", requestID)
	} else if int(resp.StatusCode/100) == 2 {
		t.HealthHandler(true)
		log.Debugf("Request ID: %s - Reporting 2xx response to proxy health", requestID)
	}
	return resp, err
}

// Limits is used for storing the various types of limits
// applied for a particular proxy instance
type Limits struct {
	MaxActiveRead       int `yaml:"maxActiveRead,omitempty"`
	MaxActiveWrite      int `yaml:"maxActiveWrite,omitempty"`
	MaxOutStandingRead  int `yaml:"maxOutStandingRead,omitempty"`
	MaxOutStandingWrite int `yaml:"maxOutStandingWrite,omitempty"`
}
