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
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"k8s.io/client-go/tools/record"
)

func TestResolveCapacityPollInterval(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{name: "unset falls back to default", env: "", want: DefaultCapacityPollInterval},
		{name: "invalid falls back to default", env: "not-a-duration", want: DefaultCapacityPollInterval},
		{name: "valid override", env: "90s", want: 90 * time.Second},
		{name: "zero duration falls back to default", env: "0s", want: DefaultCapacityPollInterval},
		{name: "negative duration falls back to default", env: "-1m", want: DefaultCapacityPollInterval},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(EnvCapacityPollInterval, tt.env)
			}
			assert.Equal(t, tt.want, resolveCapacityPollInterval())
		})
	}
}

func TestResolveCapacityThresholdFull(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want float64
	}{
		{name: "unset falls back to default", env: "", want: DefaultCapacityThresholdFull},
		{name: "invalid falls back to default", env: "not-a-number", want: DefaultCapacityThresholdFull},
		{name: "valid override", env: "90", want: 90.0},
		{name: "above 100 clamped to 100", env: "110", want: 100.0},
		{name: "below 0 clamped to 0", env: "-5", want: 0.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv(EnvCapacityThresholdFull, tt.env)
			}
			assert.Equal(t, tt.want, resolveCapacityThresholdFull())
		})
	}
}

// fakeCapacityFetcher is a test double for the pmax-backed capacity fetch
// function, avoiding the need to stand up a mock Unisphere HTTP server for
// pure poller-loop unit tests.
type fakeCapacityFetcher struct {
	results map[string]float64
	errs    map[string]error
}

func (f *fakeCapacityFetcher) fetch(_ context.Context, arrayID string) (float64, error) {
	if err, ok := f.errs[arrayID]; ok {
		return 0, err
	}
	return f.results[arrayID], nil
}

func TestRunCapacityPollCycle_SuccessUpdatesCache(t *testing.T) {
	cache := newCapacityCache()
	fetcher := &fakeCapacityFetcher{results: map[string]float64{"array1": 55.5}}

	runCapacityPollCycle(context.Background(), []string{"array1"}, cache, fetcher.fetch, DefaultCapacityPollInterval, DefaultCapacityThresholdFull, nil)

	utilization, available, _, known := cache.snapshot("array1")
	assert.True(t, known)
	assert.True(t, available)
	assert.Equal(t, 55.5, utilization)
}

func TestRunCapacityPollCycle_FailureMarksUnavailableWhenStale(t *testing.T) {
	cache := newCapacityCache()
	fetcher := &fakeCapacityFetcher{errs: map[string]error{"array1": errors.New("dial tcp: connection refused")}}

	// No prior successful poll, so the first failure marks it unavailable
	// immediately.
	runCapacityPollCycle(context.Background(), []string{"array1"}, cache, fetcher.fetch, DefaultCapacityPollInterval, DefaultCapacityThresholdFull, nil)

	_, available, _, _ := cache.snapshot("array1")
	assert.False(t, available)
}

func TestRunCapacityPollCycle_WarningEventEmittedOnCrossingThreshold(t *testing.T) {
	cache := newCapacityCache()
	fetcher := &fakeCapacityFetcher{results: map[string]float64{"array1": 92.0}}

	var emitted []string
	warn := func(arrayID string, _, _ float64) {
		emitted = append(emitted, arrayID)
	}

	// thresholdFull=100 -> warning fires at >=90.
	runCapacityPollCycle(context.Background(), []string{"array1"}, cache, fetcher.fetch, DefaultCapacityPollInterval, 100.0, warn)
	assert.Equal(t, []string{"array1"}, emitted)

	// A second poll at the same utilization should NOT re-emit (only once
	// per above-threshold streak).
	runCapacityPollCycle(context.Background(), []string{"array1"}, cache, fetcher.fetch, DefaultCapacityPollInterval, 100.0, warn)
	assert.Equal(t, []string{"array1"}, emitted, "should not re-emit while still above threshold")
}

func TestAggregateArrayCapacityUtilization_SumsAcrossSRPs(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockPmaxClient(ctrl)
	arrayID := "000000000001"

	client.EXPECT().GetStoragePoolList(gomock.Any(), arrayID).Return(&types.StoragePoolList{
		StoragePoolIDs: []string{"SRP_1", "SRP_2"},
	}, nil)
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_1").Return(&types.StoragePool{
		SrpCap: &types.SrpCap{UsableTotInTB: 100, UsableUsedInTB: 40},
	}, nil)
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_2").Return(&types.StoragePool{
		SrpCap: &types.SrpCap{UsableTotInTB: 100, UsableUsedInTB: 20},
	}, nil)

	utilization, err := aggregateArrayCapacityUtilization(context.Background(), client, arrayID)
	assert.NoError(t, err)
	assert.InDelta(t, 30.0, utilization, 0.0001, "expected (40+20)/(100+100)*100 = 30%%")
}

func TestAggregateArrayCapacityUtilization_PropagatesListError(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockPmaxClient(ctrl)
	arrayID := "000000000001"

	client.EXPECT().GetStoragePoolList(gomock.Any(), arrayID).Return(nil, errors.New("dial tcp: connection refused"))

	_, err := aggregateArrayCapacityUtilization(context.Background(), client, arrayID)
	assert.Error(t, err)
}

