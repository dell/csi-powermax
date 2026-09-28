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

type mockMVClient struct {
	views []collectors.MaskingViewInfo
	err   error
}

func (m *mockMVClient) GetMaskingViews(_ context.Context) ([]collectors.MaskingViewInfo, error) {
	return m.views, m.err
}

type mockMVClientErr struct{ err error }

func (m *mockMVClientErr) GetMaskingViews(_ context.Context) ([]collectors.MaskingViewInfo, error) {
	return nil, m.err
}

// U-PMX-07: 3 masking views — masking_view_total=3; per-view connectivity gauge
func TestMaskingViewCollector_Collect_ThreeViews(t *testing.T) {
	client := &mockMVClient{
		views: []collectors.MaskingViewInfo{
			{MaskingViewID: "mv-1", StorageGroupID: "sg-1", Connected: true},
			{MaskingViewID: "mv-2", StorageGroupID: "sg-1", Connected: false},
			{MaskingViewID: "mv-3", StorageGroupID: "sg-2", Connected: true},
		},
	}

	reg := prometheus.NewRegistry()
	c := collectors.NewMaskingViewCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	require.NoError(t, err)

	mfTotal := gatherPMXMetric(t, reg, "dell_powermax_maskingview_total")
	require.NotNil(t, mfTotal, "masking_view_total must be emitted")
	totalVal := mfTotal.GetMetric()[0].GetGauge().GetValue()
	assert.Equal(t, 3.0, totalVal, "3 masking views → total = 3")

	mfConn := gatherPMXMetric(t, reg, "dell_powermax_maskingview_healthy")
	require.NotNil(t, mfConn)

	mv1, ok := gaugePMX(mfConn, map[string]string{"array_id": "array-1", "masking_view": "mv-1"})
	require.True(t, ok)
	assert.Equal(t, 1.0, mv1, "mv-1 is connected")

	mv2, ok := gaugePMX(mfConn, map[string]string{"array_id": "array-1", "masking_view": "mv-2"})
	require.True(t, ok)
	assert.Equal(t, 0.0, mv2, "mv-2 is disconnected")
}

// U-MV-02: API error returns wrapped error.
func TestMaskingViewCollector_APIError(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewMaskingViewCollector(&mockMVClientErr{err: assert.AnError}, reg, "array-1")
	err := c.Collect(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "MaskingViewCollector")
}

// U-MV-05: Name() returns "MaskingViewCollector".
func TestMaskingViewCollector_Name(t *testing.T) {
	reg := prometheus.NewRegistry()
	c := collectors.NewMaskingViewCollector(nil, reg, "array-1")
	assert.Equal(t, "MaskingViewCollector", c.Name())
}

// U-MV-06: NewMaskingViewCollector creates collector with correct configuration
func TestMaskingViewCollector_New(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockMVClient{
		views: []collectors.MaskingViewInfo{},
	}
	c := collectors.NewMaskingViewCollector(client, reg, "array-1")
	assert.NotNil(t, c)
}

// U-MV-07: Collect with empty list returns no error
func TestMaskingViewCollector_Collect_Empty(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockMVClient{
		views: []collectors.MaskingViewInfo{},
	}
	c := collectors.NewMaskingViewCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}

// U-MV-08: Collect with single masking view
func TestMaskingViewCollector_Collect_SingleView(t *testing.T) {
	reg := prometheus.NewRegistry()
	client := &mockMVClient{
		views: []collectors.MaskingViewInfo{
			{MaskingViewID: "mv-1", StorageGroupID: "sg-1", Connected: true},
		},
	}
	c := collectors.NewMaskingViewCollector(client, reg, "array-1")

	err := c.Collect(context.Background())
	assert.NoError(t, err)
}
