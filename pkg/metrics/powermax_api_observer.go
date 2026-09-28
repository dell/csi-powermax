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
	"fmt"
	"sync"

	naming "github.com/dell/csm-metrics-common/pkg/naming"
	"github.com/dell/gopowermax/v2/api"
	"github.com/prometheus/client_golang/prometheus"
)

const powerMaxAPIMetricName = naming.MetricCSIAPICallTotal

var (
	powerMaxObserverMu       sync.Mutex
	powerMaxObserverByRegKey = map[string]*prometheus.CounterVec{}
)

// PowerMaxAPIObserver records PowerMax REST API metrics.
type PowerMaxAPIObserver struct {
	arrayID     string
	apiRequests *prometheus.CounterVec
}

// NewPowerMaxAPIObserver registers or reuses the API counter in the provided registry.
// Each observer instance is tied to a specific array ID (no dynamic extraction).
func NewPowerMaxAPIObserver(reg prometheus.Registerer, arrayID string) (*PowerMaxAPIObserver, error) {
	if reg == nil {
		return nil, fmt.Errorf("powermax api observer: registry is nil")
	}

	counter, err := getOrCreateCounter(reg)
	if err != nil {
		return nil, err
	}

	return &PowerMaxAPIObserver{
		arrayID:     arrayID,
		apiRequests: counter,
	}, nil
}

func getOrCreateCounter(reg prometheus.Registerer) (*prometheus.CounterVec, error) {
	regKey := fmt.Sprintf("%p", reg)

	powerMaxObserverMu.Lock()
	defer powerMaxObserverMu.Unlock()

	if counter, ok := powerMaxObserverByRegKey[regKey]; ok {
		return counter, nil
	}

	counter := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: powerMaxAPIMetricName,
		Help: "Total PowerMax REST API requests observed by the CSI driver.",
	}, []string{"array_id", "endpoint", "method", "status"})

	if err := reg.Register(counter); err != nil {
		if are, ok := err.(prometheus.AlreadyRegisteredError); ok {
			if existing, ok := are.ExistingCollector.(*prometheus.CounterVec); ok {
				powerMaxObserverByRegKey[regKey] = existing
				return existing, nil
			}
			return nil, fmt.Errorf("powermax api observer: unexpected collector type %T", are.ExistingCollector)
		}
		return nil, fmt.Errorf("powermax api observer: register counter: %w", err)
	}

	powerMaxObserverByRegKey[regKey] = counter
	return counter, nil
}

// ObservePowerMaxRequest implements api.RequestObserver.
func (o *PowerMaxAPIObserver) ObservePowerMaxRequest(obs api.RequestObservation) {
	if o == nil || o.apiRequests == nil {
		return
	}

	status := "failure"
	if obs.Err == nil && obs.StatusCode > 0 && obs.StatusCode < 400 {
		status = "success"
	}

	endpoint := obs.Endpoint
	if endpoint == "" {
		endpoint = "unknown"
	}

	method := obs.Method
	if method == "" {
		method = "UNKNOWN"
	}

	// Use the observer's array ID directly
	o.apiRequests.WithLabelValues(o.arrayID, endpoint, method, status).Inc()
}
