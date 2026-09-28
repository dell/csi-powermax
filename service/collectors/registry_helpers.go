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
	"errors"

	"github.com/prometheus/client_golang/prometheus"
)

// registerOrGetGaugeVec registers a GaugeVec with the provided registry.
// If the metric is already registered, it returns the existing collector.
// This allows multiple collector instances to share the same metrics safely.
func registerOrGetGaugeVec(reg prometheus.Registerer, collector *prometheus.GaugeVec) *prometheus.GaugeVec {
	if err := reg.Register(collector); err != nil {
		var alreadyRegisteredErr prometheus.AlreadyRegisteredError
		if errors.As(err, &alreadyRegisteredErr) {
			if existing, ok := alreadyRegisteredErr.ExistingCollector.(*prometheus.GaugeVec); ok {
				return existing
			}
			// Type assertion failed - return nil to indicate error
			return nil
		}
		// Other registration errors - return nil to indicate error
		return nil
	}
	return collector
}
