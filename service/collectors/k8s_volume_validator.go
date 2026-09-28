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
	"sync"

	"github.com/dell/csmlog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// K8sVolumeValidator validates volumes against Kubernetes PVs
type K8sVolumeValidator struct {
	k8sClient     kubernetes.Interface
	driverName    string
	volumeIDCache map[string]bool // PowerMax volume ID -> is driver managed
	cacheMu       sync.RWMutex
}

// NewK8sVolumeValidator creates a new K8sVolumeValidator
func NewK8sVolumeValidator(k8sClient kubernetes.Interface, driverName string) *K8sVolumeValidator {
	return &K8sVolumeValidator{
		k8sClient:     k8sClient,
		driverName:    driverName,
		volumeIDCache: make(map[string]bool),
	}
}

// IsDriverManaged checks if a volume is managed by the CSI driver
func (v *K8sVolumeValidator) IsDriverManaged(_ context.Context, volumeID string) (bool, error) {
	if v.k8sClient == nil {
		return false, fmt.Errorf("K8sVolumeValidator: kubernetes client not initialized")
	}

	if volumeID == "" {
		return false, nil
	}

	// Check cache
	v.cacheMu.RLock()
	cached, exists := v.volumeIDCache[volumeID]
	v.cacheMu.RUnlock()

	if exists {
		return cached, nil
	}

	// If not in cache, it's not managed by our driver
	return false, nil
}

// RefreshCache fetches all PVs and populates the cache with driver-managed volumes
func (v *K8sVolumeValidator) RefreshCache(ctx context.Context) error {
	// Fetch all PVs
	pvList, err := v.k8sClient.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("K8sVolumeValidator: failed to list PVs: %w", err)
	}

	csmlog.Infof("K8sVolumeValidator: fetched %d PVs from Kubernetes, driverName=%s", len(pvList.Items), v.driverName)

	// Build new cache with only driver-managed volume IDs
	newCache := make(map[string]bool)
	driverManagedCount := 0
	for _, pv := range pvList.Items {
		if pv.Spec.CSI != nil && pv.Spec.CSI.Driver == v.driverName {
			// Extract PowerMax volume ID from volumeHandle
			// volumeHandle format: "volumeID-symID-protocol/arrayID" or variations
			volumeHandle := pv.Spec.CSI.VolumeHandle
			if volumeHandle == "" {
				csmlog.Warnf("K8sVolumeValidator: PV %s has empty volumeHandle, skipping", pv.Name)
				continue
			}

			// Extract volume ID
			volumeID := extractPowerMaxVolumeID(volumeHandle)
			if volumeID == "" {
				csmlog.Warnf("K8sVolumeValidator: failed to extract volume ID from volumeHandle %s for PV %s", volumeHandle, pv.Name)
				continue
			}

			newCache[volumeID] = true
			driverManagedCount++
			csmlog.Debugf("K8sVolumeValidator: cached volume ID %s from PV %s (volumeHandle=%s)", volumeID, pv.Name, volumeHandle)
		}
	}

	csmlog.Infof("K8sVolumeValidator: cached %d driver-managed volume IDs from %d driver-managed PVs", len(newCache), driverManagedCount)

	// Update cache atomically
	v.cacheMu.Lock()
	v.volumeIDCache = newCache
	v.cacheMu.Unlock()

	return nil
}

// extractPowerMaxVolumeID extracts the PowerMax volume ID from the volumeHandle
// PowerMax volumeHandle format examples:
// - "csi-CSM-csivol-<uuid>-default-<symID>-<hexVolumeID>"
// - "csivol-<uuid>-<symID>-<protocol>/<symID>"
// - "csivol-<uuid>"
// For PowerMax, we extract the hex volume ID (last part before "/" if present)
func extractPowerMaxVolumeID(volumeHandle string) string {
	if volumeHandle == "" {
		return ""
	}

	// Split on "/" to remove array ID suffix if present
	parts := strings.Split(volumeHandle, "/")
	volumePart := parts[0]

	// PowerMax volumeHandle format: csi-CSM-csivol-<uuid>-<namespace>-<symID>-<hexVolumeID>
	// We want to extract the hex volume ID (last part after the last "-")
	idParts := strings.Split(volumePart, "-")
	if len(idParts) >= 2 {
		// Return the last part (hex volume ID)
		return idParts[len(idParts)-1]
	}

	// If format doesn't match expected pattern, return the whole volume part
	return volumePart
}
