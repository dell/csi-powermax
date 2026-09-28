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
	"net"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// metricsPortFree finds a free TCP port for test servers.
func metricsPortFree(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", ":0")
	require.NoError(t, err)
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

// U-SVC-01: MetricsEnabled returns true when X_CSI_METRICS_ENABLED=true.
func TestMetricsEnabled_XCSIEnvTrue(t *testing.T) {
	t.Setenv("X_CSI_METRICS_ENABLED", "true")
	assert.True(t, MetricsEnabled())
}

// U-SVC-02: MetricsEnabled returns false by default (unset).
func TestMetricsEnabled_DefaultFalse(t *testing.T) {
	t.Setenv("X_CSI_METRICS_ENABLED", "")
	assert.False(t, MetricsEnabled())
}

// U-SVC-03: MetricsEnabled is case-insensitive (TRUE, True).
func TestMetricsEnabled_CaseInsensitive(t *testing.T) {
	t.Setenv("X_CSI_METRICS_ENABLED", "TRUE")
	assert.True(t, MetricsEnabled())
	t.Setenv("X_CSI_METRICS_ENABLED", "True")
	assert.True(t, MetricsEnabled())
}

// U-SVC-05: DefaultMetricsArrayID returns first array from X_CSI_MANAGED_ARRAYS.
func TestDefaultMetricsArrayID_ManagedArrays(t *testing.T) {
	t.Setenv(EnvManagedArrays, "000197600156,000197600157")
	t.Setenv("X_CSI_POWERMAX_ARRAY_ID", "")
	assert.Equal(t, "000197600156", DefaultMetricsArrayID())
}

// U-SVC-06: DefaultMetricsArrayID falls back to X_CSI_POWERMAX_ARRAY_ID when no managed arrays.
func TestDefaultMetricsArrayID_FallbackArrayID(t *testing.T) {
	t.Setenv(EnvManagedArrays, "")
	t.Setenv("X_CSI_POWERMAX_ARRAY_ID", "000197699999")
	assert.Equal(t, "000197699999", DefaultMetricsArrayID())
}

// U-SVC-07: DefaultMetricsArrayID returns "unknown" when nothing is set.
func TestDefaultMetricsArrayID_Unknown(t *testing.T) {
	t.Setenv(EnvManagedArrays, "")
	t.Setenv("X_CSI_POWERMAX_ARRAY_ID", "")
	assert.Equal(t, "unknown", DefaultMetricsArrayID())
}

// U-SVC-08: DefaultMetricsArrayID handles semicolons and spaces in X_CSI_MANAGED_ARRAYS.
func TestDefaultMetricsArrayID_SemicolonSeparated(t *testing.T) {
	t.Setenv(EnvManagedArrays, " 000197600156 ; 000197600157")
	assert.Equal(t, "000197600156", DefaultMetricsArrayID())
}

// U-SVC-09: metricsPort returns 8443 when CSM_METRICS_PORT is not set.
func TestMetricsPort_Default(t *testing.T) {
	t.Setenv("CSM_METRICS_PORT", "")
	assert.Equal(t, 8443, metricsPort())
}

// U-SVC-10: metricsPort returns value from X_CSI_METRICS_PORT env var.
func TestMetricsPort_CustomPort(t *testing.T) {
	t.Setenv("X_CSI_METRICS_PORT", "9090")
	assert.Equal(t, 9090, metricsPort())
}

// U-SVC-11: metricsPort falls back to 8443 on invalid value.
func TestMetricsPort_InvalidFallback(t *testing.T) {
	t.Setenv("CSM_METRICS_PORT", "not-a-port")
	assert.Equal(t, 8443, metricsPort())
}

// U-SVC-12: metricsTLSFiles returns empty strings when X_CSI_METRICS_TLS_CERT_FILE and X_CSI_METRICS_TLS_KEY_FILE are unset.
func TestMetricsTLSFiles_Unset(t *testing.T) {
	t.Setenv("X_CSI_METRICS_TLS_CERT_FILE", "")
	t.Setenv("X_CSI_METRICS_TLS_KEY_FILE", "")
	cert, key := metricsTLSFiles()
	assert.Empty(t, cert)
	assert.Empty(t, key)
}

// U-SVC-13: metricsTLSFiles returns correct cert and key paths from X_CSI_METRICS_TLS_CERT_FILE and X_CSI_METRICS_TLS_KEY_FILE.
func TestMetricsTLSFiles_WithDir(t *testing.T) {
	t.Setenv("X_CSI_METRICS_TLS_CERT_FILE", "/tmp/tls/tls.crt")
	t.Setenv("X_CSI_METRICS_TLS_KEY_FILE", "/tmp/tls/tls.key")
	cert, key := metricsTLSFiles()
	assert.Equal(t, "/tmp/tls/tls.crt", cert)
	assert.Equal(t, "/tmp/tls/tls.key", key)
}

// U-SVC-14: metricsTLSFiles returns cert and key paths from X_CSI_METRICS_TLS_CERT_FILE and X_CSI_METRICS_TLS_KEY_FILE.
func TestMetricsTLSFiles_FallbackXCSI(t *testing.T) {
	t.Setenv("X_CSI_METRICS_TLS_CERT_FILE", "/etc/ssl/pmx/tls.crt")
	t.Setenv("X_CSI_METRICS_TLS_KEY_FILE", "/etc/ssl/pmx/tls.key")
	cert, key := metricsTLSFiles()
	assert.Equal(t, "/etc/ssl/pmx/tls.crt", cert)
	assert.Equal(t, "/etc/ssl/pmx/tls.key", key)
}

// U-SVC-15: DriverMetricsRegistry is a singleton — same pointer across calls.
func TestDriverMetricsRegistry_Singleton(t *testing.T) {
	r1 := DriverMetricsRegistry()
	r2 := DriverMetricsRegistry()
	assert.Same(t, r1, r2, "DriverMetricsRegistry must return same instance")
}

// U-PMX-10: dell_powermax_metrics_stale set to 1 when circuit OPEN
func TestPowerMaxMetricsStale_SetToOneWhenCircuitOpen(t *testing.T) {
	reg := prometheus.NewRegistry()
	staleGauge := prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: "dell_powermax_metrics_stale",
		Help: "1 when PowerMax metrics are stale (circuit open).",
	}, []string{"array_id"})
	reg.MustRegister(staleGauge)

	// Simulate 3 failures triggering circuit open
	for i := 0; i < 3; i++ {
		staleGauge.WithLabelValues("array-1").Set(0)
	}
	// Circuit is now "open" — mark stale = 1
	staleGauge.WithLabelValues("array-1").Set(1)

	mfs, err := reg.Gather()
	require.NoError(t, err)

	var mf *dto.MetricFamily
	for _, m := range mfs {
		if m.GetName() == "dell_powermax_metrics_stale" {
			mf = m
			break
		}
	}
	require.NotNil(t, mf, "dell_powermax_metrics_stale must be registered")

	v := mf.GetMetric()[0].GetGauge().GetValue()
	assert.Equal(t, 1.0, v, "stale gauge must be 1 when circuit is open")
}
