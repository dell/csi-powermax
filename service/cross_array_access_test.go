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

// Package service: cross_array_access_test.go covers the Cross-Array Volume
// Access invariants that are enforceable at the unit level: the immutable
// volume-to-array binding encoded in the CSI volume handle. Full publish/mount
// flows are exercised by the BDD suite.
package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestVolumeHandleBindsArrayImmutably verifies that the provisioning array
// serial is encoded into the CSI volume handle and recovered verbatim by
// parseCsiID. Because publish/stage resolve the array from the handle (not
// from the node's zone-preferred array), this binding is what makes a volume
// on Array-A reachable from any node in the zone (AC-004).
func TestVolumeHandleBindsArrayImmutably(t *testing.T) {
	s := &service{}
	s.opts.ClusterPrefix = "ABC"

	cases := []struct {
		name    string
		symID   string
		devID   string
		volName string
	}{
		{"array A", "000197900046", "00123", "volA"},
		{"array B", "000197900047", "04567", "volB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			handle := s.createCSIVolumeID("", tc.volName, tc.symID, tc.devID)
			_, arrayID, devID, _, _, err := s.parseCsiID(handle)
			assert.NoError(t, err)
			assert.Equal(t, tc.symID, arrayID, "array serial must round-trip through the handle")
			assert.Equal(t, tc.devID, devID)
		})
	}
}

// TestVolumeHandlesForDifferentArraysAreDistinct ensures two volumes with the
// same name/device on different arrays produce distinct handles bound to their
// respective arrays — the binding is per-array and cannot be confused.
func TestVolumeHandlesForDifferentArraysAreDistinct(t *testing.T) {
	s := &service{}
	s.opts.ClusterPrefix = "ABC"

	hA := s.createCSIVolumeID("", "vol", "000197900046", "00123")
	hB := s.createCSIVolumeID("", "vol", "000197900047", "00123")
	assert.NotEqual(t, hA, hB)

	_, aA, _, _, _, errA := s.parseCsiID(hA)
	_, aB, _, _, _, errB := s.parseCsiID(hB)
	assert.NoError(t, errA)
	assert.NoError(t, errB)
	assert.Equal(t, "000197900046", aA)
	assert.Equal(t, "000197900047", aB)
}

// TestParseCsiIDRejectsMalformedHandle confirms a malformed handle is rejected
// rather than resolved to some arbitrary array (defensive for FR-4.3).
func TestParseCsiIDRejectsMalformedHandle(t *testing.T) {
	s := &service{}
	_, _, _, _, _, err := s.parseCsiID("not-a-valid-handle")
	assert.Error(t, err)
}
