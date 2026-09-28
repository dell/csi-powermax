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
	"fmt"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/cucumber/godog"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
)

// multiArrayZoneState holds scenario state for the multi-array zone
// acceptance and capacity-based selection BDD scenarios
// (service/features/multi_array_zone_acceptance.feature). These scenarios
// exercise the driver's array-selection business logic directly (secret
// parsing, capacity cache, selectArray, error classification) rather than
// standing up a full mock Unisphere HTTP server, consistent with the
// scoped-verification style used by other lightweight service-level
// feature files in this codebase.
type multiArrayZoneState struct {
	svc          *service
	secretParams *viper.Viper

	didPanic bool

	selectedArrayID string
	selectErr       error

	provisioningErr   error
	classifiedAsConn  bool
	classificationSet bool

	provisionedArrayID string
	symidErr           error

	// FR-3 metrics / FR-4 cross-array access
	metricsReg        *prometheus.Registry
	publishResolvedID string
	handleResolveErr  error
	volumeHandle      string
}

func (m *multiArrayZoneState) reset() {
	m.svc = &service{
		opts:     Opts{StorageArrays: make(map[string]StorageArrayConfig)},
		capCache: newCapacityCache(),
	}
	m.secretParams = viper.New()
	m.didPanic = false
	m.selectedArrayID = ""
	m.selectErr = nil
	m.provisioningErr = nil
	m.classifiedAsConn = false
	m.classificationSet = false
	m.provisionedArrayID = ""
	m.symidErr = nil
	m.metricsReg = nil
	m.publishResolvedID = ""
	m.handleResolveErr = nil
	m.volumeHandle = ""
}

// --- FR-1: secret parsing steps --------------------------------------------

func (m *multiArrayZoneState) aKubernetesSecretContainsTwoArrayEntriesWithZoneLabel(zone string) error {
	m.secretParams.Set("storagearrays", []interface{}{
		map[string]interface{}{
			"storagearrayid": "000000000001",
			"labels":         map[string]interface{}{"topology.kubernetes.io/zone": zone},
		},
		map[string]interface{}{
			"storagearrayid": "000000000002",
			"labels":         map[string]interface{}{"topology.kubernetes.io/zone": zone},
		},
	})
	return nil
}

func (m *multiArrayZoneState) aKubernetesSecretContainsAValidEntryAndAMalformedEntryMissingStorageArrayID() error {
	m.secretParams.Set("storagearrays", []interface{}{
		map[string]interface{}{
			"labels": map[string]interface{}{"topology.kubernetes.io/zone": "us-east-1a"},
		},
		map[string]interface{}{
			"storagearrayid": "000000000002",
			"labels":         map[string]interface{}{"topology.kubernetes.io/zone": "us-east-1a"},
		},
	})
	return nil
}

func (m *multiArrayZoneState) aKubernetesSecretContainsANonMapEntryInTheStorageArraysList() error {
	m.secretParams.Set("storagearrays", []interface{}{
		"not-a-map",
		map[string]interface{}{
			"storagearrayid": "000000000001",
		},
	})
	return nil
}

func (m *multiArrayZoneState) theDriverParsesTheStorageArraysSecret() error {
	defer func() {
		if r := recover(); r != nil {
			m.didPanic = true
		}
	}()
	GetStorageArrays(m.secretParams, &m.svc.opts)
	return nil
}

func (m *multiArrayZoneState) bothArraysAreRegisteredForZone(zone string) error {
	count := 0
	for _, cfg := range m.svc.opts.StorageArrays {
		if z, ok := cfg.Labels["topology.kubernetes.io/zone"]; ok && z == zone {
			count++
		}
	}
	if count != 2 {
		return fmt.Errorf("expected 2 arrays registered for zone %s, got %d", zone, count)
	}
	return nil
}

func (m *multiArrayZoneState) noErrorIsRaised() error {
	if m.didPanic {
		return errors.New("expected no panic, but one occurred")
	}
	return nil
}

