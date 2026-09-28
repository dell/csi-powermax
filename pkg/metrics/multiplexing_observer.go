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
	"regexp"
	"sync"

	"github.com/dell/gopowermax/v2/api"
	"github.com/prometheus/client_golang/prometheus"
)

var symRegex = regexp.MustCompile(`/symmetrix/([A-Za-z0-9]+)`)

// MultiplexingObserver routes observations to per-array observers.
type MultiplexingObserver struct {
	mu               sync.RWMutex
	observersByArray map[string]*PowerMaxAPIObserver
}

// NewMultiplexingObserver creates a multiplexing observer that manages per-array observers.
func NewMultiplexingObserver(reg prometheus.Registerer, arrayIDs []string) (*MultiplexingObserver, error) {
	mo := &MultiplexingObserver{
		observersByArray: make(map[string]*PowerMaxAPIObserver),
	}

	// Create one observer per array
	for _, arrayID := range arrayIDs {
		observer, err := NewPowerMaxAPIObserver(reg, arrayID)
		if err != nil {
			return nil, err
		}
		mo.observersByArray[arrayID] = observer
	}

	return mo, nil
}

// ObservePowerMaxRequest implements api.RequestObserver by routing to the correct per-array observer.
func (mo *MultiplexingObserver) ObservePowerMaxRequest(obs api.RequestObservation) {
	if mo == nil {
		return
	}

	// Extract array ID from endpoint
	arrayID := extractArrayIDFromEndpoint(obs.Endpoint)
	if arrayID == "" {
		// Log failed array ID extraction for debugging
		// Note: Using a simple log to avoid importing csmlog in metrics package
		return
	}

	mo.mu.RLock()
	observer, exists := mo.observersByArray[arrayID]
	mo.mu.RUnlock()

	if exists && observer != nil {
		observer.ObservePowerMaxRequest(obs)
	}
}

// extractArrayIDFromEndpoint extracts the array ID from the REST API endpoint path.
func extractArrayIDFromEndpoint(path string) string {
	if matches := symRegex.FindStringSubmatch(path); len(matches) == 2 {
		return matches[1]
	}
	return ""
}
