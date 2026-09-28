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
	"context"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"
)

// Test that registerOrGetGaugeVec works correctly through NewSRPCollector
func TestRegisterOrGetGaugeVec_NewRegistration(t *testing.T) {
	reg := prometheus.NewRegistry()

	// Since registerOrGetGaugeVec is unexported, we test it indirectly through collectors that use it
	collector := NewSRPCollector(&mockSRPClientForRegistry{}, reg, "array1")
	require.NotNil(t, collector)
}

// Test that registerOrGetGaugeVec handles AlreadyRegisteredError correctly
func TestRegisterOrGetGaugeVec_AlreadyRegistered(t *testing.T) {
	reg := prometheus.NewRegistry()

	// Create two collectors with the same registry - they should share metrics via registerOrGetGaugeVec
	collector1 := NewSRPCollector(&mockSRPClientForRegistry{}, reg, "array1")
	collector2 := NewSRPCollector(&mockSRPClientForRegistry{}, reg, "array1")

	require.NotNil(t, collector1)
	require.NotNil(t, collector2)

	// Both collectors should work without panic
	err1 := collector1.Collect(context.Background())
	err2 := collector2.Collect(context.Background())

	// Both should succeed (or fail with same error)
	if err1 != nil {
		require.Equal(t, err1.Error(), err2.Error())
	}
}

type mockSRPClientForRegistry struct{}

func (m *mockSRPClientForRegistry) GetSRPs(_ context.Context) ([]SRPInfo, error) {
	return []SRPInfo{}, nil
}
