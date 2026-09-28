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
	"fmt"
	"strings"

	pmax "github.com/dell/gopowermax/v2"
	v100 "github.com/dell/gopowermax/v2/types/v100"
)

// extractVolumeIDFromIdentifier extracts the volume UUID from PowerMax volume identifier
// Volume identifier format: "csivol-<uuid>-<symID>-<protocol>"
// We extract the UUID part (second element after splitting by "-")
func extractVolumeIDFromIdentifier(identifier string) string {
	if identifier == "" {
		return ""
	}

	// Split by "-" and extract the UUID part
	parts := strings.Split(identifier, "-")
	if len(parts) >= 2 && strings.HasPrefix(identifier, "csivol-") {
		return parts[1]
	}

	return identifier
}

// extractProtocolFromIdentifier extracts the protocol from PowerMax volume identifier
// Volume identifier format: "csivol-<uuid>-<symID>-<protocol>"
// We extract the protocol part (last element after splitting by "-")
func extractProtocolFromIdentifier(identifier string) string {
	if identifier == "" {
		return "unknown"
	}

	// Split by "-" and extract the protocol part (last element)
	parts := strings.Split(identifier, "-")
	if len(parts) >= 4 && strings.HasPrefix(identifier, "csivol-") {
		protocol := parts[len(parts)-1]
		// Normalize protocol names
		switch strings.ToUpper(protocol) {
		case "FC":
			return "FC"
		case "ISCSI":
			return "ISCSI"
		case "NVMETCP":
			return "NVMETCP"
		case "NVMEFC":
			return "NVMEFC"
		default:
			return protocol
		}
	}

	return "unknown"
}

// PmaxSGAdapter adapts pmax.Pmax to StorageGroupClient.
type PmaxSGAdapter struct {
	Client        pmax.Pmax
	ArrayID       string
	ClusterPrefix string
	Validator     VolumeValidator
	runtime       *MetricsRuntime
}

// NewPmaxSGAdapterWithRuntime creates a PmaxSGAdapter with MetricsRuntime support.
func NewPmaxSGAdapterWithRuntime(client pmax.Pmax, arrayID, clusterPrefix string, validator VolumeValidator, runtime *MetricsRuntime) *PmaxSGAdapter {
	return &PmaxSGAdapter{
		Client:        client,
		ArrayID:       arrayID,
		ClusterPrefix: clusterPrefix,
		Validator:     validator,
		runtime:       runtime,
	}
}