func (m *multiArrayZoneState) theMalformedEntryIsSkippedWithAnErrorIdentifyingEntryIndex(_ int) error {
	// The exact log message is verified by the table-driven unit tests in
	// TestGetStorageArrays (service_test.go). Here we assert the
	// observable effect: the malformed entry did not get registered.
	if len(m.svc.opts.StorageArrays) != 1 {
		return fmt.Errorf("expected exactly 1 registered array after skipping the malformed entry, got %d", len(m.svc.opts.StorageArrays))
	}
	return nil
}

func (m *multiArrayZoneState) theValidArrayRemainsRegistered() error {
	if _, ok := m.svc.opts.StorageArrays["000000000002"]; !ok {
		return errors.New("expected valid array 000000000002 to remain registered")
	}
	return nil
}

func (m *multiArrayZoneState) theDriverDoesNotPanic() error {
	if m.didPanic {
		return errors.New("driver panicked while parsing the storage arrays secret")
	}
	return nil
}

// --- FR-2: capacity-based selection steps -----------------------------------

func (m *multiArrayZoneState) aZoneContainsArrayAtPercentUtilization(zone, arrayID string, percent int) error {
	m.svc.opts.StorageArrays[arrayID] = StorageArrayConfig{
		Labels: map[string]interface{}{"topology.kubernetes.io/zone": zone},
	}
	m.svc.capCache.recordPollSuccess(arrayID, float64(percent), time.Now())
	return nil
}

func (m *multiArrayZoneState) arrayIsMarkedUnavailable(arrayID string) error {
	m.svc.capCache.invalidate(arrayID)
	return nil
}

func (m *multiArrayZoneState) theDriverSelectsAnArrayForZoneFromTopologyRequirements(zone string) error {
	topologyRequirement := &csi.TopologyRequirement{
		Preferred: []*csi.Topology{
			{Segments: map[string]string{"topology.kubernetes.io/zone": zone}},
		},
	}
	m.selectedArrayID, m.selectErr = m.svc.selectArrayFromTopologyRequirement(topologyRequirement)
	return nil
}

func (m *multiArrayZoneState) theSelectedArrayIs(arrayID string) error {
	if m.selectErr != nil {
		return fmt.Errorf("expected a selected array, got error: %v", m.selectErr)
	}
	if m.selectedArrayID != arrayID {
		return fmt.Errorf("expected selected array %s, got %s", arrayID, m.selectedArrayID)
	}
	return nil
}

func (m *multiArrayZoneState) arrayReportsASuccessfulCapacityPollAtPercentUtilization(arrayID string, percent int) error {
	m.svc.capCache.recordPollSuccess(arrayID, float64(percent), time.Now())
	return nil
}

func (m *multiArrayZoneState) arrayIsAvailable(arrayID string) error {
	_, available, _, _ := m.svc.capCache.snapshot(arrayID)
	if !available {
		return fmt.Errorf("expected array %s to be available", arrayID)
	}
	return nil
}

func (m *multiArrayZoneState) theDriverReturnsAnErrorContaining(substr string) error {
	if m.selectErr == nil {
		return errors.New("expected an error but got none")
	}
	if !strings.Contains(m.selectErr.Error(), substr) {
		return fmt.Errorf("expected error to contain %q, got %q", substr, m.selectErr.Error())
	}
	return nil
}

func (m *multiArrayZoneState) theErrorMentionsArraysAttempted(count int) error {
	if m.selectErr == nil {
		return errors.New("expected an error but got none")
	}
	expected := fmt.Sprintf("%d arrays attempted", count)
	if !strings.Contains(m.selectErr.Error(), expected) {
		return fmt.Errorf("expected error to mention %q, got %q", expected, m.selectErr.Error())
	}
	return nil
}

func (m *multiArrayZoneState) aProvisioningErrorIsObservedForArray(errMsg, _ string) error {
	m.provisioningErr = errors.New(errMsg)
	return nil
}

func (m *multiArrayZoneState) theDriverClassifiesTheProvisioningError() error {
	m.classifiedAsConn = isConnectivityError(m.provisioningErr)
	m.classificationSet = true
	return nil
}

func (m *multiArrayZoneState) theErrorIsClassifiedAsAConnectivityError() error {
	if !m.classificationSet {
		return errors.New("classification was never performed")
	}
	if !m.classifiedAsConn {
		return errors.New("expected the error to be classified as a connectivity error")
	}
	return nil
}

