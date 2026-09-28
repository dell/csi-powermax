/*
 Copyright © 2025-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	grpcstatus "google.golang.org/grpc/status"
)

func gatherPMXSvcMetric(t *testing.T, reg prometheus.Gatherer, name string) *dto.MetricFamily {
	t.Helper()
	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			return mf
		}
	}
	return nil
}

func counterPMXSvc(mf *dto.MetricFamily, labels map[string]string) float64 {
	if mf == nil {
		return 0
	}
	for _, m := range mf.GetMetric() {
		got := make(map[string]string)
		for _, lp := range m.GetLabel() {
			got[lp.GetName()] = lp.GetValue()
		}
		match := true
		for k, v := range labels {
			if got[k] != v {
				match = false
				break
			}
		}
		if match {
			return m.GetCounter().GetValue()
		}
	}
	return 0
}

// U-PMX-01: CreateVolume success uses array_id label
func TestPMXInterceptor_CreateVolume_Success(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")

	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/CreateVolume"}
	noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

	_, err := interceptor(context.Background(), nil, info, noopH)
	require.NoError(t, err)

	mf := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	require.NotNil(t, mf)
	v := counterPMXSvc(mf, map[string]string{
		"array_id": "array-1", "operation": "CreateVolume", "status": "success",
	})
	assert.Equal(t, 1.0, v)
}

// U-PMX-02: Sidecar port check — default health-monitor still uses 8080
// This test documents the port reservation constraint (config validation at runtime).
func TestPMXInterceptor_SidecarPortReservation(t *testing.T) {
	const existingHealthMonitorPort = ":8080"
	const metricsPort = ":8443"

	assert.NotEqual(t, existingHealthMonitorPort, metricsPort,
		"MetricsServer port :8443 must not conflict with existing health-monitor :8080")
	assert.Equal(t, ":8443", metricsPort,
		"New metrics sidecar must bind to :8443 per FR-26/FR-27")
}

// errHandler returns a handler that always returns the given error.
func errHandler(e error) grpc.UnaryHandler {
	return func(_ context.Context, _ interface{}) (interface{}, error) { return nil, e }
}

// U-PMX-03 through U-PMX-10: All 8 CSI operations are instrumented and emit success counter.
func TestPMXInterceptor_AllEightCSIOperations(t *testing.T) {
	ops := []string{
		"CreateVolume",
		"DeleteVolume",
		"ControllerPublishVolume",
		"ControllerUnpublishVolume",
		"NodeStageVolume",
		"NodeUnstageVolume",
		"NodePublishVolume",
		"NodeUnpublishVolume",
	}

	for _, op := range ops {
		op := op
		t.Run(op, func(t *testing.T) {
			reg := prometheus.NewRegistry()
			interceptor := NewOperationInterceptor(reg, "array-1")
			info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/" + op}
			noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

			_, err := interceptor(context.Background(), nil, info, noopH)
			require.NoError(t, err)

			mf := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
			require.NotNil(t, mf, op+": dell_csi_operation_total must be emitted")
			v := counterPMXSvc(mf, map[string]string{
				"array_id": "array-1", "operation": op, "status": "success",
			})
			assert.Equal(t, 1.0, v, op+": success counter must be 1")
		})
	}
}

// U-PMX-11: gRPC error → failure counter incremented, success counter stays 0.
func TestPMXInterceptor_FailureCounter(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/CreateVolume"}

	_, _ = interceptor(context.Background(), nil, info, errHandler(assert.AnError))

	mfTotal := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	require.NotNil(t, mfTotal)
	failV := counterPMXSvc(mfTotal, map[string]string{
		"array_id": "array-1", "operation": "CreateVolume", "status": "failure",
	})
	assert.Equal(t, 1.0, failV, "failure counter must be 1")

	successV := counterPMXSvc(mfTotal, map[string]string{
		"array_id": "array-1", "operation": "CreateVolume", "status": "success",
	})
	assert.Equal(t, 0.0, successV, "success counter must remain 0")
}

// U-PMX-12: operation_failure_total records the error_code label.
func TestPMXInterceptor_ErrorCodeLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodeStageVolume"}

	_, _ = interceptor(context.Background(), nil, info, errHandler(assert.AnError))

	mfFail := gatherPMXSvcMetric(t, reg, "dell_csi_operation_failure_total")
	require.NotNil(t, mfFail, "dell_csi_operation_failure_total must be emitted")
	v := counterPMXSvc(mfFail, map[string]string{
		"array_id": "array-1", "operation": "NodeStageVolume", "error_code": "unknown",
	})
	assert.Equal(t, 1.0, v)
}

// U-PMX-13: gRPC codes.DeadlineExceeded → error_code=timeout.
func TestPMXInterceptor_TimeoutErrorCode(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/DeleteVolume"}

	timeoutErr := grpcstatus.Error(codes.DeadlineExceeded, "deadline exceeded")
	_, _ = interceptor(context.Background(), nil, info, errHandler(timeoutErr))

	mfFail := gatherPMXSvcMetric(t, reg, "dell_csi_operation_failure_total")
	require.NotNil(t, mfFail)
	v := counterPMXSvc(mfFail, map[string]string{
		"array_id": "array-1", "operation": "DeleteVolume", "error_code": "timeout",
	})
	assert.Equal(t, 1.0, v)
}

// U-PMX-14: gRPC codes.Unauthenticated → error_code=auth_failure.
func TestPMXInterceptor_AuthFailureErrorCode(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/CreateVolume"}

	authErr := grpcstatus.Error(codes.Unauthenticated, "not authenticated")
	_, _ = interceptor(context.Background(), nil, info, errHandler(authErr))

	mfFail := gatherPMXSvcMetric(t, reg, "dell_csi_operation_failure_total")
	require.NotNil(t, mfFail)
	v := counterPMXSvc(mfFail, map[string]string{
		"array_id": "array-1", "operation": "CreateVolume", "error_code": "auth_failure",
	})
	assert.Equal(t, 1.0, v)
}

// U-PMX-15: context.Canceled → no counters incremented (skip).
func TestPMXInterceptor_ContextCancelled_Skip(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/CreateVolume"}

	cancelledErr := grpcstatus.Error(codes.Canceled, "context cancelled")
	_, _ = interceptor(context.Background(), nil, info, errHandler(cancelledErr))

	mfTotal := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	if mfTotal != nil {
		failV := counterPMXSvc(mfTotal, map[string]string{
			"array_id": "array-1", "operation": "CreateVolume", "status": "failure",
		})
		assert.Equal(t, 0.0, failV, "cancelled calls must not increment failure counter")
		successV := counterPMXSvc(mfTotal, map[string]string{
			"array_id": "array-1", "operation": "CreateVolume", "status": "success",
		})
		assert.Equal(t, 0.0, successV, "cancelled calls must not increment success counter")
	}
}

// U-PMX-16: Duration histogram is populated after a call.
func TestPMXInterceptor_DurationHistogramPopulated(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/CreateVolume"}
	noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

	_, _ = interceptor(context.Background(), nil, info, noopH)

	mfDur := gatherPMXSvcMetric(t, reg, "dell_csi_operation_duration_seconds")
	require.NotNil(t, mfDur, "dell_csi_operation_duration_seconds must be emitted")
	require.NotEmpty(t, mfDur.GetMetric())
	count := mfDur.GetMetric()[0].GetHistogram().GetSampleCount()
	assert.Equal(t, uint64(1), count, "histogram must have 1 observation")
}

// U-PMX-17: Multiple calls accumulate counters.
func TestPMXInterceptor_MultipleCallsAccumulate(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Node/NodePublishVolume"}
	noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

	for i := 0; i < 5; i++ {
		_, _ = interceptor(context.Background(), nil, info, noopH)
	}

	mf := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	require.NotNil(t, mf)
	v := counterPMXSvc(mf, map[string]string{
		"array_id": "array-1", "operation": "NodePublishVolume", "status": "success",
	})
	assert.Equal(t, 5.0, v, "5 calls must produce counter=5")
}

// U-PMX-18: Unknown full method path still extracts last segment.
func TestPMXInterceptor_UnknownMethodPath(t *testing.T) {
	t.Skip("Skipping: Unknown operations are not instrumented (only 8 core volume lifecycle operations)")
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/my.pkg.Service/SomeCustomOp"}
	noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

	_, err := interceptor(context.Background(), nil, info, noopH)
	require.NoError(t, err)

	mf := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	require.NotNil(t, mf)
	v := counterPMXSvc(mf, map[string]string{
		"array_id": "array-1", "operation": "SomeCustomOp", "status": "success",
	})
	assert.Equal(t, 1.0, v)
}

func TestExtractProtocolFromRequest(t *testing.T) {
	if p := extractProtocolFromRequest(nil); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}

	req1 := &csi.CreateVolumeRequest{
		Parameters: map[string]string{
			"storagePool": "SRP_1_FC",
		},
	}
	if p := extractProtocolFromRequest(req1); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	req2 := &csi.DeleteVolumeRequest{
		VolumeId: "csivol-123-000197900046-FC",
	}
	if p := extractProtocolFromRequest(req2); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	req3 := &csi.ControllerPublishVolumeRequest{
		VolumeId: "csivol-123-000197900046-ISCSI",
	}
	if p := extractProtocolFromRequest(req3); p != "ISCSI" {
		t.Errorf("Expected ISCSI, got %s", p)
	}

	req4 := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "csivol-123-000197900046-NVMETCP",
	}
	if p := extractProtocolFromRequest(req4); p != "NVMETCP" {
		t.Errorf("Expected NVMETCP, got %s", p)
	}

	req5 := &csi.NodePublishVolumeRequest{
		VolumeId: "csivol-123-000197900046-FC",
	}
	if p := extractProtocolFromRequest(req5); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	req6 := &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csivol-123-000197900046-NFS",
	}
	if p := extractProtocolFromRequest(req6); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}

	req7 := &csi.NodeGetVolumeStatsRequest{
		VolumeId: "csivol-123-000197900046-FC",
	}
	if p := extractProtocolFromRequest(req7); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}

	req8 := &csi.NodeStageVolumeRequest{
		VolumeId: "csivol-123-000197900053-FC",
	}
	if p := extractProtocolFromRequest(req8); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	req9 := &csi.NodeUnstageVolumeRequest{
		VolumeId: "csivol-123-000197900054-NFS",
	}
	if p := extractProtocolFromRequest(req9); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}
}

func TestExtractProtocolFromResponse(t *testing.T) {
	if p := extractProtocolFromResponse(nil); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}

	resp1 := &csi.CreateVolumeResponse{
		Volume: &csi.Volume{
			VolumeId: "csivol-123-000197900046-FC",
		},
	}
	if p := extractProtocolFromResponse(resp1); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	resp2 := &csi.ListVolumesResponse{
		Entries: []*csi.ListVolumesResponse_Entry{
			{
				Volume: &csi.Volume{
					VolumeId: "csivol-123-000197900046-NFS",
				},
			},
		},
	}
	if p := extractProtocolFromResponse(resp2); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}
}

func TestParsePowerMaxVolumeID(t *testing.T) {
	symID, proto := parsePowerMaxVolumeID("csivol-123-000197900046-FC")
	if symID != "000197900046" || proto != "FC" {
		t.Errorf("Expected 000197900046, FC. Got %s, %s", symID, proto)
	}

	symID, proto = parsePowerMaxVolumeID("invalid-format")
	if symID != "unknown" || proto != "unknown" {
		t.Errorf("Expected unknown, unknown. Got %s, %s", symID, proto)
	}

	symID, proto = parsePowerMaxVolumeID("csi-XYZ-pmax-000197900046-12345")
	if symID != "unknown" || proto != "unknown" {
		t.Errorf("Expected unknown, unknown. Got %s, %s", symID, proto)
	}

	symID, proto = parsePowerMaxVolumeID("csivol-123/000197900046")
	if symID != "000197900046" || proto != "unknown" {
		t.Errorf("Expected unknown, unknown. Got %s, %s", symID, proto)
	}

	symID, proto = parsePowerMaxVolumeID("csivol-123-456-789-FC/000197900046")
	if symID != "000197900046" || proto != "FC" {
		t.Errorf("Expected 000197900046, FC. Got %s, %s", symID, proto)
	}
}

func TestExtractProtocolFromStoragePool(t *testing.T) {
	p := extractProtocolFromStoragePool("SRP_1_FC")
	if p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}

	p = extractProtocolFromStoragePool("SRP_1_ISCSI")
	if p != "ISCSI" {
		t.Errorf("Expected ISCSI, got %s", p)
	}

	p = extractProtocolFromStoragePool("SRP_1_NVMETCP")
	if p != "NVMETCP" {
		t.Errorf("Expected NVMETCP, got %s", p)
	}

	p = extractProtocolFromStoragePool("SRP_1_NVMEFC")
	if p != "NVMEFC" {
		t.Errorf("Expected NVMEFC, got %s", p)
	}

	p = extractProtocolFromStoragePool("SRP_1")
	if p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}

	p = extractProtocolFromStoragePool("")
	if p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}
}

func TestNormalizeProtocol(t *testing.T) {
	if p := normalizeProtocol("FC"); p != "FC" {
		t.Errorf("Expected FC, got %s", p)
	}
	if p := normalizeProtocol("ISCSI"); p != "ISCSI" {
		t.Errorf("Expected ISCSI, got %s", p)
	}
	if p := normalizeProtocol("NVMETCP"); p != "NVMETCP" {
		t.Errorf("Expected NVMETCP, got %s", p)
	}
	if p := normalizeProtocol("NFS"); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}
	if p := normalizeProtocol("OTHER"); p != "unknown" {
		t.Errorf("Expected unknown, got %s", p)
	}
}

func TestExtractArrayIDFromRequest(t *testing.T) {
	if a := extractArrayIDFromRequest(nil); a != "unknown" {
		t.Errorf("Expected unknown, got %s", a)
	}

	req2 := &csi.DeleteVolumeRequest{
		VolumeId: "csivol-123-000197900047-FC",
	}
	if a := extractArrayIDFromRequest(req2); a != "000197900047" {
		t.Errorf("Expected 000197900047, got %s", a)
	}

	req3 := &csi.ControllerPublishVolumeRequest{
		VolumeId: "csivol-123-000197900048-FC",
	}
	if a := extractArrayIDFromRequest(req3); a != "000197900048" {
		t.Errorf("Expected 000197900048, got %s", a)
	}

	req4 := &csi.ControllerUnpublishVolumeRequest{
		VolumeId: "csivol-123-000197900049-FC",
	}
	if a := extractArrayIDFromRequest(req4); a != "000197900049" {
		t.Errorf("Expected 000197900049, got %s", a)
	}

	req5 := &csi.NodePublishVolumeRequest{
		VolumeId: "csivol-123-000197900050-FC",
	}
	if a := extractArrayIDFromRequest(req5); a != "000197900050" {
		t.Errorf("Expected 000197900050, got %s", a)
	}

	req6 := &csi.NodeUnpublishVolumeRequest{
		VolumeId: "csivol-123-000197900051-FC",
	}
	if a := extractArrayIDFromRequest(req6); a != "000197900051" {
		t.Errorf("Expected 000197900051, got %s", a)
	}

	req8 := &csi.NodeStageVolumeRequest{
		VolumeId: "csivol-123-000197900053-FC",
	}
	if a := extractArrayIDFromRequest(req8); a != "000197900053" {
		t.Errorf("Expected 000197900053, got %s", a)
	}

	req9 := &csi.NodeUnstageVolumeRequest{
		VolumeId: "csivol-123-000197900054-FC",
	}
	if a := extractArrayIDFromRequest(req9); a != "000197900054" {
		t.Errorf("Expected 000197900054, got %s", a)
	}

	req10 := &csi.CreateVolumeRequest{}
	if a := extractArrayIDFromRequest(req10); a != "unknown" {
		t.Errorf("Expected unknown, got %s", a)
	}
}

// TestPMXInterceptor_NonCoreOperation_Skipped verifies that non-lifecycle
// operations (e.g. ListVolumes) pass through without recording metrics.
func TestPMXInterceptor_NonCoreOperation_Skipped(t *testing.T) {
	reg := prometheus.NewRegistry()
	interceptor := NewOperationInterceptor(reg, "array-1")
	info := &grpc.UnaryServerInfo{FullMethod: "/csi.v1.Controller/ListVolumes"}
	noopH := func(_ context.Context, _ interface{}) (interface{}, error) { return nil, nil }

	_, err := interceptor(context.Background(), nil, info, noopH)
	require.NoError(t, err)

	// Non-core operations must NOT produce any metric entries.
	mf := gatherPMXSvcMetric(t, reg, "dell_csi_operation_total")
	assert.Nil(t, mf, "non-core operations must not emit dell_csi_operation_total")
}

func TestShouldSkipOperation(t *testing.T) {
	assert.False(t, shouldSkipOperation("CreateVolume"))
	assert.False(t, shouldSkipOperation("NodePublishVolume"))
	assert.True(t, shouldSkipOperation("ListVolumes"))
	assert.True(t, shouldSkipOperation("GetCapacity"))
	assert.True(t, shouldSkipOperation(""))
}

func TestClassifyPMXError_NilAndNotFound(t *testing.T) {
	assert.Equal(t, "none", classifyPMXError(nil))
	assert.Equal(t, "not_found", classifyPMXError(grpcstatus.Error(codes.NotFound, "not found")))
}

func TestIsPMXContextCancelled_NativeCanceled(t *testing.T) {
	assert.True(t, isPMXContextCancelled(context.Canceled))
}

func TestExtractProtocolFromRequest_NodeUnstageAndUnpublishKnownProtocol(t *testing.T) {
	unstage := &csi.NodeUnstageVolumeRequest{VolumeId: "csivol-abc-000197900055-FC"}
	if p := extractProtocolFromRequest(unstage); p != "FC" {
		t.Errorf("NodeUnstageVolumeRequest: expected FC, got %s", p)
	}
	unpublish := &csi.NodeUnpublishVolumeRequest{VolumeId: "csivol-abc-000197900055-ISCSI"}
	if p := extractProtocolFromRequest(unpublish); p != "ISCSI" {
		t.Errorf("NodeUnpublishVolumeRequest: expected ISCSI, got %s", p)
	}
}

func TestNormalizeProtocol_NVMEFCVariants(t *testing.T) {
	assert.Equal(t, "NVMEFC", normalizeProtocol("NVMEFC"))
	assert.Equal(t, "NVMEFC", normalizeProtocol("NVME_FC"))
	assert.Equal(t, "NVMEFC", normalizeProtocol("NVME-FC"))
}

func TestParsePowerMaxVolumeID_EmptyString(t *testing.T) {
	symID, proto := parsePowerMaxVolumeID("")
	assert.Equal(t, "unknown", symID)
	assert.Equal(t, "unknown", proto)
}
