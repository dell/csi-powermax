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

package collectors_test

import (
	"context"
	"testing"

	"github.com/dell/csi-powermax/v2/service/collectors"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSRPClient struct {
	srps []collectors.SRPInfo
	err  error
}

func (m *mockSRPClient) GetSRPs(_ context.Context) ([]collectors.SRPInfo, error) {
	return m.srps, m.err
}

// U-PMX-07: 2 SRPs — capacity and utilization metrics emitted per SRP
func TestSRPCollector_Collect_TwoSRPs(t *testing.T) {
	client := &mockSRPClient{
		srps: []collectors.SRPInfo{
			{
				SRPID:                        "SRP_1",
				UsableTotalTB:                100.0,
				UsableUsedTB:                 60.0,
				SubscribedTB:                 80.0,
				SnapshotTB:                   10.0,
				EffectiveUsedCapacityPercent: 60.0,
			},
			{
				SRPID:                        "SRP_2",
				UsableTotalTB:                200.0,
				UsableUsedTB:                 100.0,
				SubscribedTB:                 150.0,
				SnapshotTB:                   20.0,
				EffectiveUsedCapacityPercent: 50.0,
			},
		},
	}

	reg := prometheus.NewRegistry()
	c := collectors.NewSRPCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	require.NoError(t, err)

	mfCap := gatherPMXMetric(t, reg, "dell_powermax_srp_capacity_bytes")
	require.NotNil(t, mfCap, "srp_capacity_bytes must be emitted")

	totalV, ok := gaugePMX(mfCap, map[string]string{"array_id": "array-1", "srp_id": "SRP_1", "capacity_type": "total"})
	require.True(t, ok)
	assert.InDelta(t, 100.0*1099511627776.0, totalV, 0.01)

	usedV, ok := gaugePMX(mfCap, map[string]string{"array_id": "array-1", "srp_id": "SRP_1", "capacity_type": "used"})
	require.True(t, ok)
	assert.InDelta(t, 60.0*1099511627776.0, usedV, 0.01)

	subV, ok := gaugePMX(mfCap, map[string]string{"array_id": "array-1", "srp_id": "SRP_1", "capacity_type": "subscribed"})
	require.True(t, ok)
	assert.InDelta(t, 80.0*1099511627776.0, subV, 0.01)

	snapV, ok := gaugePMX(mfCap, map[string]string{"array_id": "array-1", "srp_id": "SRP_1", "capacity_type": "snapshot"})
	require.True(t, ok)
	assert.InDelta(t, 10.0*1099511627776.0, snapV, 0.01)

	mfUtil := gatherPMXMetric(t, reg, "dell_powermax_srp_utilization_ratio")
	require.NotNil(t, mfUtil, "srp_utilization_ratio must be emitted")

	utilV, ok := gaugePMX(mfUtil, map[string]string{"array_id": "array-1", "srp_id": "SRP_1"})
	require.True(t, ok)
	assert.InDelta(t, 0.60, utilV, 0.01)

	utilV2, ok := gaugePMX(mfUtil, map[string]string{"array_id": "array-1", "srp_id": "SRP_2"})
	require.True(t, ok)
	assert.InDelta(t, 0.50, utilV2, 0.01)
}

// U-PMX-08: API error — collector returns error, no panic
func TestSRPCollector_Collect_APIError(t *testing.T) {
	client := &mockSRPClient{
		err: assert.AnError,
	}

	reg := prometheus.NewRegistry()
	c := collectors.NewSRPCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get SRPs")
}

// U-PMX-09: Empty SRP list — no metrics emitted, no panic
func TestSRPCollector_Collect_EmptySRPs(t *testing.T) {
	client := &mockSRPClient{
		srps: []collectors.SRPInfo{},
	}

	reg := prometheus.NewRegistry()
	c := collectors.NewSRPCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	require.NoError(t, err)
}

// U-PMX-10: Name returns expected string
func TestSRPCollector_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewSRPCollector(nil, reg, "array-1")
	assert.Equal(t, "SRPCollector", c.Name())
}

// U-PMX-11: NewSRPCollector creates collector with correct configuration
func TestSRPCollector_New(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockSRPClient{
		srps: []collectors.SRPInfo{},
	}
	c := collectors.NewSRPCollector(client, reg, "array-1")
	assert.NotNil(t, c)
}

// U-PMX-12: Collect with single SRP
func TestSRPCollector_Collect_SingleSRP(t *testing.T) {
	client := &mockSRPClient{
		srps: []collectors.SRPInfo{
			{
				SRPID:                        "SRP_1",
				UsableTotalTB:                100.0,
				UsableUsedTB:                 60.0,
				SubscribedTB:                 80.0,
				SnapshotTB:                   10.0,
				EffectiveUsedCapacityPercent: 60.0,
			},
		},
	}

	reg := prometheus.NewRegistry()
	c := collectors.NewSRPCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	require.NoError(t, err)
}
