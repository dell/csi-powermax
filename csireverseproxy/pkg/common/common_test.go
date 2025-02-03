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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func reportFailure(proxyHealth ProxyHealth, count int, duration time.Duration) bool {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		func() {
			proxyHealth.ReportFailure()
			wg.Done()
		}()
	}
	wg.Wait()
	time.Sleep(duration)
	return proxyHealth.ReportFailure()
}

func reportSuccess(proxyHealth ProxyHealth, count int) {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		func() {
			proxyHealth.ReportSuccess()
			wg.Done()
		}()
	}
	wg.Wait()
}

func TestProxyHealth(t *testing.T) {
	failureCount := 5
	successCount := 2
	maxDuration := time.Second

	proxyHealth := NewProxyHealth()
	proxyHealth.SetThreshold(failureCount, successCount, maxDuration)

	shouldFailover := reportFailure(proxyHealth, failureCount, maxDuration)
	if !shouldFailover {
		t.Error("Health update not working properly")
	}
	if proxyHealth.ReportFailure() {
		t.Error("Health update not working properly")
	}

	shouldFailover = reportFailure(proxyHealth, failureCount, maxDuration)
	if !shouldFailover {
		t.Error("Health update not working properly")
	}
}

func TestHealthReset(t *testing.T) {
	failureCount := 5
	successCount := 2
	maxDuration := time.Second

	proxyHealth := NewProxyHealth()
	proxyHealth.SetThreshold(failureCount, successCount, maxDuration)
	shouldFailover := reportFailure(proxyHealth, 3, time.Nanosecond)
	if shouldFailover {
		t.Error("Health update not working properly")
	}
	reportSuccess(proxyHealth, successCount+1)
	if proxyHealth.HasDeteriorated() {
		t.Error("Proxy health not reset properly")
	} else {
		fmt.Printf("Proxy health reset successfully after %d success requests\n", successCount)
	}
}

type mockRoundTripper struct {
	resp *http.Response
	err  error
}

func (m *mockRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	return m.resp, m.err
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name           string
		resp           *http.Response
		err            error
		expectedHealth bool
	}{
		{
			name: "Successful 2xx response",
			resp: &http.Response{
				StatusCode: http.StatusOK,
			},
			expectedHealth: true,
		},
		{
			name: "Unauthorized response",
			resp: &http.Response{
				StatusCode: http.StatusUnauthorized,
			},
			expectedHealth: false,
		},
		{
			name: "Server error response",
			resp: &http.Response{
				StatusCode: http.StatusInternalServerError,
			},
			expectedHealth: false,
		},
		{
			name:           "Transport error",
			err:            errors.New("network error"),
			expectedHealth: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			healthStatus := false
			tp := &Transport{
				RoundTripper: &mockRoundTripper{resp: tt.resp, err: tt.err},
				HealthHandler: func(status bool) {
					healthStatus = status
				},
			}

			req := httptest.NewRequest("GET", "http://example.com", nil)
			req.Header.Set("RequestID", "12345")
			_, _ = tp.RoundTrip(req)

			if healthStatus != tt.expectedHealth {
				t.Errorf("expected health %v, got %v", tt.expectedHealth, healthStatus)
			}
		})
	}
}