func (m *multiArrayZoneState) theErrorIsNotClassifiedAsAConnectivityError() error {
	if !m.classificationSet {
		return errors.New("classification was never performed")
	}
	if m.classifiedAsConn {
		return errors.New("expected the error to NOT be classified as a connectivity error")
	}
	return nil
}

// --- FR-2.1: SYMID StorageClass-targeted selection steps --------------------

// aCreateVolumeRequestSpecifiesSYMIDForZone exercises the same
// isArrayUnavailable check that CreateVolume applies to an explicit SYMID
// parameter (see controller.go CreateVolume), without requiring a full
// mock-Unisphere round trip.
func (m *multiArrayZoneState) aCreateVolumeRequestSpecifiesSYMIDForZone(symid, _ string) error {
	if unavailable, reason := m.svc.isArrayUnavailable(symid); unavailable {
		m.symidErr = fmt.Errorf("targeted array %s is unavailable: %s", symid, reason)
		return nil
	}
	m.provisionedArrayID = symid
	return nil
}

func (m *multiArrayZoneState) theVolumeIsProvisionedOnArray(arrayID string) error {
	if m.symidErr != nil {
		return fmt.Errorf("expected successful provisioning, got error: %v", m.symidErr)
	}
	if m.provisionedArrayID != arrayID {
		return fmt.Errorf("expected volume provisioned on %s, got %s", arrayID, m.provisionedArrayID)
	}
	return nil
}

func (m *multiArrayZoneState) theDriverReturnsAnErrorStatingArrayIsUnavailable(arrayID string) error {
	if m.symidErr == nil {
		return errors.New("expected an unavailable-target error but got none")
	}
	if !strings.Contains(m.symidErr.Error(), arrayID) {
		return fmt.Errorf("expected error to mention array %s, got %q", arrayID, m.symidErr.Error())
	}
	return nil
}

func (m *multiArrayZoneState) noVolumeIsProvisionedOnArray(arrayID string) error {
	if m.provisionedArrayID == arrayID {
		return fmt.Errorf("expected no fallback provisioning on %s, but it was provisioned there", arrayID)
	}
	return nil
}

// --- FR-3: observable per-array metrics steps -------------------------------

func (m *multiArrayZoneState) multiArrayMetricsAreEnabled() error {
	m.metricsReg = prometheus.NewRegistry()
	m.svc.multiArrayMetrics = newMultiArrayMetrics(m.metricsReg)
	m.svc.capacityThresholdFull = 100
	return nil
}

func (m *multiArrayZoneState) allConfiguredArrayIDs() []string {
	ids := make([]string, 0, len(m.svc.opts.StorageArrays))
	for id := range m.svc.opts.StorageArrays {
		ids = append(ids, id)
	}
	return ids
}

func (m *multiArrayZoneState) theDriverRefreshesCapacityMetricsForZone(_ string) error {
	m.svc.syncCapacityMetricsFromCache(m.allConfiguredArrayIDs())
	return nil
}

func (m *multiArrayZoneState) theDriverRecomputesTheAdoptionGauge() error {
	m.svc.multiArrayMetrics.setMultiArrayZones(countMultiArrayZones(m.svc.opts.StorageArrays))
	return nil
}

// gatherMetricValue reads the value of the named metric with the exact labels
// from the scenario's registry. For histograms it returns the sample count.
func (m *multiArrayZoneState) gatherMetricValue(name string, labels map[string]string) (float64, bool, error) {
	if m.metricsReg == nil {
		return 0, false, errors.New("metrics registry not initialized; did you enable multi-array metrics?")
	}
	mfs, err := m.metricsReg.Gather()
	if err != nil {
		return 0, false, err
	}
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, metric := range mf.GetMetric() {
			got := map[string]string{}
			for _, l := range metric.GetLabel() {
				got[l.GetName()] = l.GetValue()
			}
			match := len(got) == len(labels)
			for k, v := range labels {
				if got[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
			switch {
			case metric.Gauge != nil:
				return metric.GetGauge().GetValue(), true, nil
			case metric.Counter != nil:
				return metric.GetCounter().GetValue(), true, nil
			case metric.Histogram != nil:
				return float64(metric.GetHistogram().GetSampleCount()), true, nil
			}
		}
	}
	return 0, false, nil
}

func (m *multiArrayZoneState) metricForZoneHasAPositiveCount(name, zone string) error {
	v, ok, err := m.gatherMetricValue(name, map[string]string{"zone": zone})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("metric %s for zone %s not found", name, zone)
	}
	if v <= 0 {
		return fmt.Errorf("expected metric %s for zone %s count > 0, got %v", name, zone, v)
	}
	return nil
}

