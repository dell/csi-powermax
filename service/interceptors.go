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
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csmlog"
)

// NewOperationInterceptor returns a gRPC UnaryServerInterceptor that records
// dell_csi_operation_* metrics using array_id label.
// Only tracks the 8 core volume lifecycle operations with per-operation protocol detection.
func NewOperationInterceptor(reg prometheus.Registerer, arrayID string) grpc.UnaryServerInterceptor {
	// Create metrics locally (no singleton pattern - follows PowerStore/PowerScale pattern)
	opTotal := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dell_csi_operation_total",
		Help: "Total CSI operations.",
	}, []string{"array_id", "operation", "status"})
	opDuration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "dell_csi_operation_duration_seconds",
		Help:    "CSI operation duration.",
		Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
	}, []string{"array_id", "operation"})
	opFailure := prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "dell_csi_operation_failure_total",
		Help: "Total CSI operation failures.",
	}, []string{"array_id", "operation", "error_code"})

	// Register metrics immediately if registry is provided
	if reg != nil {
		reg.MustRegister(opTotal, opDuration, opFailure)
		csmlog.Infof("Operation interceptor registered metrics")
	}

	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()
		operation := extractPMXOperation(info.FullMethod)

		// Skip non-volume lifecycle operations
		if shouldSkipOperation(operation) {
			return handler(ctx, req)
		}

		csmlog.Infof("Operation interceptor called for: %s", operation)

		// Extract array ID from request (volume ID)
		requestArrayID := extractArrayIDFromRequest(req)
		if requestArrayID == "unknown" {
			requestArrayID = arrayID
		}

		resp, err := handler(ctx, req)

		duration := time.Since(start).Seconds()
		opDuration.WithLabelValues(requestArrayID, operation).Observe(duration)

		if err != nil {
			if isPMXContextCancelled(err) {
				return resp, err
			}
			opTotal.WithLabelValues(requestArrayID, operation, "failure").Inc()
			opFailure.WithLabelValues(requestArrayID, operation, classifyPMXError(err)).Inc()
		} else {
			opTotal.WithLabelValues(requestArrayID, operation, "success").Inc()
		}
		return resp, err
	}
}

func extractPMXOperation(fullMethod string) string {
	parts := strings.Split(fullMethod, "/")
	if len(parts) == 0 {
		return "unknown"
	}
	return parts[len(parts)-1]
}

func classifyPMXError(err error) string {
	if err == nil {
		return "none"
	}
	s, ok := status.FromError(err)
	if !ok {
		return "unknown"
	}
	switch s.Code() {
	case codes.DeadlineExceeded:
		return "timeout"
	case codes.Unauthenticated, codes.PermissionDenied:
		return "auth_failure"
	case codes.NotFound:
		return "not_found"
	default:
		return "unknown"
	}
}

func isPMXContextCancelled(err error) bool {
	if err == context.Canceled {
		return true
	}
	s, ok := status.FromError(err)
	return ok && s.Code() == codes.Canceled
}

// shouldSkipOperation returns true for CSI operations that should not have metrics recorded.
// Only the 8 core volume lifecycle operations are instrumented.
func shouldSkipOperation(operation string) bool {
	switch operation {
	case "CreateVolume", "DeleteVolume",
		"ControllerPublishVolume", "ControllerUnpublishVolume",
		"NodeStageVolume", "NodeUnstageVolume",
		"NodePublishVolume", "NodeUnpublishVolume":
		return false
	}
	return true
}

// extractProtocolFromRequest attempts to extract the storage protocol from CSI requests.
// PowerMax volume ID format: "csivol-<uuid>-<symID>-<protocol>"
func extractProtocolFromRequest(req interface{}) string {
	if req == nil {
		return "unknown"
	}

	switch r := req.(type) {
	case *csi.CreateVolumeRequest:
		if r.Parameters != nil {
			// Try to extract protocol from storage pool name
			if storagePool, ok := r.Parameters["storagePool"]; ok {
				return extractProtocolFromStoragePool(storagePool)
			}
		}
	case *csi.DeleteVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.ControllerPublishVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.ControllerUnpublishVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.NodeStageVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.NodeUnstageVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.NodePublishVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	case *csi.NodeUnpublishVolumeRequest:
		if _, protocol := parsePowerMaxVolumeID(r.VolumeId); protocol != "unknown" {
			return protocol
		}
	}

	return "unknown"
}