func (a *PmaxSGAdapter) GetStorageGroups(ctx context.Context) ([]StorageGroupInfo, error) {
	var sgList *v100.StorageGroupIDList
	var err error

	// Use MetricsRuntime if available for resilience features
	if a.runtime != nil {
		cacheKey := fmt.Sprintf("sglist_%s", a.ArrayID)
		result, rtErr := a.runtime.Do(ctx, "storagegroups", cacheKey, func(callCtx context.Context) (any, error) {
			return a.Client.GetStorageGroupIDList(callCtx, a.ArrayID, "", false)
		})
		if rtErr != nil {
			return nil, rtErr
		}
		sgList = result.(*v100.StorageGroupIDList)
	} else {
		sgList, err = a.Client.GetStorageGroupIDList(ctx, a.ArrayID, "", false)
		if err != nil {
			return nil, err
		}
	}

	// Refresh validator cache if available
	skipValidation := false
	if a.Validator != nil {
		if err := a.Validator.RefreshCache(ctx); err != nil {
			skipValidation = true
		}
	}

	// Calculate capacity and volume count per SG by iterating PV-validated volumes only.
	// This ensures NumOfVolumes only counts volumes with corresponding Kubernetes PVs.
	// Also track protocol counts per storage group
	sgTotalGB := make(map[string]float64)
	sgUsedGB := make(map[string]float64)
	sgVolCount := make(map[string]int)
	sgProtocolCounts := make(map[string]map[string]int) // SG -> Protocol -> Count

	volList, err := a.Client.GetVolumeIDList(ctx, a.ArrayID, "", false)
	if err == nil {
		for _, volID := range volList {
			v, err := a.Client.GetVolumeByID(ctx, a.ArrayID, volID)
			// Apply PV validation filter only
			if err == nil && v.VolumeIdentifier != "" {
				// Apply PV validation if available
				if !skipValidation && a.Validator != nil {
					// Use the hex volume ID (v.VolumeID) for PV validation
					// This matches what the validator extracts from the PV's volumeHandle
					volumeID := v.VolumeID
					isManaged, err := a.Validator.IsDriverManaged(ctx, volumeID)
					if err != nil || !isManaged {
						continue
					}
				}

				// Extract protocol from volume identifier
				// Format: "csivol-<uuid>-<symID>-<protocol>"
				protocol := extractProtocolFromIdentifier(v.VolumeIdentifier)

				totalGB := v.CapacityGB
				usedGB := v.CapacityGB * (float64(v.AllocatedPercent) / 100.0)
				for _, sg := range v.StorageGroupIDList {
					sgTotalGB[sg] += totalGB
					sgUsedGB[sg] += usedGB
					sgVolCount[sg]++

					// Track protocol counts
					if sgProtocolCounts[sg] == nil {
						sgProtocolCounts[sg] = make(map[string]int)
					}
					sgProtocolCounts[sg][protocol]++
				}
			}
		}
	}

	var result []StorageGroupInfo
	for _, sgName := range sgList.StorageGroupIDs {
		// Only include storage groups that have PV-validated volumes
		if sgVolCount[sgName] == 0 {
			continue
		}
		g, err := a.Client.GetStorageGroup(ctx, a.ArrayID, sgName)
		if err != nil {
			continue
		}

		// Use volume-based capacity calculation since API doesn't provide it for thin provisioning
		totalCapacity := sgTotalGB[g.StorageGroupID]
		if totalCapacity == 0 && g.CapacityGB > 0 {
			// Fallback to API value if available (for thick provisioning)
			totalCapacity = g.CapacityGB
		}

		// Use cluster-filtered volume count instead of array-wide g.NumOfVolumes
		numVolumes := sgVolCount[g.StorageGroupID]

		result = append(result, StorageGroupInfo{
			StorageGroupID: g.StorageGroupID,
			NumOfVolumes:   numVolumes,
			CapacityGB:     totalCapacity,              // computed from THIS CLUSTER's volumes
			UsedGB:         sgUsedGB[g.StorageGroupID], // computed from THIS CLUSTER's volumes
			NumOfSnapshots: g.NumOfSnapshots,

			ProtocolCounts: sgProtocolCounts[g.StorageGroupID],
		})
	}
	return result, nil
}

// PmaxSRPAdapter adapts pmax.Pmax to SRPClient.
type PmaxSRPAdapter struct {
	Client  pmax.Pmax
	ArrayID string
}

func (a *PmaxSRPAdapter) GetSRPs(ctx context.Context) ([]SRPInfo, error) {
	srpList, err := a.Client.GetStoragePoolList(ctx, a.ArrayID)
	if err != nil {
		return nil, err
	}
	var result []SRPInfo
	for _, srpID := range srpList.StoragePoolIDs {
		srp, err := a.Client.GetStoragePool(ctx, a.ArrayID, srpID)
		if err != nil {
			continue
		}
		info := SRPInfo{SRPID: srp.StoragePoolID}

		// Try SrpCap first (standard capacity structure)
		if srp.SrpCap != nil {
			info.UsableTotalTB = srp.SrpCap.UsableTotInTB
			info.UsableUsedTB = srp.SrpCap.UsableUsedInTB
			info.SubscribedTB = srp.SrpCap.SubTotInTB
			info.SnapshotTB = srp.SrpCap.SnapModInTB
			info.EffectiveUsedCapacityPercent = float64(srp.SrpCap.EffectiveUsedCapacityPercent)
		} else if srp.FbaCap != nil && srp.FbaCap.Provisioned != nil {
			// Fallback to FBA capacity (for FBA arrays)
			info.UsableTotalTB = srp.FbaCap.Provisioned.UsableTotInTB

			// Extract used capacity from nested effective section
			if srp.FbaCap.Effective != nil {
				info.UsableUsedTB = srp.FbaCap.Effective.UsedTB
				info.EffectiveUsedCapacityPercent = srp.FbaCap.Effective.EffectiveUsedPercent
			} else {
				// Fallback if effective section not available
				info.UsableUsedTB = 0.0
				info.EffectiveUsedCapacityPercent = 0.0
			}

			// Extract snapshot capacity from nested snapshot section
			if srp.FbaCap.Snapshot != nil {
				info.SnapshotTB = srp.FbaCap.Snapshot.ResourceUsedTB
			}

			// Extract subscribed capacity from provisioned section
			info.SubscribedTB = srp.FbaCap.Provisioned.ProvisionedTB
		} else if srp.CkdCap != nil && srp.CkdCap.Provisioned != nil {
			// Fallback to CKD capacity (for CKD/mainframe arrays)
			info.UsableTotalTB = srp.CkdCap.Provisioned.UsableTotInTB
			info.UsableUsedTB = srp.CkdCap.Provisioned.UsableUsedInTB
			info.EffectiveUsedCapacityPercent = float64(srp.EffectiveUsedCapPerc)
		} else {
			// If no capacity data available, use EffectiveUsedCapPerc from top level
			info.EffectiveUsedCapacityPercent = float64(srp.EffectiveUsedCapPerc)
		}

		result = append(result, info)
	}
	return result, nil
}

