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
	"context"
	"strings"
	"testing"

	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestParseCSIAddonsReplicationParams(t *testing.T) {
	tests := []struct {
		name     string
		params   map[string]string
		wantCode codes.Code
		validate func(t *testing.T, p *csiAddonsReplicationParams)
	}{
		{
			name:     "nil parameters rejected",
			params:   nil,
			wantCode: codes.InvalidArgument,
		},
		{
			name: "missing remote system rejected",
			params: map[string]string{
				CSIAddonsParamReplicationMode: "SYNC",
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "missing mode rejected",
			params: map[string]string{
				CSIAddonsParamRemoteSystem: "000000000002",
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "metro mode rejected with canonical message",
			params: map[string]string{
				CSIAddonsParamRemoteSystem:    "000000000002",
				CSIAddonsParamReplicationMode: "METRO",
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "unknown mode rejected",
			params: map[string]string{
				CSIAddonsParamRemoteSystem:    "000000000002",
				CSIAddonsParamReplicationMode: "ACTIVE",
			},
			wantCode: codes.InvalidArgument,
		},
		{
			name: "sync mode accepted with defaults",
			params: map[string]string{
				CSIAddonsParamRemoteSystem:    "000000000002",
				CSIAddonsParamReplicationMode: "sync",
			},
			validate: func(t *testing.T, p *csiAddonsReplicationParams) {
				assert.Equal(t, "000000000002", p.remoteSymID)
				assert.Equal(t, Sync, p.replicationMode)
				assert.Equal(t, defaultVolumeGroupPrefix, p.volumeGroupPrefix)
			},
		},
		{
			name: "target array alias accepted in place of remote system",
			params: map[string]string{
				CSIAddonsParamTargetArrayID:   "000000000002",
				CSIAddonsParamReplicationMode: "ASYNC",
			},
			validate: func(t *testing.T, p *csiAddonsReplicationParams) {
				assert.Equal(t, "000000000002", p.remoteSymID)
				assert.Equal(t, Async, p.replicationMode)
			},
		},
		{
			name: "all optional fields parsed",
			params: map[string]string{
				CSIAddonsParamRemoteSystem:      "000000000002",
				CSIAddonsParamReplicationMode:   "ASYNC",
				CSIAddonsParamRdfGroupNumber:    "42",
				CSIAddonsParamVolumeGroupPrefix: "vg-prod",
				CSIAddonsParamNamespace:         "default",
			},
			validate: func(t *testing.T, p *csiAddonsReplicationParams) {
				assert.Equal(t, "42", p.rdfGroupNumber)
				assert.Equal(t, "vg-prod", p.volumeGroupPrefix)
				assert.Equal(t, "default", p.namespace)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCSIAddonsReplicationParams(tt.params)
			if tt.wantCode != codes.OK && tt.wantCode != 0 {
				assert.Error(t, err)
				assert.Equal(t, tt.wantCode, status.Code(err))
				assert.Nil(t, got)
				if tt.wantCode == codes.InvalidArgument && tt.params[CSIAddonsParamReplicationMode] == "METRO" {
					assert.Contains(t, err.Error(), metroErrorMessage)
				}
				return
			}
			assert.NoError(t, err)
			assert.NotNil(t, got)
			if tt.validate != nil {
				tt.validate(t, got)
			}
		})
	}
}

func TestBuildCSIAddonsSGName(t *testing.T) {
	got := buildCSIAddonsSGName("vg-prefix", "prod", "1", Sync)
	assert.Equal(t, CsiRepSGPrefix+"addons-vg-prefix-prod-1-SYNC", got)

	// Empty prefix defaults to defaultVolumeGroupPrefix.
	got = buildCSIAddonsSGName("", "", "5", Async)
	assert.Equal(t, CsiRepSGPrefix+"addons-"+defaultVolumeGroupPrefix+"-default-5-ASYNC", got)

	// Long names: variable parts (vgPrefix + namespace) are truncated so that
	// the total SG name stays within MaxStorageGroupNameLength (64).
	// The fixed suffix (-rdfGrpNo-mode) is never truncated.
	veryLongNS := "this-is-a-very-long-namespace-that-exceeds-limits"
	gotLong := buildCSIAddonsSGName("vg", veryLongNS, "5", Async)
	assert.LessOrEqual(t, len(gotLong), MaxStorageGroupNameLength)
	// The suffix must always end with the unmodified rdfGrpNo and mode.
	assert.True(t, strings.HasSuffix(gotLong, "-5-ASYNC"))
	// The prefix must always start with the managed SG prefix.
	assert.True(t, strings.HasPrefix(gotLong, csiAddonsManagedSGPrefix))
}

func TestParseCSIAddonsSGName(t *testing.T) {
	// Round-trip a name produced by buildCSIAddonsSGName, including a
	// prefix and namespace that themselves contain dashes.
	name := buildCSIAddonsSGName("vg-prefix-with-dashes", "ns-1", "12", Async)
	rdfGrpNo, mode, ok := parseCSIAddonsSGName(name)
	assert.True(t, ok)
	assert.Equal(t, "12", rdfGrpNo)
	assert.Equal(t, Async, mode)

	// Round-trip with a truncated name to verify parse still works.
	longName := buildCSIAddonsSGName("long-prefix", "very-long-namespace-that-forces-truncation-of-variable-parts", "99", Sync)
	assert.LessOrEqual(t, len(longName), MaxStorageGroupNameLength)
	rdfGrpNo, mode, ok = parseCSIAddonsSGName(longName)
	assert.True(t, ok)
	assert.Equal(t, "99", rdfGrpNo)
	assert.Equal(t, Sync, mode)

	// Non CSI-Addons SG names are rejected.
	_, _, ok = parseCSIAddonsSGName("csi-rep-sg-some-other-sg")
	assert.False(t, ok)
	_, _, ok = parseCSIAddonsSGName("application-sg")
	assert.False(t, ok)
}

func TestEncodeDecodeVolumeGroupID(t *testing.T) {
	id := encodeVolumeGroupID("000000000001", "csi-rep-sg-addons-vg-x-2-ASYNC", "2")
	assert.Equal(t, "000000000001:csi-rep-sg-addons-vg-x-2-ASYNC:2", id)

	sym, sg, rdf, err := decodeVolumeGroupID(id)
	assert.NoError(t, err)
	assert.Equal(t, "000000000001", sym)
	assert.Equal(t, "csi-rep-sg-addons-vg-x-2-ASYNC", sg)
	assert.Equal(t, "2", rdf)
}

func TestDecodeVolumeGroupID_Errors(t *testing.T) {
	cases := []string{
		"",
		"only-one-part",
		"two:parts",
		":missing:source",
		"sym::5",
	}
	for _, c := range cases {
		_, _, _, err := decodeVolumeGroupID(c)
		assert.Error(t, err, "id=%q", c)
		assert.Equal(t, codes.InvalidArgument, status.Code(err))
	}
}

func TestCSIAddonsSRDFPersonality(t *testing.T) {
	tests := []struct {
		name           string
		in             *types.StorageGroupRDFG
		wantState      string
		wantIsR1       bool
		wantMixedPers  bool
		wantMixedState bool
	}{
		{
			name: "nil",
			in:   nil,
		},
		{
			name: "uniform R1 consistent",
			in: &types.StorageGroupRDFG{
				VolumeRdfTypes: []string{"R1", "R1"},
				States:         []string{Consistent, Consistent},
			},
			wantState: Consistent,
			wantIsR1:  true,
		},
		{
			name: "uniform R2 synchronized",
			in: &types.StorageGroupRDFG{
				VolumeRdfTypes: []string{"R2", "R2"},
				States:         []string{Synchronized},
			},
			wantState: Synchronized,
			wantIsR1:  false,
		},
		{
			name: "mixed personalities flagged",
			in: &types.StorageGroupRDFG{
				VolumeRdfTypes: []string{"R1", "R2"},
				States:         []string{Consistent, Consistent},
			},
			// Detection scans all devices and is order-independent: when
			// personalities are mixed, isR1 is false (not "first wins") and
			// the mixedPersonalities flag is set.
			wantState:     Consistent,
			wantIsR1:      false,
			wantMixedPers: true,
		},
		{
			// Order-independence: R2 appearing before R1 must still be
			// reported as mixed with isR1=false.
			name: "mixed personalities R2 first",
			in: &types.StorageGroupRDFG{
				VolumeRdfTypes: []string{"R2", "R1"},
				States:         []string{Consistent, Consistent},
			},
			wantState:     Consistent,
			wantIsR1:      false,
			wantMixedPers: true,
		},
		{
			name: "mixed states flagged",
			in: &types.StorageGroupRDFG{
				VolumeRdfTypes: []string{"R1", "R1"},
				States:         []string{Consistent, Suspended},
			},
			wantState:      Consistent,
			wantIsR1:       true,
			wantMixedState: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, isR1, mPers, mStates := csiAddonsSRDFPersonality(tt.in)
			assert.Equal(t, tt.wantState, state)
			assert.Equal(t, tt.wantIsR1, isR1)
			assert.Equal(t, tt.wantMixedPers, mPers)
			assert.Equal(t, tt.wantMixedState, mStates)
		})
	}
}

func TestResolveEffectiveRDFGroup(t *testing.T) {
	// Explicit group always wins.
	got, err := resolveEffectiveRDFGroup("7", &types.RDFStorageGroup{RDFGroups: []int{5, 9}})
	assert.NoError(t, err)
	assert.Equal(t, "7", got)

	// Single group on the SG is used when none supplied.
	got, err = resolveEffectiveRDFGroup("", &types.RDFStorageGroup{RDFGroups: []int{5}})
	assert.NoError(t, err)
	assert.Equal(t, "5", got)

	// Ambiguous: SG spans multiple RDF groups and none was supplied.
	_, err = resolveEffectiveRDFGroup("", &types.RDFStorageGroup{RDFGroups: []int{5, 9}})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// No group available at all.
	_, err = resolveEffectiveRDFGroup("", &types.RDFStorageGroup{})
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
}

func TestIsCSIAddonsManagedSG(t *testing.T) {
	assert.True(t, isCSIAddonsManagedSG(csiAddonsManagedSGPrefix+"vg-ns1-5-"+Sync))
	assert.False(t, isCSIAddonsManagedSG("csi-rep-sg-some-other"))
	assert.False(t, isCSIAddonsManagedSG("application-sg"))
}

func TestSummarizeSRDFState(t *testing.T) {
	assert.Equal(t, "unknown", summarizeSRDFState(nil))

	r1Sync := &types.StorageGroupRDFG{
		VolumeRdfTypes: []string{"R1"},
		States:         []string{Synchronized},
	}
	assert.Contains(t, summarizeSRDFState(r1Sync), "personality=R1")
	assert.Contains(t, summarizeSRDFState(r1Sync), "state="+Synchronized)

	mixed := &types.StorageGroupRDFG{
		VolumeRdfTypes: []string{"R1", "R2"},
		States:         []string{Consistent, Suspended},
	}
	assert.Contains(t, summarizeSRDFState(mixed), "personality=mixed")
}

func TestExtractSymIDFromVolumeID(t *testing.T) {
	svc := &service{}

	// Empty volume ID returns error.
	_, err := svc.extractSymIDFromVolumeID("")
	assert.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Valid CSI volume ID returns the array ID component.
	symID, err := svc.extractSymIDFromVolumeID("volname-000120000548-011AB")
	assert.NoError(t, err)
	assert.Equal(t, "000120000548", symID)
}

func TestExtractDevIDFromVolumeID(t *testing.T) {
	svc := &service{}

	// Empty volume ID returns error.
	_, err := svc.extractDevIDFromVolumeID("")
	assert.Error(t, err)
	st, _ := status.FromError(err)
	assert.Equal(t, codes.InvalidArgument, st.Code())

	// Malformed volume ID (parseCsiID fails) returns error.
	_, err = svc.extractDevIDFromVolumeID("bad")
	assert.Error(t, err)

	// Valid CSI volume ID returns the device ID component.
	devID, err := svc.extractDevIDFromVolumeID("volname-000120000548-011AB")
	assert.NoError(t, err)
	assert.Equal(t, "011AB", devID)
}

func TestCsiAddonsReqID_ContextWithoutKey(t *testing.T) {
	// context.Background() carries no RequestIDKey → must return empty string.
	id := csiAddonsReqID(context.Background())
	assert.Equal(t, "", id)
}

func TestParseCSIAddonsSGName_EdgeCases(t *testing.T) {
	prefix := csiAddonsManagedSGPrefix

	// Wrong prefix → not ok.
	_, _, ok := parseCSIAddonsSGName("other-sg-name")
	assert.False(t, ok)

	// Name ends with trailing dash → mode is empty → not ok.
	_, _, ok = parseCSIAddonsSGName(prefix)
	assert.False(t, ok)

	// Valid name parses correctly.
	name := prefix + "ns-14-ASYNC"
	rdf, mode, ok := parseCSIAddonsSGName(name)
	assert.True(t, ok)
	assert.Equal(t, "14", rdf)
	assert.Equal(t, "ASYNC", mode)
}

func TestCsiAddonsSRDFPersonality_EmptySliceEntries(t *testing.T) {
	// Empty rdfType strings should be skipped without panic.
	psg := &types.StorageGroupRDFG{
		VolumeRdfTypes: []string{"", "R1"},
		States:         []string{"", Synchronized},
	}
	state, isR1, mixedPersonalities, mixedStates := csiAddonsSRDFPersonality(psg)
	assert.Equal(t, Synchronized, state)
	assert.True(t, isR1)
	assert.False(t, mixedPersonalities)
	assert.False(t, mixedStates)
}