// extractArrayIDFromRequest attempts to extract the array ID from CSI requests.
func extractArrayIDFromRequest(req interface{}) string {
	if req == nil {
		return "unknown"
	}

	switch r := req.(type) {
	case *csi.DeleteVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.ControllerPublishVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.ControllerUnpublishVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.NodeStageVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.NodeUnstageVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.NodePublishVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	case *csi.NodeUnpublishVolumeRequest:
		if arrayID, _ := parsePowerMaxVolumeID(r.VolumeId); arrayID != "unknown" {
			return arrayID
		}
	}

	return "unknown"
}

// extractProtocolFromResponse attempts to extract protocol from CreateVolume response.
func extractProtocolFromResponse(resp interface{}) string {
	switch r := resp.(type) {
	case *csi.CreateVolumeResponse:
		if r != nil && r.GetVolume() != nil {
			if _, protocol := parsePowerMaxVolumeID(r.GetVolume().GetVolumeId()); protocol != "unknown" {
				return protocol
			}
		}
	}
	return "unknown"
}

// parsePowerMaxVolumeID parses PowerMax volume ID format.
// Format: "csivol-<uuid>-<symID>-<protocol>" or "<volumeID>/<arrayID>"
func parsePowerMaxVolumeID(volumeID string) (string, string) {
	if volumeID == "" {
		return "unknown", "unknown"
	}

	// Try format: "volumeID/arrayID"
	if parts := strings.Split(volumeID, "/"); len(parts) == 2 {
		arrayID := parts[1]
		// Try to extract protocol from volume identifier part
		if idParts := strings.Split(parts[0], "-"); len(idParts) >= 4 && strings.HasPrefix(parts[0], "csivol-") {
			protocol := normalizeProtocol(idParts[len(idParts)-1])
			return arrayID, protocol
		}
		return arrayID, "unknown"
	}

	// Try format: "csivol-<uuid>-<symID>-<protocol>"
	parts := strings.Split(volumeID, "-")
	if len(parts) >= 4 && strings.HasPrefix(volumeID, "csivol-") {
		symID := parts[2]                                  // Symmetrix ID (array ID)
		protocol := normalizeProtocol(parts[len(parts)-1]) // Protocol (last part)
		return symID, protocol
	}

	return "unknown", "unknown"
}

// extractProtocolFromStoragePool extracts protocol from PowerMax storage pool name.
// Storage pool format examples: "SRP_1", "Bronze_FC", "Diamond_NVMETCP"
func extractProtocolFromStoragePool(storagePool string) string {
	if storagePool == "" {
		return "unknown"
	}

	// Check if storage pool name contains protocol suffix
	if strings.Contains(strings.ToUpper(storagePool), "_FC") {
		return "FC"
	}
	if strings.Contains(strings.ToUpper(storagePool), "_ISCSI") {
		return "ISCSI"
	}
	if strings.Contains(strings.ToUpper(storagePool), "_NVMETCP") {
		return "NVMETCP"
	}
	if strings.Contains(strings.ToUpper(storagePool), "_NVMEFC") {
		return "NVMEFC"
	}

	return "unknown"
}

// normalizeProtocol normalizes protocol names to standard format.
func normalizeProtocol(protocol string) string {
	protocol = strings.ToUpper(strings.TrimSpace(protocol))
	switch protocol {
	case "FC":
		return "FC"
	case "ISCSI":
		return "ISCSI"
	case "NVMETCP", "NVME_TCP", "NVME-TCP":
		return "NVMETCP"
	case "NVMEFC", "NVME_FC", "NVME-FC":
		return "NVMEFC"
	default:
		return "unknown"
	}
}