func (m *multiArrayZoneState) metricForZoneArrayEquals(name, zone, array string, expected int) error {
	v, ok, err := m.gatherMetricValue(name, map[string]string{"zone": zone, "array": array})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("metric %s for zone %s array %s not found", name, zone, array)
	}
	if v != float64(expected) {
		return fmt.Errorf("expected metric %s{zone=%s,array=%s}=%d, got %v", name, zone, array, expected, v)
	}
	return nil
}

func (m *multiArrayZoneState) metricEquals(name string, expected int) error {
	v, ok, err := m.gatherMetricValue(name, map[string]string{})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("metric %s not found", name)
	}
	if v != float64(expected) {
		return fmt.Errorf("expected metric %s=%d, got %v", name, expected, v)
	}
	return nil
}

// --- FR-4: cross-array volume access steps ----------------------------------

func (m *multiArrayZoneState) aVolumeProvisionedOnArrayWithDevice(arrayID, devID string) error {
	m.svc.opts.ClusterPrefix = "ABC"
	m.volumeHandle = m.svc.createCSIVolumeID("", "crossvol", arrayID, devID)
	return nil
}

func (m *multiArrayZoneState) aPublishRequestArrivesAtNodePreferringArray(_ string) error {
	// The node's zone-preferred array is irrelevant: publish resolves the
	// target array from the volume handle, not from node topology.
	_, arrayID, _, _, _, err := m.svc.parseCsiID(m.volumeHandle)
	m.publishResolvedID = arrayID
	m.handleResolveErr = err
	return nil
}

func (m *multiArrayZoneState) theArrayResolvedForPublishIs(arrayID string) error {
	if m.handleResolveErr != nil {
		return fmt.Errorf("expected resolution to array %s, got error: %v", arrayID, m.handleResolveErr)
	}
	if m.publishResolvedID != arrayID {
		return fmt.Errorf("expected publish to resolve to array %s, got %s", arrayID, m.publishResolvedID)
	}
	return nil
}

func (m *multiArrayZoneState) aMalformedVolumeHandle() error {
	m.volumeHandle = "not-a-valid-handle"
	return nil
}

func (m *multiArrayZoneState) theArrayIsResolvedFromTheVolumeHandle() error {
	_, _, _, _, _, err := m.svc.parseCsiID(m.volumeHandle)
	m.handleResolveErr = err
	return nil
}

func (m *multiArrayZoneState) resolvingTheArrayFromTheHandleFails() error {
	if m.handleResolveErr == nil {
		return errors.New("expected malformed handle resolution to fail, but it succeeded")
	}
	return nil
}

