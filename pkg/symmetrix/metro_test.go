/*
 Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package symmetrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
)

func TestMetroClient_healthHandler(t *testing.T) {
	tests := []struct {
		name           string
		failureWeight  int32
		failureCount   int32
		lastFailure    time.Time
		expectedCount  int32
		expectedActive string
	}{
		{
			name:           "failure count below threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  2,
			expectedActive: "primaryArray",
		},
		{
			name:           "failure count at threshold",
			failureWeight:  1,
			failureCount:   failoverThreshold,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  6,
			expectedActive: "primaryArray",
		},
		{
			name:           "failure count above threshold",
			failureWeight:  1,
			failureCount:   failoverThreshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedCount:  7,
			expectedActive: "primaryArray",
		},
		{
			name:           "last failure more than failure time threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-2 * time.Minute),
			expectedCount:  1,
			expectedActive: "primaryArray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    "primaryArray",
				failureCount:   tt.failureCount,
				lastFailure:    tt.lastFailure,
			}

			m.healthHandler(tt.failureWeight)

			if m.failureCount != tt.expectedCount {
				t.Errorf("expected failure count %d, but got %d", tt.expectedCount, m.failureCount)
			}

			if m.activeArray != tt.expectedActive {
				t.Errorf("expected active array %s, but got %s", tt.expectedActive, m.activeArray)
			}
		})
	}
}

func TestTransport_RoundTrip(t *testing.T) {
	type args struct {
		req *http.Request
	}
	tests := []struct {
		name    string
		wantRes *http.Response
		wantErr bool
		err     error
	}{
		{
			name:    "Success",
			wantRes: &http.Response{},
			wantErr: false,
		},
		{
			name:    "Expected error",
			wantRes: &http.Response{},
			err:     errors.New("error"),
			wantErr: true,
		},
		{
			name:    "Response code 401",
			wantRes: &http.Response{StatusCode: 401},
			wantErr: false,
		},
		{
			name:    "Response code 500",
			wantRes: &http.Response{StatusCode: 500},
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    "primaryArray",
				failureCount:   1,
				lastFailure:    time.Now().Add(-1 * time.Minute),
			}
			roundTripper := mocks.NewMockRoundTripperInterface(gomock.NewController(t))
			roundTripper.EXPECT().RoundTrip(&http.Request{}).Return(tt.wantRes, tt.err).AnyTimes()
			tr := &transport{
				roundTripper,
				m.healthHandler,
			}

			gotRes, err := tr.RoundTrip(&http.Request{})
			if (err != nil) != tt.wantErr {
				t.Errorf("RoundTrip() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(gotRes, tt.wantRes) {
				t.Errorf("RoundTrip() = %v, want %v", gotRes, tt.wantRes)
			}
		})
	}
}

func TestMetroClient_getActiveArray(t *testing.T) {
	tests := []struct {
		name           string
		failureWeight  int
		failureCount   int32
		lastFailure    time.Time
		expectedActive string
		activeArray    string
	}{
		{
			name:           "failure count below threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count at threshold",
			failureWeight:  1,
			failureCount:   failoverThreshold,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "secondaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count above threshold",
			failureWeight:  1,
			failureCount:   failoverThreshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "secondaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "last failure more than failure time threshold",
			failureWeight:  1,
			failureCount:   1,
			lastFailure:    time.Now().Add(-2 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "primaryArray",
		},
		{
			name:           "failure count above threshold and active array was secondaryArray",
			failureWeight:  1,
			failureCount:   failoverThreshold + 1,
			lastFailure:    time.Now().Add(-1 * time.Minute),
			expectedActive: "primaryArray",
			activeArray:    "secondaryArray",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &metroClient{
				primaryArray:   "primaryArray",
				secondaryArray: "secondaryArray",
				activeArray:    tt.activeArray,
				failureCount:   tt.failureCount,
				lastFailure:    tt.lastFailure,
			}

			if got := m.getActiveArray(); got != tt.expectedActive {
				t.Errorf("getActiveArray() = %v, want %v", got, tt.expectedActive)
			}
		})
	}
}

func TestCheckMetroState_WinnerFromLocalArray(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Local has WitnessEffective=true; remote responds but has WitnessEffective=false.
	// Expected winner: local (FR-1.2 Witness-first rule).
	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "local-sym",
		WitnessConfigured: true,
		WitnessEffective:  true,
	}, nil)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "remote-sym",
		WitnessConfigured: true,
		WitnessEffective:  false,
	}, nil)

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.WinnerSymID != "local-sym" {
		t.Errorf("expected winner local-sym, got %s", state.WinnerSymID)
	}
	if state.LoserSymID != "remote-sym" {
		t.Errorf("expected loser remote-sym, got %s", state.LoserSymID)
	}
	if !state.WitnessEffective {
		t.Errorf("expected WitnessEffective true")
	}
}

// TestCheckMetroState_BothReachable_RemoteWitnessWins covers FR-1.2: when
// both arrays respond but the Witness has designated the remote (non-preferred
// R2) array as winner, remote must be returned as winner, not local (R1).
func TestCheckMetroState_UsesRemoteRDFGroupNumber(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(&types.RDFGroup{
		WitnessConfigured: true,
		WitnessEffective:  true,
	}, nil)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "20").Return(&types.RDFGroup{
		WitnessConfigured: true,
	}, nil)

	state, err := CheckMetroStateWithRDFGroups(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10", "20")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.WinnerSymID != "local-sym" {
		t.Fatalf("expected local winner, got %s", state.WinnerSymID)
	}
}

func TestCheckMetroState_QueriesSurvivingArrayBeforeOtherQueryTimesOut(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").DoAndReturn(
		func(ctx context.Context, _, _ string) (*types.RDFGroup, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").DoAndReturn(
		func(ctx context.Context, _, _ string) (*types.RDFGroup, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &types.RDFGroup{WitnessConfigured: true, WitnessEffective: true}, nil
		},
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	state, err := CheckMetroState(ctx, localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected surviving remote array to be detected, got %v", err)
	}
	if state.WinnerSymID != "remote-sym" {
		t.Fatalf("expected remote winner, got %s", state.WinnerSymID)
	}
}

func TestCheckMetroState_BothReachable_RemoteWitnessWins(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Split-brain scenario: both arrays are reachable, but the Witness has
	// designated the remote (R2) side as winner.
	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "local-sym",
		WitnessConfigured: true,
		WitnessEffective:  false, // local is NOT the Witness winner
	}, nil)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "remote-sym",
		WitnessConfigured: true,
		WitnessEffective:  true, // remote IS the Witness winner
	}, nil)

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.WinnerSymID != "remote-sym" {
		t.Errorf("expected Witness-designated winner remote-sym, got %s", state.WinnerSymID)
	}
	if state.LoserSymID != "local-sym" {
		t.Errorf("expected loser local-sym, got %s", state.LoserSymID)
	}
	if !state.WitnessEffective {
		t.Errorf("expected WitnessEffective true on the winning side")
	}
}

// TestCheckMetroState_BothReachable_NoArbitration covers the normal
// active-active state where neither array has Witness/Bias effective.
// In this case local (R1/preferred) is returned as the serving side.
func TestCheckMetroState_BothReachable_NoArbitration(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "local-sym",
		WitnessConfigured: false,
		WitnessEffective:  false,
	}, nil)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "remote-sym",
		WitnessConfigured: false,
		WitnessEffective:  false,
	}, nil)

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	// Normal active-active: local is default serving side.
	if state.WinnerSymID != "local-sym" {
		t.Errorf("expected local-sym as default serving side, got %s", state.WinnerSymID)
	}
}

func TestCheckMetroState_WinnerFromRemoteArray_NonPreferredSide(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Local array (R1/preferred side) is unreachable; the Witness has
	// designated the remote (non-preferred, R2) array as the winner.
	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(nil, errors.New("connection refused"))
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity:    "local-sym",
		WitnessConfigured: true,
		WitnessEffective:  true,
	}, nil)

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.WinnerSymID != "remote-sym" {
		t.Errorf("expected winner remote-sym (non-preferred side), got %s", state.WinnerSymID)
	}
	if state.LoserSymID != "local-sym" {
		t.Errorf("expected loser local-sym, got %s", state.LoserSymID)
	}
}

func TestCheckMetroState_BothArraysUnreachable(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(nil, errors.New("timeout"))
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(nil, errors.New("connection refused"))

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err == nil {
		t.Fatalf("expected an error when both arrays are unreachable, got nil")
	}
	if !errors.Is(err, ErrBothArraysUnreachable) {
		t.Errorf("expected ErrBothArraysUnreachable, got %v", err)
	}
	if state != nil {
		t.Errorf("expected nil state when both arrays are unreachable, got %+v", state)
	}
}

func TestCheckMetroState_DeviceBiasR1Failure(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Device Bias (no Witness) configuration; the R1 (local) side has
	// failed, so only the remote (R2) array responds. Under Device
	// Bias there is no automatic failover, so this must be flagged.
	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(nil, errors.New("connection refused"))
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity: "local-sym",
		BiasConfigured: true,
		BiasEffective:  false,
	}, nil)

	_, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err == nil {
		t.Fatal("expected Device Bias R1-side failure to return a hard error")
	}
	if !errors.Is(err, ErrDeviceBiasR1Failure) {
		t.Errorf("expected ErrDeviceBiasR1Failure, got %v", err)
	}
}

func TestCheckMetroState_DeviceBiasR2Failure_NoHostAccessLoss(t *testing.T) {
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Device Bias configuration; the R2 (remote) side has failed. The R1
	// (bias winner) side continues serving normally -- no host access
	// loss, so DeviceBiasR1Failure must remain false.
	// Remote is unreachable (R2 site failure).
	localClient.EXPECT().GetRDFGroupByID(gomock.Any(), "local-sym", "10").Return(&types.RDFGroup{
		DevicePolarity: "local-sym",
		BiasConfigured: true,
		BiasEffective:  true,
	}, nil)
	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(nil, errors.New("connection refused"))

	state, err := CheckMetroState(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.DeviceBiasR1Failure {
		t.Errorf("expected DeviceBiasR1Failure false when R1 side is still the winner")
	}
}

func TestCheckMetroState_NilClientTreatedAsUnreachable(t *testing.T) {
	ctrl := gomock.NewController(t)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	remoteClient.EXPECT().GetRDFGroupByID(gomock.Any(), "remote-sym", "10").Return(&types.RDFGroup{
		DevicePolarity: "remote-sym",
	}, nil)

	state, err := CheckMetroState(context.Background(), nil, remoteClient, "local-sym", "remote-sym", "10")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if state.WinnerSymID != "remote-sym" {
		t.Errorf("expected winner remote-sym when local client is nil, got %s", state.WinnerSymID)
	}
}

// ─── resolveRDFGroupNo ────────────────────────────────────────────────────────

func TestResolveRDFGroupNo_AlreadyProvided(t *testing.T) {
	// When a non-empty rdfGroupNo is supplied, no API call should occur.
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	// No expectations — GetRDFGroupList must NOT be called.
	result := resolveRDFGroupNo(context.Background(), localClient, nil, "local-sym", "remote-sym", "14")
	if result != "14" {
		t.Errorf("expected '14', got %q", result)
	}
}

func TestResolveRDFGroupNo_ResolvedFromLocalList(t *testing.T) {
	ResetRDFGroupCache()
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym", gomock.Any()).Return(&types.RDFGroupList{
		RDFGroupIDs: []types.RDFGroupIDL{
			{RDFGNumber: 10, GroupType: "Async"},
			{RDFGNumber: 14, GroupType: "Metro"},
		},
	}, nil)

	result := resolveRDFGroupNo(context.Background(), localClient, nil, "local-sym", "remote-sym", "")
	if result != "14" {
		t.Errorf("expected '14', got %q", result)
	}
}

func TestResolveRDFGroupNo_LocalListError_FallsBackToRemote(t *testing.T) {
	ResetRDFGroupCache()
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	localClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym", gomock.Any()).Return(nil, fmt.Errorf("timeout"))
	remoteClient.EXPECT().GetRDFGroupList(gomock.Any(), "remote-sym", gomock.Any()).Return(&types.RDFGroupList{
		RDFGroupIDs: []types.RDFGroupIDL{
			{RDFGNumber: 20, GroupType: "METRO"},
		},
	}, nil)

	result := resolveRDFGroupNo(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "")
	if result != "20" {
		t.Errorf("expected '20', got %q", result)
	}
}

func TestResolveRDFGroupNo_NoMetroGroup_ReturnsEmpty(t *testing.T) {
	ResetRDFGroupCache()
	ctrl := gomock.NewController(t)
	localClient := mocks.NewMockPmaxClient(ctrl)
	remoteClient := mocks.NewMockPmaxClient(ctrl)

	// Neither array has a Metro group.
	localClient.EXPECT().GetRDFGroupList(gomock.Any(), "local-sym", gomock.Any()).Return(&types.RDFGroupList{
		RDFGroupIDs: []types.RDFGroupIDL{
			{RDFGNumber: 10, GroupType: "Async"},
		},
	}, nil)
	remoteClient.EXPECT().GetRDFGroupList(gomock.Any(), "remote-sym", gomock.Any()).Return(&types.RDFGroupList{
		RDFGroupIDs: []types.RDFGroupIDL{},
	}, nil)

	result := resolveRDFGroupNo(context.Background(), localClient, remoteClient, "local-sym", "remote-sym", "")
	if result != "" {
		t.Errorf("expected empty string, got %q", result)
	}
}

func TestResolveRDFGroupNo_NilClients_ReturnsEmpty(t *testing.T) {
	ResetRDFGroupCache()
	result := resolveRDFGroupNo(context.Background(), nil, nil, "local-sym", "remote-sym", "")
	if result != "" {
		t.Errorf("expected empty string for nil clients, got %q", result)
	}
}

// ─── MetroStateCache ──────────────────────────────────────────────────────────

func TestMetroStateCache_New_DefaultTTL(t *testing.T) {
	c := NewMetroStateCache(0)
	if c == nil {
		t.Fatal("expected non-nil cache")
	}
	if c.ttl != MetroStateCacheTTL {
		t.Errorf("expected default TTL %v, got %v", MetroStateCacheTTL, c.ttl)
	}
}

func TestMetroStateCache_New_CustomTTL(t *testing.T) {
	c := NewMetroStateCache(10 * time.Second)
	if c.ttl != 10*time.Second {
		t.Errorf("expected TTL 10s, got %v", c.ttl)
	}
}

func TestMetroStateCache_Get_Miss(t *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	state, hit, err := c.Get("A", "B")
	if hit {
		t.Error("expected cache miss on empty cache")
	}
	if state != nil {
		t.Error("expected nil state on miss")
	}
	if err != nil {
		t.Error("expected nil err on miss")
	}
}

func TestMetroStateCache_Put_And_Get_Hit(t *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	want := &MetroState{WinnerSymID: "A", LoserSymID: "B"}
	c.Put("A", "B", want, nil)

	got, hit, err := c.Get("A", "B")
	if !hit {
		t.Error("expected cache hit after Put")
	}
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if got != want {
		t.Errorf("expected cached state %+v, got %+v", want, got)
	}
}

func TestMetroStateCache_PutError_And_Get_ReturnsError(t *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	wantErr := errors.New("both arrays unreachable")
	c.PutError("A", "B", wantErr)

	gotState, hit, gotErr := c.Get("A", "B")
	if !hit {
		t.Error("expected cache hit for error entry")
	}
	if !errors.Is(gotErr, wantErr) {
		t.Errorf("expected cached error %v, got %v", wantErr, gotErr)
	}
	if gotState != nil {
		t.Error("expected nil state for error entry")
	}
}

func TestMetroStateCache_PutError_OverwrittenByPut(t *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	c.PutError("A", "B", errors.New("transient error"))

	want := &MetroState{WinnerSymID: "A"}
	c.Put("A", "B", want, nil)

	got, hit, err := c.Get("A", "B")
	if !hit {
		t.Error("expected cache hit after Put overwrites PutError")
	}
	if err != nil {
		t.Errorf("expected nil err after Put, got %v", err)
	}
	if got != want {
		t.Error("expected new state to replace error entry")
	}
}

func TestMetroStateCache_Invalidate_ClearsEntry(t *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	c.Put("A", "B", &MetroState{WinnerSymID: "A"}, nil)

	c.Invalidate("A", "B")

	_, hit, _ := c.Get("A", "B")
	if hit {
		t.Error("expected cache miss after Invalidate")
	}
}

func TestMetroStateCache_Invalidate_NonExistentKey_NoOp(_ *testing.T) {
	c := NewMetroStateCache(30 * time.Second)
	// Must not panic when key does not exist.
	c.Invalidate("X", "Y")
}

func TestMetroStateCache_Get_ExpiredEntry_ReturnsMiss(t *testing.T) {
	c := NewMetroStateCache(1 * time.Millisecond)
	c.Put("A", "B", &MetroState{WinnerSymID: "A"}, nil)
	time.Sleep(10 * time.Millisecond)

	_, hit, _ := c.Get("A", "B")
	if hit {
		t.Error("expected cache miss after TTL expiry")
	}
}

func TestGetMetroPartner(t *testing.T) {
	metroClients.Store("partner-primary-secondary", &metroClient{primaryArray: "partner-primary", secondaryArray: "partner-secondary"})
	defer metroClients.Delete("partner-primary-secondary")

	if got := GetMetroPartner("partner-primary"); got != "partner-secondary" {
		t.Fatalf("primary partner = %q, want partner-secondary", got)
	}
	if got := GetMetroPartner("partner-secondary"); got != "partner-primary" {
		t.Fatalf("secondary partner = %q, want partner-primary", got)
	}
	if got := GetMetroPartner("unknown-array"); got != "" {
		t.Fatalf("unknown partner = %q, want empty", got)
	}
}

// ─── setWinner / SetMetroWinner ───────────────────────────────────────────────

func TestMetroClient_setWinner_SetsAndClears(t *testing.T) {
	m := &metroClient{
		primaryArray:   "A",
		secondaryArray: "B",
		activeArray:    "A",
	}
	m.setWinner("B")
	if m.witnessWinner != "B" {
		t.Errorf("expected witnessWinner B, got %q", m.witnessWinner)
	}
	m.setWinner("")
	if m.witnessWinner != "" {
		t.Errorf("expected empty witnessWinner after clear, got %q", m.witnessWinner)
	}
}

func TestMetroClient_getActiveArray_WitnessWinnerOverridesFailCount(t *testing.T) {
	// Even with failureCount below failoverThreshold, witnessWinner takes priority.
	m := &metroClient{
		primaryArray:   "A",
		secondaryArray: "B",
		activeArray:    "A",
		witnessWinner:  "B",
		failureCount:   0,
	}
	got := m.getActiveArray()
	if got != "B" {
		t.Errorf("expected Witness winner B, got %q", got)
	}
	// activeArray should now be updated to the witness winner.
	if m.activeArray != "B" {
		t.Errorf("expected activeArray B after witness override, got %q", m.activeArray)
	}
}

func TestMetroClient_getActiveArray_WitnessWinnerAlreadyActive_NoLog(t *testing.T) {
	// witnessWinner == activeArray: no routing change, just returns current.
	m := &metroClient{
		primaryArray:   "A",
		secondaryArray: "B",
		activeArray:    "B",
		witnessWinner:  "B",
	}
	got := m.getActiveArray()
	if got != "B" {
		t.Errorf("expected B, got %q", got)
	}
}

func TestMetroClient_getActiveArray_WitnessWinnerInvalidIgnored(t *testing.T) {
	// witnessWinner not in primary/secondary pair: falls back to legacy path.
	m := &metroClient{
		primaryArray:   "A",
		secondaryArray: "B",
		activeArray:    "A",
		witnessWinner:  "C", // unknown array
		failureCount:   0,
	}
	got := m.getActiveArray()
	// Legacy path: failureCount < threshold → returns current activeArray.
	if got != "A" {
		t.Errorf("expected A (legacy path), got %q", got)
	}
}

func TestSetMetroWinner_ForwardOrder(t *testing.T) {
	m := &metroClient{primaryArray: "A", secondaryArray: "B", activeArray: "A"}
	metroClients.Store("A-B", m)
	defer metroClients.Delete("A-B")

	SetMetroWinner("A", "B", "B")
	if m.witnessWinner != "B" {
		t.Errorf("expected witnessWinner B, got %q", m.witnessWinner)
	}
}

func TestSetMetroWinner_ReverseOrder(t *testing.T) {
	m := &metroClient{primaryArray: "A", secondaryArray: "B", activeArray: "A"}
	metroClients.Store("A-B", m)
	defer metroClients.Delete("A-B")

	// Caller passes reversed order — should still find the client via reversed lookup.
	SetMetroWinner("B", "A", "A")
	if m.witnessWinner != "A" {
		t.Errorf("expected witnessWinner A, got %q", m.witnessWinner)
	}
}

func TestSetMetroWinner_NotFound_NoOp(_ *testing.T) {
	// No metroClient registered for this pair — must not panic.
	SetMetroWinner("X", "Y", "X")
}

func TestSetMetroWinner_ClearWinner(t *testing.T) {
	m := &metroClient{primaryArray: "A", secondaryArray: "B", activeArray: "A", witnessWinner: "B"}
	metroClients.Store("A-B", m)
	defer metroClients.Delete("A-B")

	SetMetroWinner("A", "B", "") // clear
	if m.witnessWinner != "" {
		t.Errorf("expected empty witnessWinner after clear, got %q", m.witnessWinner)
	}
}