func TestAggregateArrayCapacityUtilization_SkipsPoolWithGetError(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockPmaxClient(ctrl)
	arrayID := "000000000001"

	client.EXPECT().GetStoragePoolList(gomock.Any(), arrayID).Return(&types.StoragePoolList{
		StoragePoolIDs: []string{"SRP_1", "SRP_2"},
	}, nil)
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_1").Return(nil, errors.New("not found"))
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_2").Return(&types.StoragePool{
		SrpCap: &types.SrpCap{UsableTotInTB: 100, UsableUsedInTB: 25},
	}, nil)

	utilization, err := aggregateArrayCapacityUtilization(context.Background(), client, arrayID)
	assert.NoError(t, err)
	assert.InDelta(t, 25.0, utilization, 0.0001, "should skip the errored pool and only aggregate the successful one")
}

func TestAggregateArrayCapacityUtilization_NoPoolsReturnsZeroUtilization(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockPmaxClient(ctrl)
	arrayID := "000000000001"

	client.EXPECT().GetStoragePoolList(gomock.Any(), arrayID).Return(&types.StoragePoolList{StoragePoolIDs: []string{}}, nil)

	utilization, err := aggregateArrayCapacityUtilization(context.Background(), client, arrayID)
	assert.NoError(t, err)
	assert.Equal(t, 0.0, utilization, "should return 0% utilization when no pools are available")
}

func TestAggregateArrayCapacityUtilization_AllPoolsMissingSrpCapReturnsZeroUtilization(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := mocks.NewMockPmaxClient(ctrl)
	arrayID := "000000000001"

	client.EXPECT().GetStoragePoolList(gomock.Any(), arrayID).Return(&types.StoragePoolList{
		StoragePoolIDs: []string{"SRP_1", "SRP_2"},
	}, nil)
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_1").Return(&types.StoragePool{}, nil)
	client.EXPECT().GetStoragePool(gomock.Any(), arrayID, "SRP_2").Return(&types.StoragePool{SrpCap: nil}, nil)

	utilization, err := aggregateArrayCapacityUtilization(context.Background(), client, arrayID)
	assert.NoError(t, err)
	assert.Equal(t, 0.0, utilization, "should return 0% utilization when all pools are missing SrpCap")
}

func TestFetchArrayCapacityUtilization_PropagatesClientResolutionError(t *testing.T) {
	s := &service{opts: Opts{StorageArrays: make(map[string]StorageArrayConfig)}}
	_, err := s.fetchArrayCapacityUtilization(context.Background(), "unregistered-array")
	assert.Error(t, err, "expected an error when no PowerMax client is registered for the array")
}

func TestStartCapacityPoller_SetsUpCacheAndCancelsCleanly(t *testing.T) {
	s := &service{opts: Opts{StorageArrays: make(map[string]StorageArrayConfig)}}
	cancel := s.startCapacityPoller(context.Background(), []string{})

	assert.NotNil(t, s.capCache)
	assert.Equal(t, DefaultCapacityPollInterval, s.capacityPollInterval)
	assert.Equal(t, DefaultCapacityThresholdFull, s.capacityThresholdFull)

	// Cancel should stop the goroutine without panicking or hanging.
	cancel()
}

func TestStartCapacityPoller_NonPositiveIntervalFallsBackToDefault(t *testing.T) {
	t.Setenv(EnvCapacityPollInterval, "0s")
	s := &service{opts: Opts{StorageArrays: make(map[string]StorageArrayConfig)}}
	cancel := s.startCapacityPoller(context.Background(), []string{})

	assert.Equal(t, DefaultCapacityPollInterval, s.capacityPollInterval)
	cancel()
}

func TestEmitCapacityWarningEvent_NoRecorderDoesNotPanic(t *testing.T) {
	origFn := newEventRecorderFunc
	origCached := cachedEventRecorder
	defer func() {
		newEventRecorderFunc = origFn
		eventRecorderOnce = *new(sync.Once)
		cachedEventRecorder = origCached
	}()

	newEventRecorderFunc = func() (record.EventRecorder, error) {
		return nil, errors.New("k8s unavailable")
	}
	eventRecorderOnce = *new(sync.Once)
	cachedEventRecorder = nil

	s := &service{}
	assert.NotPanics(t, func() {
		s.emitCapacityWarningEvent("array1", 95.0, 100.0)
	})
}

func TestEmitCapacityWarningEvent_PostsEventWhenRecorderAvailable(t *testing.T) {
	origFn := newEventRecorderFunc
	origCached := cachedEventRecorder
	defer func() {
		newEventRecorderFunc = origFn
		eventRecorderOnce = *new(sync.Once)
		cachedEventRecorder = origCached
	}()

	fakeRecorder := record.NewFakeRecorder(10)
	newEventRecorderFunc = func() (record.EventRecorder, error) {
		return fakeRecorder, nil
	}
	eventRecorderOnce = *new(sync.Once)
	cachedEventRecorder = nil

	s := &service{}
	s.emitCapacityWarningEvent("array1", 95.0, 100.0)

	select {
	case event := <-fakeRecorder.Events:
		assert.Contains(t, event, "CapacityApproachingFull")
		assert.Contains(t, event, "array1")
	default:
		t.Fatal("expected an event to be recorded")
	}
}

func TestRunCapacityPollCycle_NoWarningBelowThreshold(t *testing.T) {
	cache := newCapacityCache()
	fetcher := &fakeCapacityFetcher{results: map[string]float64{"array1": 50.0}}

	var emitted []string
	warn := func(arrayID string, _, _ float64) {
		emitted = append(emitted, arrayID)
	}

	runCapacityPollCycle(context.Background(), []string{"array1"}, cache, fetcher.fetch, DefaultCapacityPollInterval, 100.0, warn)
	assert.Empty(t, emitted)
}
