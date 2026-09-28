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

package metrics

import (
	"errors"
	"testing"

	"github.com/dell/gopowermax/v2/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

// TestNewPowerMaxAPIObserver tests the NewPowerMaxAPIObserver function
func TestNewPowerMaxAPIObserver(t *testing.T) {
	tests := []struct {
		name           string
		reg            prometheus.Registerer
		defaultArrayID string
		expectError    bool
	}{
		{
			name:           "Nil registry returns error",
			reg:            nil,
			defaultArrayID: "000120000001",
			expectError:    true,
		},
		{
			name:           "Valid registry creates observer",
			reg:            prometheus.NewRegistry(),
			defaultArrayID: "000120000001",
			expectError:    false,
		},
		{
			name:           "Empty array ID is accepted",
			reg:            prometheus.NewRegistry(),
			defaultArrayID: "",
			expectError:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			observer, err := NewPowerMaxAPIObserver(tt.reg, tt.defaultArrayID)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, observer)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, observer)
				assert.Equal(t, tt.defaultArrayID, observer.arrayID)
				assert.NotNil(t, observer.apiRequests)
			}
		})
	}
}

// TestNewPowerMaxAPIObserver_RegistryError tests error path when registry fails
func TestNewPowerMaxAPIObserver_RegistryError(t *testing.T) {
	// Create a registry that will fail when we try to register
	reg := prometheus.NewRegistry()

	// Register a different collector with the same name to cause AlreadyRegisteredError
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name: powerMaxAPIMetricName,
	})
	reg.MustRegister(gauge)

	// This should fail with AlreadyRegisteredError
	observer, err := NewPowerMaxAPIObserver(reg, "000120000001")
	assert.Error(t, err)
	assert.Nil(t, observer)
}

// TestGetOrCreateCounter tests the getOrCreateCounter function
func TestGetOrCreateCounter(t *testing.T) {
	tests := []struct {
		name        string
		reg         prometheus.Registerer
		expectError bool
	}{
		{
			name:        "Create new counter",
			reg:         prometheus.NewRegistry(),
			expectError: false,
		},
		{
			name:        "Reuse existing counter",
			reg:         prometheus.NewRegistry(),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			counter1, err := getOrCreateCounter(tt.reg)
			if tt.expectError {
				assert.Error(t, err)
				assert.Nil(t, counter1)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, counter1)

				// Try to get the same counter again - should return the same instance
				counter2, err := getOrCreateCounter(tt.reg)
				assert.NoError(t, err)
				assert.Equal(t, counter1, counter2)
			}
		})
	}
}

// TestObservePowerMaxRequest tests the ObservePowerMaxRequest function
func TestObservePowerMaxRequest(t *testing.T) {
	tests := []struct {
		name        string
		observer    *PowerMaxAPIObserver
		observation api.RequestObservation
		expectPanic bool
	}{
		{
			name:        "Nil observer does not panic",
			observer:    nil,
			observation: api.RequestObservation{},
			expectPanic: false,
		},
		{
			name: "Observer with nil apiRequests does not panic",
			observer: &PowerMaxAPIObserver{
				apiRequests: nil,
			},
			observation: api.RequestObservation{},
			expectPanic: false,
		},
		{
			name: "Successful request observation",
			observer: &PowerMaxAPIObserver{
				arrayID:     "000120000001",
				apiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"array_id", "endpoint", "method", "status"}),
			},
			observation: api.RequestObservation{
				Endpoint:   "/symmetrix/000120000001/storagegroup",
				Method:     "GET",
				StatusCode: 200,
				Err:        nil,
			},
			expectPanic: false,
		},
		{
			name: "Failed request observation",
			observer: &PowerMaxAPIObserver{
				arrayID:     "000120000001",
				apiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"array_id", "endpoint", "method", "status"}),
			},
			observation: api.RequestObservation{
				Endpoint:   "/symmetrix/000120000001/storagegroup",
				Method:     "POST",
				StatusCode: 500,
				Err:        errors.New("internal error"),
			},
			expectPanic: false,
		},
		{
			name: "Request with empty endpoint",
			observer: &PowerMaxAPIObserver{
				arrayID:     "000120000001",
				apiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"array_id", "endpoint", "method", "status"}),
			},
			observation: api.RequestObservation{
				Endpoint:   "",
				Method:     "GET",
				StatusCode: 200,
				Err:        nil,
			},
			expectPanic: false,
		},
		{
			name: "Request with empty method",
			observer: &PowerMaxAPIObserver{
				arrayID:     "000120000001",
				apiRequests: prometheus.NewCounterVec(prometheus.CounterOpts{}, []string{"array_id", "endpoint", "method", "status"}),
			},
			observation: api.RequestObservation{
				Endpoint:   "/symmetrix/000120000001/storagegroup",
				Method:     "",
				StatusCode: 200,
				Err:        nil,
			},
			expectPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.expectPanic {
				assert.NotPanics(t, func() {
					tt.observer.ObservePowerMaxRequest(tt.observation)
				})
			}
		})
	}
}

// TestPowerMaxAPIObserver_Integration tests the observer with a real registry
func TestPowerMaxAPIObserver_Integration(t *testing.T) {
	reg := prometheus.NewRegistry()
	observer, err := NewPowerMaxAPIObserver(reg, "000120000001")
	assert.NoError(t, err)
	assert.NotNil(t, observer)

	// Test successful request
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/symmetrix/000120000001/storagegroup",
		Method:     "GET",
		StatusCode: 200,
		Err:        nil,
	})

	// Test failed request
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/symmetrix/000120000001/volume",
		Method:     "POST",
		StatusCode: 500,
		Err:        errors.New("error"),
	})

	// Test request with unknown array ID
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/api/volume",
		Method:     "GET",
		StatusCode: 200,
		Err:        nil,
	})
}
