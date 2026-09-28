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

package service

import (
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	defaultMetricsPort = 8443
	metricsCertFile    = "tls.crt"
	metricsKeyFile     = "tls.key"
)

var (
	driverMetricsRegistry     *prometheus.Registry
	driverMetricsRegistryOnce sync.Once
)

func DriverMetricsRegistry() *prometheus.Registry {
	driverMetricsRegistryOnce.Do(func() {
		driverMetricsRegistry = prometheus.NewRegistry()
	})
	return driverMetricsRegistry
}

func DefaultMetricsArrayID() string {
	if v := os.Getenv(EnvManagedArrays); v != "" {
		fields := strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == ';' })
		if len(fields) > 0 {
			return strings.TrimSpace(fields[0])
		}
	}
	if v := os.Getenv("X_CSI_POWERMAX_ARRAY_ID"); v != "" {
		return v
	}
	return "unknown"
}

func MetricsEnabled() bool {
	return metricsEnabled()
}

func metricsEnabled() bool {
	return strings.EqualFold(os.Getenv(EnvMetricsEnabled), "true")
}

func metricsPort() int {
	if v := os.Getenv(EnvMetricsPort); v != "" {
		if p, err := strconv.Atoi(v); err == nil && p > 0 {
			return p
		}
	}
	return defaultMetricsPort
}

func metricsTLSFiles() (string, string) {
	certFile := os.Getenv(EnvMetricsTLSCertFile)
	keyFile := os.Getenv(EnvMetricsTLSKeyFile)
	return certFile, keyFile
}