// PmaxVolumeAdapter adapts pmax.Pmax to VolumeClient.
type PmaxVolumeAdapter struct {
	Client        pmax.Pmax
	ArrayID       string
	ClusterPrefix string
	Validator     VolumeValidator
	runtime       *MetricsRuntime
}

// NewPmaxVolumeAdapterWithRuntime creates a PmaxVolumeAdapter with MetricsRuntime support.
func NewPmaxVolumeAdapterWithRuntime(client pmax.Pmax, arrayID, clusterPrefix string, validator VolumeValidator, runtime *MetricsRuntime) *PmaxVolumeAdapter {
	return &PmaxVolumeAdapter{
		Client:        client,
		ArrayID:       arrayID,
		ClusterPrefix: clusterPrefix,
		Validator:     validator,
		runtime:       runtime,
	}
}

func (a *PmaxVolumeAdapter) GetVolumes(ctx context.Context) ([]VolumeInfo, error) {
	// Refresh validator cache if available
	skipValidation := false
	if a.Validator != nil {
		if err := a.Validator.RefreshCache(ctx); err != nil {
			skipValidation = true
		}
	}

	var mvList []string
	var err error

	// Use MetricsRuntime if available for resilience features
	if a.runtime != nil {
		cacheKey := fmt.Sprintf("mvlist_%s", a.ArrayID)
		result, rtErr := a.runtime.Do(ctx, "volumes", cacheKey, func(callCtx context.Context) (any, error) {
			return a.Client.GetMaskingViewList(callCtx, a.ArrayID)
		})
		if rtErr != nil {
			return nil, rtErr
		}
		mvListResponse := result.(*v100.MaskingViewList)
		mvList = mvListResponse.MaskingViewIDs
	} else {
		mvListResponse, err := a.Client.GetMaskingViewList(ctx, a.ArrayID)
		if err != nil {
			return nil, err
		}
		mvList = mvListResponse.MaskingViewIDs
	}

	// Build map of VolumeID -> list of HostNames from Masking Views
	// This is used for the host label but NOT for determining attachment status.
	volHostMap := make(map[string][]string)
	for _, mvName := range mvList {
		mv, err := a.Client.GetMaskingViewByID(ctx, a.ArrayID, mvName)
		if err != nil {
			continue
		}
		conns, err := a.Client.GetMaskingViewConnections(ctx, a.ArrayID, mvName, "")
		if err == nil {
			for _, conn := range conns {
				volHostMap[conn.VolumeID] = append(volHostMap[conn.VolumeID], mv.HostID)
			}
		}
	}

	volList, err := a.Client.GetVolumeIDList(ctx, a.ArrayID, "", false)
	if err != nil {
		return nil, err
	}
	var result []VolumeInfo
	for _, volID := range volList {
		v, err := a.Client.GetVolumeByID(ctx, a.ArrayID, volID)
		if err != nil {
			continue
		}
		// Apply PV validation filter only
		if v.VolumeIdentifier == "" {
			continue
		}

		// Apply PV validation if available
		if !skipValidation && a.Validator != nil {
			// Use the hex volume ID (v.VolumeID) for PV validation
			// This matches what the validator extracts from the PV's volumeHandle
			volumeID := v.VolumeID
			isManaged, err := a.Validator.IsDriverManaged(ctx, volumeID)
			if err != nil || !isManaged {
				continue
			}
		}
		sg := "none"
		if len(v.StorageGroupIDList) > 0 {
			sg = v.StorageGroupIDList[0]
		}
		sizeBytes := v.CapacityGB * 1024 * 1024 * 1024
		if sizeBytes == 0 {
			sizeBytes = float64(v.CapacityCYL) * 1920 * 512
		}

		// Determine attachment status from NumberOfFrontEndPaths (reliable).
		// This is a direct property of the Volume object from Unisphere
		// and is always accurate, unlike masking view connection lookups
		// which can fail silently.
		attached := v.NumberOfFrontEndPaths > 0

		// Determine health status based on volume status
		// PowerMax volume status: Ready, Not Ready, Mixed, Write Disabled, etc.
		healthy := v.Status == "Ready"

		hosts := volHostMap[v.VolumeID]
		if len(hosts) == 0 {
			result = append(result, VolumeInfo{
				VolumeID:       v.VolumeID,
				StorageGroupID: sg,
				SizeBytes:      sizeBytes,
				Attached:       attached,
				HostName:       "",
				Healthy:        healthy,
			})
		} else {
			// Deduplicate hosts for this volume
			seenHosts := make(map[string]bool)
			for _, h := range hosts {
				if !seenHosts[h] {
					seenHosts[h] = true
					result = append(result, VolumeInfo{
						VolumeID:       v.VolumeID,
						StorageGroupID: sg,
						SizeBytes:      sizeBytes,
						Attached:       attached,
						HostName:       h,
						Healthy:        healthy,
					})
				}
			}
		}
	}
	return result, nil
}