// RegisterMultiArrayZoneSteps wires the multi-array zone acceptance and
// capacity-based selection steps into the shared godog ScenarioContext.
func RegisterMultiArrayZoneSteps(s *godog.ScenarioContext) {
	m := &multiArrayZoneState{}

	s.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		m.reset()
		return ctx, nil
	})

	s.Step(`^a Kubernetes Secret contains two PowerMax array entries with zone label "([^"]*)"$`, m.aKubernetesSecretContainsTwoArrayEntriesWithZoneLabel)
	s.Step(`^a Kubernetes Secret contains a valid array entry and a malformed array entry missing storagearrayid$`, m.aKubernetesSecretContainsAValidEntryAndAMalformedEntryMissingStorageArrayID)
	s.Step(`^a Kubernetes Secret contains a non-map entry in the storagearrays list$`, m.aKubernetesSecretContainsANonMapEntryInTheStorageArraysList)
	s.Step(`^the driver parses the storage arrays secret$`, m.theDriverParsesTheStorageArraysSecret)
	s.Step(`^both arrays are registered for zone "([^"]*)"$`, m.bothArraysAreRegisteredForZone)
	s.Step(`^no error is raised$`, m.noErrorIsRaised)
	s.Step(`^the malformed entry is skipped with an error identifying entry index (\d+)$`, m.theMalformedEntryIsSkippedWithAnErrorIdentifyingEntryIndex)
	s.Step(`^the valid array remains registered$`, m.theValidArrayRemainsRegistered)
	s.Step(`^the driver does not panic$`, m.theDriverDoesNotPanic)

	s.Step(`^a zone "([^"]*)" contains array "([^"]*)" at (\d+) percent utilization$`, m.aZoneContainsArrayAtPercentUtilization)
	s.Step(`^array "([^"]*)" is marked unavailable$`, m.arrayIsMarkedUnavailable)
	s.Step(`^the driver selects an array for zone "([^"]*)" from topology requirements$`, m.theDriverSelectsAnArrayForZoneFromTopologyRequirements)
	s.Step(`^the selected array is "([^"]*)"$`, m.theSelectedArrayIs)
	s.Step(`^array "([^"]*)" reports a successful capacity poll at (\d+) percent utilization$`, m.arrayReportsASuccessfulCapacityPollAtPercentUtilization)
	s.Step(`^array "([^"]*)" is available$`, m.arrayIsAvailable)
	s.Step(`^the driver returns an error containing "([^"]*)"$`, m.theDriverReturnsAnErrorContaining)
	s.Step(`^the error mentions (\d+) arrays attempted$`, m.theErrorMentionsArraysAttempted)
	s.Step(`^a provisioning error "([^"]*)" is observed for array "([^"]*)"$`, m.aProvisioningErrorIsObservedForArray)
	s.Step(`^the driver classifies the provisioning error$`, m.theDriverClassifiesTheProvisioningError)
	s.Step(`^the error is classified as a connectivity error$`, m.theErrorIsClassifiedAsAConnectivityError)
	s.Step(`^the error is not classified as a connectivity error$`, m.theErrorIsNotClassifiedAsAConnectivityError)

	s.Step(`^a CreateVolume request specifies SYMID "([^"]*)" for zone "([^"]*)"$`, m.aCreateVolumeRequestSpecifiesSYMIDForZone)
	s.Step(`^the volume is provisioned on array "([^"]*)"$`, m.theVolumeIsProvisionedOnArray)
	s.Step(`^the driver returns an error stating array "([^"]*)" is unavailable$`, m.theDriverReturnsAnErrorStatingArrayIsUnavailable)
	s.Step(`^no volume is provisioned on array "([^"]*)"$`, m.noVolumeIsProvisionedOnArray)

	// FR-3: observable per-array metrics
	s.Step(`^multi-array metrics are enabled$`, m.multiArrayMetricsAreEnabled)
	s.Step(`^the driver refreshes capacity metrics for zone "([^"]*)"$`, m.theDriverRefreshesCapacityMetricsForZone)
	s.Step(`^the driver recomputes the multi-array zone adoption gauge$`, m.theDriverRecomputesTheAdoptionGauge)
	s.Step(`^metric "([^"]*)" for zone "([^"]*)" has a positive count$`, m.metricForZoneHasAPositiveCount)
	s.Step(`^metric "([^"]*)" for zone "([^"]*)" array "([^"]*)" equals (\d+)$`, m.metricForZoneArrayEquals)
	s.Step(`^metric "([^"]*)" equals (\d+)$`, m.metricEquals)

	// FR-4: cross-array volume access
	s.Step(`^a volume provisioned on array "([^"]*)" with device "([^"]*)"$`, m.aVolumeProvisionedOnArrayWithDevice)
	s.Step(`^a publish request for that volume arrives at a node preferring array "([^"]*)"$`, m.aPublishRequestArrivesAtNodePreferringArray)
	s.Step(`^the array resolved for publish is "([^"]*)"$`, m.theArrayResolvedForPublishIs)
	s.Step(`^a malformed volume handle$`, m.aMalformedVolumeHandle)
	s.Step(`^the array is resolved from the volume handle$`, m.theArrayIsResolvedFromTheVolumeHandle)
	s.Step(`^resolving the array from the handle fails$`, m.resolvingTheArrayFromTheHandleFails)
}
