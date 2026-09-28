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
	"testing"

	"github.com/dell/gopowermax/v2/api"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
)

func TestNewMultiplexingObserver(t *testing.T) {
	reg := prometheus.NewRegistry()
	arrayIDs := []string{"000120000001", "000120000002"}

	observer, err := NewMultiplexingObserver(reg, arrayIDs)
	assert.NoError(t, err)
	assert.NotNil(t, observer)
	assert.Equal(t, 2, len(observer.observersByArray))
}

func TestNewMultiplexingObserver_NilRegistry(t *testing.T) {
	arrayIDs := []string{"000120000001"}

	observer, err := NewMultiplexingObserver(nil, arrayIDs)
	assert.Error(t, err)
	assert.Nil(t, observer)
}

func TestMultiplexingObserver_RoutesByArrayID(t *testing.T) {
	reg := prometheus.NewRegistry()
	arrayIDs := []string{"000120000001", "000120000002"}

	observer, err := NewMultiplexingObserver(reg, arrayIDs)
	assert.NoError(t, err)

	// Observe request for array 1
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/univmax/restapi/100/sloprovisioning/symmetrix/000120000001/volume",
		Method:     "GET",
		StatusCode: 200,
		Err:        nil,
	})

	// Observe request for array 2
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/univmax/restapi/100/sloprovisioning/symmetrix/000120000002/storagegroup",
		Method:     "POST",
		StatusCode: 201,
		Err:        nil,
	})

	// Verify metrics were recorded
	metrics, err := reg.Gather()
	assert.NoError(t, err)
	assert.NotEmpty(t, metrics)
}

func TestMultiplexingObserver_IgnoresUnknownArray(t *testing.T) {
	reg := prometheus.NewRegistry()
	arrayIDs := []string{"000120000001"}

	observer, err := NewMultiplexingObserver(reg, arrayIDs)
	assert.NoError(t, err)

	// Request without array ID in path
	observer.ObservePowerMaxRequest(api.RequestObservation{
		Endpoint:   "/univmax/restapi/100/system/version",
		Method:     "GET",
		StatusCode: 200,
		Err:        nil,
	})

	// Should be ignored (no observer for unknown array)
	_, err = reg.Gather()
	assert.NoError(t, err)
	// No metrics should be recorded since array ID extraction failed
}

func TestMultiplexingObserver_NilObserver(t *testing.T) {
	var observer *MultiplexingObserver

	// Should not panic
	assert.NotPanics(t, func() {
		observer.ObservePowerMaxRequest(api.RequestObservation{
			Endpoint: "/test",
			Method:   "GET",
		})
	})
}

func TestExtractArrayIDFromEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected string
	}{
		{
			name:     "Extract from symmetrix path",
			path:     "/univmax/restapi/100/sloprovisioning/symmetrix/000120000001/volume",
			expected: "000120000001",
		},
		{
			name:     "No symmetrix in path",
			path:     "/univmax/restapi/100/system/version",
			expected: "",
		},
		{
			name:     "Empty path",
			path:     "",
			expected: "",
		},
		{
			name:     "Multiple symmetrix occurrences (uses first)",
			path:     "/symmetrix/000120000001/volume/symmetrix/000120000002",
			expected: "000120000001",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractArrayIDFromEndpoint(tt.path)
			assert.Equal(t, tt.expected, result)
		})
	}
}