// PmaxMVAdapter adapts pmax.Pmax to MaskingViewClient.
type PmaxMVAdapter struct {
	Client        pmax.Pmax
	ArrayID       string
	ClusterPrefix string
	Validator     VolumeValidator
}

func (a *PmaxMVAdapter) GetMaskingViews(ctx context.Context) ([]MaskingViewInfo, error) {
	// Refresh validator cache if available
	skipValidation := false
	if a.Validator != nil {
		if err := a.Validator.RefreshCache(ctx); err != nil {
			skipValidation = true
		}
	}

	mvList, err := a.Client.GetMaskingViewList(ctx, a.ArrayID)
	if err != nil {
		return nil, err
	}
	var result []MaskingViewInfo
	for _, mvName := range mvList.MaskingViewIDs {
		mv, err := a.Client.GetMaskingViewByID(ctx, a.ArrayID, mvName)
		if err != nil {
			continue
		}

		// Count PV-validated volumes in this masking view and track by protocol
		volumeCount := 0
		protocolCounts := make(map[string]int)
		conns, err := a.Client.GetMaskingViewConnections(ctx, a.ArrayID, mvName, "")
		connected := false
		if err == nil && len(conns) > 0 {
			// Track unique volumes in this masking view
			seenVolumes := make(map[string]bool)

			for _, conn := range conns {
				// Check connection health
				if conn.LoggedIn || conn.OnFabric {
					connected = true
				}

				// Count PV-validated volumes and track protocol
				if !seenVolumes[conn.VolumeID] {
					seenVolumes[conn.VolumeID] = true

					// Get volume and validate with PV metadata
					v, err := a.Client.GetVolumeByID(ctx, a.ArrayID, conn.VolumeID)
					if err == nil && v.VolumeIdentifier != "" {
						// Extract protocol from volume identifier
						protocol := extractProtocolFromIdentifier(v.VolumeIdentifier)

						// Apply PV validation if available
						if !skipValidation && a.Validator != nil {
							// Use the hex volume ID (v.VolumeID) for PV validation
							// This matches what the validator extracts from the PV's volumeHandle
							volumeID := v.VolumeID
							isManaged, err := a.Validator.IsDriverManaged(ctx, volumeID)
							if err == nil && isManaged {
								volumeCount++
								protocolCounts[protocol]++
							}
						} else {
							// Without validation, count all volumes
							volumeCount++
							protocolCounts[protocol]++
						}
					}
				}
			}

			// If no connections are explicitly logged in or on fabric,
			// but we have connections, consider it healthy anyway
			if !connected && len(conns) > 0 {
				connected = true
			}
		}

		// Only include masking views that have at least one PV-validated volume
		if volumeCount > 0 {
			result = append(result, MaskingViewInfo{
				MaskingViewID:    mv.MaskingViewID,
				StorageGroupID:   mv.StorageGroupID,
				PortGroupID:      mv.PortGroupID,
				InitiatorGroupID: mv.HostID,
				Connected:        connected,
				VolumeCount:      volumeCount,
				ProtocolCounts:   protocolCounts,
			})
		}
	}
	return result, nil
}
