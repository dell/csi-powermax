// Copyright © 2024-2026 Dell Inc. or its subsidiaries. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//      http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dell/gofsutil"
	pmax "github.com/dell/gopowermax/v2"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/tools/record"
)

// ---- Annotation key constants ----

const (
	// LabelPrefix is the prefix for space reclamation PVC labels.
	LabelPrefix = "space-reclamation.csi.dell.com/"
	// LabelEnabled controls per-PVC opt-in/opt-out via labels.
	LabelEnabled = LabelPrefix + "enabled"
	// LabelBlockReclaim controls raw block PV reclamation opt-in.
	LabelBlockReclaim = LabelPrefix + "block-reclaim"

	// AnnotationPrefix is the prefix for space reclamation PVC annotations (for result metadata).
	AnnotationPrefix = "space-reclamation.csi.dell.com/"
	// AnnotationLastRunTime records the last reclamation timestamp.
	AnnotationLastRunTime = AnnotationPrefix + "last-run-time"
	// AnnotationBytesAvailable records bytes available after reclamation.
	AnnotationBytesAvailable = AnnotationPrefix + "bytes-available"
	// AnnotationDuration records the reclamation duration in seconds.
	AnnotationDuration = AnnotationPrefix + "duration-seconds"
	// AnnotationStatus records the reclamation status.
	AnnotationStatus = AnnotationPrefix + "status"
	// AnnotationErrorMsg records any error message.
	AnnotationErrorMsg = AnnotationPrefix + "error-message"
	// AnnotationNode records the node where reclamation ran.
	AnnotationNode = AnnotationPrefix + "node"
)

// ---- Event reason constants ----

const (
	// EventReasonCompleted is the event reason for successful reclamation.
	EventReasonCompleted = "SpaceReclamationCompleted"
	// EventReasonFailed is the event reason for failed reclamation.
	EventReasonFailed = "SpaceReclamationFailed"
	// EventReasonTimeout is the event reason for timed-out reclamation.
	EventReasonTimeout = "SpaceReclamationTimeout"
	// EventReasonUnsupported is the event reason for unsupported devices.
	EventReasonUnsupported = "SpaceReclamationUnsupported"
)

// ---- Volume mode constants ----

// VolumeMode distinguishes filesystem from raw block volumes.
type VolumeMode corev1.PersistentVolumeMode

// Volume mode constants
const (
	VolumeModeFilesystem VolumeMode = VolumeMode(corev1.PersistentVolumeFilesystem)
	VolumeModeBlock      VolumeMode = VolumeMode(corev1.PersistentVolumeBlock)
)

// ---- Configuration ----

// SpaceReclamationConfig holds configuration for the space reclamation feature.
type SpaceReclamationConfig struct {
	// Enabled gates the entire subsystem.
	Enabled bool
	// Schedule is a cron expression (5-field). Default: "0 2 * * 0".
	Schedule string
	// MaxConcurrentVolumes is the max parallel reclamation jobs per node. Default: 2.
	MaxConcurrentVolumes int
	// TimeoutSeconds is the per-volume timeout. Default: 14400.
	TimeoutSeconds int
	// NodeName is the Kubernetes node name (from downward API or env var).
	NodeName string
}

// getEnvString reads an environment variable and returns a default if unset or empty.
func getEnvString(key, defaultVal string) string {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	return val
}

// getEnvBool reads an environment variable as a boolean, returning a default on error or empty.
func getEnvBool(key string, defaultVal bool) bool {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	b, err := strconv.ParseBool(val)
	if err != nil {
		return defaultVal
	}
	return b
}

// getEnvInt reads an environment variable as an int, returning a default on error, empty, or negative.
func getEnvInt(key string, defaultVal int) int {
	val := os.Getenv(key)
	if val == "" {
		return defaultVal
	}
	i, err := strconv.Atoi(val)
	if err != nil {
		return defaultVal
	}
	if i < 0 {
		return defaultVal
	}
	return i
}

// ReadSpaceReclamationConfig reads configuration from environment variables.
func ReadSpaceReclamationConfig() SpaceReclamationConfig {
	cfg := SpaceReclamationConfig{
		Enabled:              getEnvBool(EnvSpaceReclamationEnabled, false),
		Schedule:             getEnvString(EnvSpaceReclamationSchedule, "0 2 * * 0"),
		MaxConcurrentVolumes: getEnvInt(EnvSpaceReclamationMaxConcurrent, 2),
		TimeoutSeconds:       getEnvInt(EnvSpaceReclamationTimeout, 14400),
		NodeName:             getEnvString(EnvNodeName, ""),
	}
	return cfg
}

// ---- Volume Info ----

// VolumeInfo stores metadata about a staged volume for reclamation.
type VolumeInfo struct {
	VolumeID     string
	StagingPath  string     // Mount point for filesystem PVs; device path for block PVs
	DevicePath   string     // Underlying block device (e.g., /dev/sda, /dev/dm-0)
	VolumeMode   VolumeMode // Filesystem or Block
	PVCName      string
	PVCNamespace string
	PVC          *corev1.PersistentVolumeClaim // PVC object (fetched in RunOnce, reused in reclaimVolume)
}

// ---- Reclamation Result ----

// ReclamationResult represents the outcome of a reclamation operation.
type ReclamationResult struct {
	Status         string // "success", "error", "timeout", "unsupported"
	BytesAvailable int64
	Duration       time.Duration
	ErrorMessage   string // populated on failure
	NodeName       string
}

// ---- PVC Annotator ----

// PVCAnnotator updates PVC annotations with reclamation results.
type PVCAnnotator struct {
	client   kubernetes.Interface
	maxRetry int
}

// NewPVCAnnotator creates a new PVCAnnotator.
func NewPVCAnnotator(client kubernetes.Interface) *PVCAnnotator {
	return &PVCAnnotator{
		client:   client,
		maxRetry: 3,
	}
}

// Annotate updates the PVC with reclamation result annotations.
// It handles 404 (PVC not found) and 409 (conflict, retry) responses.
func (a *PVCAnnotator) Annotate(ctx context.Context, pvcName, pvcNamespace string, result *ReclamationResult) error {
	var lastErr error
	for attempt := 0; attempt <= a.maxRetry; attempt++ {
		// GET the latest PVC
		pvc, err := a.client.CoreV1().PersistentVolumeClaims(pvcNamespace).Get(ctx, pvcName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get PVC %s/%s: %w", pvcNamespace, pvcName, err)
		}

		// Merge annotations
		if pvc.Annotations == nil {
			pvc.Annotations = make(map[string]string)
		}
		pvc.Annotations[AnnotationStatus] = result.Status
		pvc.Annotations[AnnotationLastRunTime] = time.Now().UTC().Format(time.RFC3339)
		pvc.Annotations[AnnotationBytesAvailable] = strconv.FormatInt(result.BytesAvailable, 10)
		pvc.Annotations[AnnotationDuration] = fmt.Sprintf("%.3f", result.Duration.Seconds())
		pvc.Annotations[AnnotationNode] = result.NodeName
		if result.ErrorMessage != "" {
			pvc.Annotations[AnnotationErrorMsg] = result.ErrorMessage
		} else {
			// Clear error message on success to remove stale error states
			delete(pvc.Annotations, AnnotationErrorMsg)
		}

		// UPDATE the PVC
		_, err = a.client.CoreV1().PersistentVolumeClaims(pvcNamespace).Update(ctx, pvc, metav1.UpdateOptions{})
		if err == nil {
			return nil
		}
		lastErr = err
		// Retry on conflict (409)
		if strings.Contains(err.Error(), "the object has been modified") || strings.Contains(err.Error(), "Conflict") {
			continue
		}
		return fmt.Errorf("failed to update PVC %s/%s: %w", pvcNamespace, pvcName, err)
	}
	return lastErr
}

// ---- Event Emitter ----

// EventEmitter creates Kubernetes Events on PVCs.
type EventEmitter struct {
	recorder record.EventRecorder
}

// NewEventEmitter creates a new EventEmitter with a Kubernetes event recorder.
func NewEventEmitter(clientset kubernetes.Interface, driverName string) *EventEmitter {
	if clientset == nil {
		return &EventEmitter{}
	}
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{
		Interface: clientset.CoreV1().Events(""),
	})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: driverName})
	return &EventEmitter{recorder: recorder}
}

// EmitSuccess records a successful reclamation event on the PVC.
func (e *EventEmitter) EmitSuccess(pvc *corev1.PersistentVolumeClaim, bytesAvailable int64) {
	if e.recorder == nil {
		return
	}
	msg := fmt.Sprintf("Space reclamation completed: %d bytes available", bytesAvailable)
	e.recorder.Event(pvc, corev1.EventTypeNormal, EventReasonCompleted, msg)
}

// EmitFailure records a failed reclamation event on the PVC.
func (e *EventEmitter) EmitFailure(pvc *corev1.PersistentVolumeClaim, err error) {
	if e.recorder == nil {
		return
	}
	msg := fmt.Sprintf("Space reclamation failed: %v", err)
	e.recorder.Event(pvc, corev1.EventTypeWarning, EventReasonFailed, msg)
}

// EmitTimeout records a timed-out reclamation event on the PVC.
func (e *EventEmitter) EmitTimeout(pvc *corev1.PersistentVolumeClaim, timeout time.Duration) {
	if e.recorder == nil {
		return
	}
	msg := fmt.Sprintf("Space reclamation timed out after %v", timeout)
	e.recorder.Event(pvc, corev1.EventTypeWarning, EventReasonTimeout, msg)
}

// EmitUnsupported records an unsupported-device reclamation event on the PVC.
func (e *EventEmitter) EmitUnsupported(pvc *corev1.PersistentVolumeClaim, reason string) {
	if e.recorder == nil {
		return
	}
	msg := fmt.Sprintf("Device does not support space reclamation: %s", reason)
	e.recorder.Event(pvc, corev1.EventTypeWarning, EventReasonUnsupported, msg)
}

// ---- Eligibility ----

// IsEligible determines if a volume is eligible for reclamation based on
// global config and per-PVC labels.
// Returns (eligible, reason) where reason explains why not eligible (empty if eligible).
func IsEligible(globalEnabled bool, labels map[string]string, volumeMode VolumeMode) (bool, string) {
	// Block mode requires explicit opt-in via label
	if volumeMode == VolumeModeBlock {
		if labels == nil {
			return false, "block mode missing required label"
		}
		val, ok := labels[LabelBlockReclaim]
		if !ok {
			return false, "block mode missing required label"
		}
		if !strings.EqualFold(val, "true") {
			return false, fmt.Sprintf("block mode label is '%s' (must be 'true')", val)
		}
		return true, ""
	}

	// Filesystem mode: explicit label takes precedence, otherwise follow global config
	if labels == nil {
		if globalEnabled {
			return true, ""
		}
		return false, "global disabled"
	}
	val, ok := labels[LabelEnabled]
	if !ok {
		if globalEnabled {
			return true, ""
		}
		return false, "global disabled"
	}
	if strings.EqualFold(val, "true") {
		return true, ""
	}
	return false, fmt.Sprintf("label is '%s' (must be 'true' to override global)", val)
}

// ---- Injectable function variables (overridable in tests) ----

// wwnToDevicePathFunc is overridable in tests to avoid real /dev/disk/by-id lookups.
var wwnToDevicePathFunc = func(ctx context.Context, wwn string) (string, string, error) {
	return gofsutil.WWNToDevicePathX(ctx, wwn)
}

// checkDiscardCapabilityFunc checks if a device supports discard operations.
var checkDiscardCapabilityFunc = func(ctx context.Context, devicePath string) (supported bool, maxBytes int64, reason string) {
	discardCap, err := gofsutil.CheckDiscardSupport(ctx, devicePath)
	if err != nil {
		return false, 0, fmt.Sprintf("failed to check discard support: %v", err)
	}
	if !discardCap.Supported {
		return false, discardCap.DiscardMaxBytes, discardCap.Reason
	}
	return true, discardCap.DiscardMaxBytes, ""
}

// getVolumeWWNFunc is overridable in tests to avoid real PowerMax API calls.
// Default implementation calls the manager's getVolumeWWN method.
var getVolumeWWNFunc = func(m *SpaceReclamationManager, ctx context.Context, pvName, volumeHandle string) (symID, devID, wwn string, err error) {
	return m.getVolumeWWN(ctx, pvName, volumeHandle)
}

// normalizeDevicePathFunc resolves symlinks to get the canonical device path.
// This handles cases where mount table shows /dev/mapper/mpatha but WWN resolution returns /dev/dm-1.
// Returns the original path if symlink resolution fails. Overridable in tests.
var normalizeDevicePathFunc = func(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

// normalizeDevicePath is a convenience wrapper around normalizeDevicePathFunc.
func normalizeDevicePath(path string) string {
	return normalizeDevicePathFunc(path)
}

// ---- Space Reclamation Manager ----

// SpaceReclamationManager orchestrates periodic space reclamation on staged volumes.
type SpaceReclamationManager struct {
	config      SpaceReclamationConfig
	annotator   *PVCAnnotator
	emitter     *EventEmitter
	k8sClient   kubernetes.Interface
	semaphore   chan struct{}
	volumeLocks sync.Map
	ctx         context.Context
	cronSched   *cron.Cron
	running     atomic.Bool // Flag to prevent overlapping RunOnce cycles
	svc         *service    // Reference to service for PowerMax client access
}

// NewSpaceReclamationManager creates a new SpaceReclamationManager.
// Returns error if the cron schedule expression is invalid.
func NewSpaceReclamationManager(
	ctx context.Context,
	config SpaceReclamationConfig,
	k8sClient kubernetes.Interface,
	nodeName string,
	svc *service,
) (*SpaceReclamationManager, error) {
	// Validate the cron expression by attempting to parse it
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	_, err := parser.Parse(config.Schedule)
	if err != nil {
		return nil, fmt.Errorf("invalid cron schedule %q: %w", config.Schedule, err)
	}

	config.NodeName = nodeName

	semSize := config.MaxConcurrentVolumes
	if semSize <= 0 {
		semSize = 1
	}

	mgr := &SpaceReclamationManager{
		config:    config,
		annotator: NewPVCAnnotator(k8sClient),
		emitter:   NewEventEmitter(k8sClient, Name),
		k8sClient: k8sClient,
		semaphore: make(chan struct{}, semSize),
		ctx:       ctx,
		svc:       svc,
	}
	return mgr, nil
}

// Start begins the cron-based reclamation scheduler.
func (m *SpaceReclamationManager) Start() error {
	m.cronSched = cron.New(cron.WithParser(cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)))
	log.Info("SpaceReclamation: cron scheduler created")
	_, err := m.cronSched.AddFunc(m.config.Schedule, m.RunOnce)
	if err != nil {
		log.Errorf("SpaceReclamation: failed to add cron job: %v", err)
		return fmt.Errorf("failed to add cron job: %w", err)
	}
	log.Info("SpaceReclamation: cron job added")
	m.cronSched.Start()
	log.Infof("SpaceReclamation: scheduler running with schedule %q", m.config.Schedule)
	return nil
}

// Stop halts the cron scheduler.
func (m *SpaceReclamationManager) Stop() {
	if m.cronSched != nil {
		m.cronSched.Stop()
	}
}

// buildDeviceToMountMap builds a map of device paths to mount paths from the system mount table.
// It filters to only CSI-related mounts and prefers private mounts over pod mount paths.
// For filesystem volumes, the PowerMax driver mounts devices to /var/lib/kubelet/plugins/powermax.emc.dell.com/disks/<volume-id> (private mount)
// and then bind-mounts to pod paths. The private mount is more stable and persists even when pods are recreated.
// Device paths are normalized by resolving symlinks to handle /dev/mapper/mpatha vs /dev/dm-1 differences.
func (m *SpaceReclamationManager) buildDeviceToMountMap(ctx context.Context) (map[string]string, error) {
	mounts, err := gofsutil.GetMounts(ctx)
	if err != nil {
		log.Errorf("SpaceReclamation: failed to get mounts: %v", err)
		return nil, fmt.Errorf("failed to get mounts: %w", err)
	}
	log.Infof("SpaceReclamation: found %d total mounts", len(mounts))

	deviceToMount := make(map[string]string, len(mounts))
	for _, mnt := range mounts {
		// Filter to only CSI-related mounts:
		// 1. Pod mounts: /var/lib/kubelet/pods/...
		// 2. Private mounts: /var/lib/kubelet/plugins/powermax.emc.dell.com/disks/...
		isPodMount := strings.Contains(mnt.Path, "/var/lib/kubelet/pods/")
		isPrivateMount := strings.Contains(mnt.Path, "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/")

		if !isPodMount && !isPrivateMount {
			continue
		}

		log.Infof("SpaceReclamation: CSI mount - Device: %s, Path: %s", mnt.Device, mnt.Path)

		// Normalize device path by resolving symlinks (e.g., /dev/mapper/mpatha -> /dev/dm-1)
		// This ensures consistent comparison with WWN-resolved device paths
		normalizedDevice := normalizeDevicePath(mnt.Device)

		// Prefer private mounts (/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/) over pod mount paths (/pods/)
		// Private mounts are more stable and persist even when pods are recreated
		currentPath, exists := deviceToMount[normalizedDevice]
		if !exists {
			deviceToMount[normalizedDevice] = mnt.Path
		} else if isPrivateMount && !strings.Contains(currentPath, "/var/lib/kubelet/plugins/powermax.emc.dell.com/disks/") {
			// Replace with private mount if current is not a private mount
			deviceToMount[normalizedDevice] = mnt.Path
		}
	}
	return deviceToMount, nil
}

// shouldSkipPV checks if a PV should be skipped based on basic filters.
// Returns (shouldSkip, reason).
func (m *SpaceReclamationManager) shouldSkipPV(pv *corev1.PersistentVolume) (bool, string) {
	// Filter: only process PVs managed by this driver
	if pv.Spec.CSI == nil || pv.Spec.CSI.Driver != Name {
		return true, "not managed by this driver"
	}
	// Filter: only process Bound PVs
	if pv.Status.Phase != corev1.VolumeBound {
		return true, "not bound"
	}
	// Skip RWX volumes - space reclamation not supported for multi-node access
	for _, accessMode := range pv.Spec.AccessModes {
		if accessMode == corev1.ReadWriteMany {
			return true, "SpaceReclamation: skipping RWX volume"
		}
	}
	// Skip NFS volumes - space reclamation is handled at the NFS server level
	if pv.Spec.CSI.FSType == "nfs" {
		return true, "NFS handled at server level"
	}
	if pv.Spec.ClaimRef == nil {
		return true, "no claim reference"
	}
	return false, ""
}

// getVolumeWWN retrieves the WWN for a PowerMax volume.
func (m *SpaceReclamationManager) getVolumeWWN(ctx context.Context, pvName, volumeHandle string) (symID, devID, wwn string, err error) {
	// Parse the CSI volume ID to get symID and devID
	_, symID, devID, _, _, err = m.parseCsiID(volumeHandle)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to parse volumeHandle %s: %w", volumeHandle, err)
	}
	log.Infof("SpaceReclamation: PV %s parsed to symID=%s, devID=%s", pvName, symID, devID)

	// Get PowerMax client and retrieve volume details to get WWN
	pmaxClient, err := m.getPowerMaxClient(symID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get PowerMax client for array %s: %w", symID, err)
	}
	log.Info("SpaceReclamation: PowerMax client successfully initialized")

	// Retrieve volume from PowerMax to get EffectiveWWN
	vol, err := pmaxClient.GetVolumeByID(ctx, symID, devID)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to get volume %s/%s from array: %w", symID, devID, err)
	}
	log.Infof("SpaceReclamation: Retrieved Volume Details: %v", vol)

	wwn = vol.EffectiveWWN
	if wwn == "" {
		return "", "", "", fmt.Errorf("volume %s/%s has no EffectiveWWN", symID, devID)
	}
	log.Infof("SpaceReclamation: PV %s retrieved WWN %s from PowerMax", pvName, wwn)
	return symID, devID, wwn, nil
}

// selectBestDevice selects the best device from a list based on protocol and mount status.
// For filesystem mode, it verifies the device is in the mount table.
func (m *SpaceReclamationManager) selectBestDevice(pvName, wwn string, devices []string, deviceToMount map[string]string, volMode VolumeMode) (string, error) {
	// First, try to find a device that's already mounted (for both filesystem and block)
	for _, dev := range devices {
		candidatePath := "/dev/" + dev
		// Normalize the candidate path to match the normalized keys in deviceToMount
		normalizedCandidate := normalizeDevicePath(candidatePath)
		if _, mounted := deviceToMount[normalizedCandidate]; mounted {
			log.Infof("SpaceReclamation: PV %s resolved to device %s (found in mount table)", pvName, candidatePath)
			return candidatePath, nil
		}
	}

	// For filesystem mode, if no mounted device found, fail
	if volMode == VolumeModeFilesystem {
		return "", fmt.Errorf("no device from WWN %s found in mount table", wwn)
	}

	// For block mode, select the best candidate even if not in mount table
	// (block devices don't appear in mount table with their actual device path)
	var namespaceDevice string
	// Single iteration to classify devices and find the best match
	var isNVMe bool
	var multipathDevice string
	var firstDevice string

	for _, dev := range devices {
		// Store first device for fallback
		if firstDevice == "" {
			firstDevice = dev
		}

		// Check for NVMe devices
		if strings.HasPrefix(dev, "nvme") {
			isNVMe = true
			// NVMe namespace devices have format nvme<ctrl>n<ns> (e.g., nvme0n4)
			// Controller devices have format nvme<ctrl>c<host>n<ns> (e.g., nvme0c0n4)
			if !strings.Contains(dev, "c") {
				namespaceDevice = dev
				break // Found NVMe namespace device, stop searching
			}
		}

		// Check for multipath devices (only relevant for non-NVMe)
		if !isNVMe && multipathDevice == "" && strings.HasPrefix(dev, "dm-") {
			multipathDevice = dev
		}
	}

	// Return NVMe namespace device if found
	if isNVMe {
		if namespaceDevice != "" {
			devicePath := "/dev/" + namespaceDevice
			log.Infof("SpaceReclamation: PV %s resolved to device %s (NVMe namespace device)", pvName, devicePath)
			return devicePath, nil
		}
		return "", fmt.Errorf("NVMe devices found but no namespace device (only controller devices)")
	}

	// For non-NVMe (FC/iSCSI), prefer multipath devices (dm-*) over single paths (sd*)
	if multipathDevice != "" {
		devicePath := "/dev/" + multipathDevice
		log.Infof("SpaceReclamation: PV %s resolved to device %s (multipath device)", pvName, devicePath)
		return devicePath, nil
	}

	// Fallback: use the first device
	devicePath := "/dev/" + firstDevice
	log.Infof("SpaceReclamation: PV %s resolved to device %s (first device)", pvName, devicePath)
	return devicePath, nil
}

// resolveDevicePath resolves the device path for a volume from its WWN.
// For filesystem mode, it ensures the device is in the mount table.
func (m *SpaceReclamationManager) resolveDevicePath(ctx context.Context, pvName, wwn string, deviceToMount map[string]string, volMode VolumeMode) (string, error) {
	// Try WWNToDevicePath first
	_, devicePath, err := wwnToDevicePathFunc(ctx, wwn)
	log.Infof("SpaceReclamation: WWNToDevicePath result: devicePath=%s, err=%v", devicePath, err)

	if err == nil && devicePath != "" {
		log.Infof("SpaceReclamation: PV %s resolved to device %s via WWN %s", pvName, devicePath, wwn)
		// Normalize the device path to handle symlinks (e.g., /dev/mapper/mpatha -> /dev/dm-1)
		normalizedDevicePath := normalizeDevicePath(devicePath)
		log.Infof("SpaceReclamation: PV %s normalized device path: %s -> %s", pvName, devicePath, normalizedDevicePath)

		// Verify the device is in mount table for filesystem mode
		if volMode == VolumeModeFilesystem {
			if mountPath, mounted := deviceToMount[normalizedDevicePath]; mounted {
				log.Infof("SpaceReclamation: PV %s found in mount table at %s", pvName, mountPath)
				// Return the original devicePath (not normalized) for consistency with other code
				return devicePath, nil
			}
			log.Warnf("SpaceReclamation: PV %s normalized device %s not in mount table, trying fallback", pvName, normalizedDevicePath)
			// Fall through to GetSysBlockDevicesForVolumeWWN
		} else {
			// Block mode: accept the device even if not in mount table
			return devicePath, nil
		}
	}

	// Fallback: try GetSysBlockDevicesForVolumeWWN
	log.Infof("SpaceReclamation: trying GetSysBlockDevicesForVolumeWWN for PV %s", pvName)
	devices, err := gofsutil.GetSysBlockDevicesForVolumeWWN(ctx, wwn)
	log.Infof("SpaceReclamation: GetSysBlockDevicesForVolumeWWN result: length(devices)=%d, err=%v", len(devices), err)
	if err != nil || len(devices) == 0 {
		return "", fmt.Errorf("PV not on this node (WWN: %s)", wwn)
	}

	return m.selectBestDevice(pvName, wwn, devices, deviceToMount, volMode)
}

// processVolume processes a single PV for space reclamation.
// Returns the VolumeInfo if successful, or nil if the volume should be skipped.
func (m *SpaceReclamationManager) processVolume(ctx context.Context, pv *corev1.PersistentVolume, deviceToMount map[string]string) (*VolumeInfo, error) {
	// Check basic PV filters
	if skip, reason := m.shouldSkipPV(pv); skip {
		log.Infof("SpaceReclamation: skipping PV %s (%s)", pv.Name, reason)
		return nil, nil
	}

	log.Infof("SpaceReclamation: processing PV %s with AccessModes: %v", pv.Name, pv.Spec.AccessModes)

	pvcRef := pv.Spec.ClaimRef

	// Get PVC and check eligibility
	pvc, err := m.k8sClient.CoreV1().PersistentVolumeClaims(pvcRef.Namespace).Get(ctx, pvcRef.Name, metav1.GetOptions{})
	if err != nil {
		log.Errorf("SpaceReclamation: failed to get PVC: %v", err)
		return nil, fmt.Errorf("failed to get PVC %s/%s: %w", pvcRef.Namespace, pvcRef.Name, err)
	}

	var volMode VolumeMode
	if pvc.Spec.VolumeMode != nil {
		volMode = VolumeMode(*pvc.Spec.VolumeMode)
	} else {
		volMode = VolumeModeFilesystem
	}

	log.Infof("SpaceReclamation: PV %s has VolumeMode: %s, Labels: %v", pv.Name, volMode, pvc.Labels)
	eligible, reason := IsEligible(m.config.Enabled, pvc.Labels, volMode)
	if !eligible {
		log.Infof("SpaceReclamation: PV %s is not eligible for reclamation (reason: %s)", pv.Name, reason)
		return nil, nil
	}
	log.Infof("SpaceReclamation: PV %s is eligible for reclamation", pv.Name)

	// Get volume handle
	volumeHandle := pv.Spec.CSI.VolumeHandle
	if volumeHandle == "" {
		return nil, fmt.Errorf("PV %s missing VolumeHandle", pv.Name)
	}
	log.Infof("SpaceReclamation: PV %s has VolumeHandle: %s", pv.Name, volumeHandle)

	// Get WWN from PowerMax (or test mock)
	symID, devID, wwn, err := getVolumeWWNFunc(m, ctx, pv.Name, volumeHandle)
	if err != nil {
		return nil, fmt.Errorf("failed to get WWN: %w", err)
	}
	_ = symID // Unused in current implementation but may be needed later
	_ = devID // Unused in current implementation but may be needed later

	// Resolve device path
	devicePath, err2 := m.resolveDevicePath(ctx, pv.Name, wwn, deviceToMount, volMode)
	if err2 != nil {
		return nil, fmt.Errorf("failed to resolve device path: %w", err2)
	}

	// Resolve staging path for filesystem mode
	var stagingPath string
	if volMode == VolumeModeFilesystem {
		log.Info("Found Filesystem Mode")
		if mountPath, mounted := deviceToMount[devicePath]; mounted {
			stagingPath = mountPath
			log.Infof("SpaceReclamation: PV %s filesystem mode, staging path: %s", pv.Name, stagingPath)
		} else {
			return nil, fmt.Errorf("filesystem mode but no mount found for device %s", devicePath)
		}
	} else {
		log.Info("Found Block Mode")
		log.Infof("SpaceReclamation: PV %s block mode, device path: %s", pv.Name, devicePath)
	}

	volInfo := &VolumeInfo{
		VolumeID:     volumeHandle,
		StagingPath:  stagingPath,
		DevicePath:   devicePath,
		VolumeMode:   volMode,
		PVCName:      pvcRef.Name,
		PVCNamespace: pvcRef.Namespace,
		PVC:          pvc,
	}

	if volMode == VolumeModeFilesystem {
		log.Infof("SpaceReclamation: submitting reclamation job for PV %s (VolumeID: %s, Device: %s, Path: %s, Mode: %s)",
			pv.Name, volInfo.VolumeID, devicePath, stagingPath, volMode)
	} else {
		log.Infof("SpaceReclamation: submitting reclamation job for PV %s (VolumeID: %s, Device: %s, Mode: %s)",
			pv.Name, volInfo.VolumeID, devicePath, volMode)
	}

	return volInfo, nil
}

// RunOnce executes one reclamation cycle using on-demand discovery.
// Called by the cron scheduler.
// It discovers eligible volumes by:
//  1. Listing all Bound PowerMax PVs from Kubernetes.
//  2. Checking live PVC labels for eligibility (fail fast, no device work for ineligible PVCs).
//  3. Resolving the device path from WWN using gofsutil.WWNToDevicePathX.
//  4. Resolving the mount path from gofsutil.GetMounts() to determine VolumeMode.
func (m *SpaceReclamationManager) RunOnce() {
	log.Info("SpaceReclamation: starting RunOnce cycle")

	// Prevent overlapping cycles
	if !m.running.CompareAndSwap(false, true) {
		log.Warn("SpaceReclamation: previous scheduled run is still in progress, skipping this cycle")
		return
	}
	defer m.running.Store(false)

	// Create a job-level timeout context for the entire reclamation cycle
	timeout := time.Duration(m.config.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	// List all PVs in the cluster
	pvList, err := m.k8sClient.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err != nil {
		log.Errorf("SpaceReclamation: failed to list PersistentVolumes: %v", err)
		return
	}
	log.Infof("SpaceReclamation: found %d total PVs", len(pvList.Items))

	// Build device-to-mount map for O(1) lookup
	deviceToMount, err := m.buildDeviceToMountMap(ctx)
	if err != nil {
		log.Errorf("SpaceReclamation: %v", err)
		return
	}

	// Process each PV concurrently
	var wg sync.WaitGroup
	for i := range pvList.Items {
		pv := &pvList.Items[i]

		volInfo, err := m.processVolume(ctx, pv, deviceToMount)
		if err != nil {
			log.Warnf("SpaceReclamation: PV %s error: %v", pv.Name, err)
			continue
		}
		if volInfo == nil {
			// Volume was skipped (not eligible or filtered out)
			continue
		}

		// Submit reclamation job
		wg.Add(1)
		go func(v *VolumeInfo) {
			defer wg.Done()
			m.reclaimVolume(ctx, v)
		}(volInfo)
	}
	wg.Wait()
	log.Info("SpaceReclamation: completed RunOnce cycle")
}

// reclaimVolume performs space reclamation on a single volume.
func (m *SpaceReclamationManager) reclaimVolume(ctx context.Context, vol *VolumeInfo) {
	log.Infof("SpaceReclamation: starting reclamation for volume %s (PVC: %s/%s, Mode: %s, Device: %s, Path: %s)",
		vol.VolumeID, vol.PVCNamespace, vol.PVCName, vol.VolumeMode, vol.DevicePath, vol.StagingPath)

	// Acquire semaphore for concurrency control
	select {
	case m.semaphore <- struct{}{}:
		defer func() { <-m.semaphore }()
	case <-ctx.Done():
		return
	}

	// Acquire per-volume mutex to prevent duplicate jobs
	mu := &sync.Mutex{}
	actual, _ := m.volumeLocks.LoadOrStore(vol.VolumeID, mu)
	actualMu := actual.(*sync.Mutex)
	if !actualMu.TryLock() {
		log.Infof("SpaceReclamation: skipping duplicate job for volume %s (already in progress)", vol.VolumeID)
		return // Another reclamation is already running for this volume
	}
	defer actualMu.Unlock()

	// Check if device supports discard operations
	var supported bool
	var reason string
	supported, _, reason = checkDiscardCapabilityFunc(m.ctx, vol.DevicePath)
	log.Infof("SpaceReclamation: checked discard capability for device %s (supported: %v, reason: %s)", vol.DevicePath, supported, reason)
	if !supported {
		log.Infof("SpaceReclamation: volume %s does not support discard (device: %s, reason: %s)", vol.VolumeID, vol.DevicePath, reason)
		// Annotate as unsupported
		result := &ReclamationResult{
			Status:       "unsupported",
			ErrorMessage: reason,
			NodeName:     m.config.NodeName,
		}
		if m.annotator != nil && m.k8sClient != nil && vol.PVCName != "" {
			_ = m.annotator.Annotate(m.ctx, vol.PVCName, vol.PVCNamespace, result)
		}
		// Emit event for unsupported device
		if m.emitter != nil && vol.PVC != nil {
			m.emitter.EmitUnsupported(vol.PVC, reason)
		}
		return
	}

	var bytesAvailable int64
	var reclaimErr error
	start := time.Now()

	// Execute the reclamation operation
	switch vol.VolumeMode {
	case VolumeModeFilesystem:
		var fstrimResult *gofsutil.FstrimResult
		fstrimResult, reclaimErr = gofsutil.Fstrim(ctx, vol.StagingPath)
		if reclaimErr == nil && fstrimResult != nil {
			bytesAvailable = fstrimResult.BytesTrimmed
		}
		log.Infof("SpaceReclamation: fstrim on %s - %d bytes available", vol.StagingPath, bytesAvailable)
	case VolumeModeBlock:
		var blkResult *gofsutil.BlkdiscardResult
		blkResult, reclaimErr = gofsutil.Blkdiscard(ctx, vol.DevicePath)
		if reclaimErr == nil && blkResult != nil {
			bytesAvailable = blkResult.BytesDiscarded
		}
		log.Infof("SpaceReclamation: blkdiscard on %s - %d bytes available", vol.DevicePath, bytesAvailable)
	}

	duration := time.Since(start)

	// Build the result
	var result *ReclamationResult
	if reclaimErr != nil {
		if ctx.Err() == context.DeadlineExceeded {
			result = &ReclamationResult{
				Status:       "timeout",
				ErrorMessage: fmt.Sprintf("operation timed out after %v", time.Duration(m.config.TimeoutSeconds)*time.Second),
				NodeName:     m.config.NodeName,
				Duration:     duration,
			}
		} else {
			result = &ReclamationResult{
				Status:       "error",
				ErrorMessage: reclaimErr.Error(),
				NodeName:     m.config.NodeName,
				Duration:     duration,
			}
		}
	} else {
		result = &ReclamationResult{
			Status:         "success",
			BytesAvailable: bytesAvailable,
			Duration:       duration,
			NodeName:       m.config.NodeName,
		}
	}

	// Annotate the PVC with results
	// If ctx is already expired (e.g. after a timeout), the Kubernetes API calls inside
	// Annotate would immediately fail. Use a fresh context derived from the manager's
	// parent context so the annotation is always written regardless of reclamation outcome.
	annotateCtx := ctx
	if ctx.Err() != nil {
		var annotateCancel context.CancelFunc
		annotateCtx, annotateCancel = context.WithTimeout(m.ctx, 10*time.Second)
		defer annotateCancel()
	}
	if m.annotator != nil && m.k8sClient != nil && vol.PVCName != "" {
		_ = m.annotator.Annotate(annotateCtx, vol.PVCName, vol.PVCNamespace, result)
	}

	// Emit Kubernetes event based on result status
	if m.emitter != nil && vol.PVC != nil {
		switch result.Status {
		case "success":
			m.emitter.EmitSuccess(vol.PVC, result.BytesAvailable)
		case "timeout":
			m.emitter.EmitTimeout(vol.PVC, time.Duration(m.config.TimeoutSeconds)*time.Second)
		case "error":
			m.emitter.EmitFailure(vol.PVC, errors.New(result.ErrorMessage))
		}
	}

	log.Infof("SpaceReclamation: completed reclamation for volume %s (PVC: %s/%s) - Status: %s, BytesAvailable: %d, Duration: %v",
		vol.VolumeID, vol.PVCNamespace, vol.PVCName, result.Status, result.BytesAvailable, result.Duration)
}

// parseCsiID parses the CSI volume ID to extract volume name, array ID, and device ID.
func (m *SpaceReclamationManager) parseCsiID(csiID string) (volName string, arrayID string, devID string, remoteSymID, remoteVolID string, err error) {
	if m.svc == nil {
		return "", "", "", "", "", fmt.Errorf("service reference is nil")
	}
	return m.svc.parseCsiID(csiID)
}

// getPowerMaxClient returns a PowerMax client for the specified array.
func (m *SpaceReclamationManager) getPowerMaxClient(symID string) (pmax.Pmax, error) {
	if m.svc == nil {
		return nil, fmt.Errorf("service reference is nil")
	}
	return m.svc.GetPowerMaxClient(symID)
}

// initSpaceReclamation reads env and initializes the space reclamation manager on the service.
// This is called from BeforeServe when in node mode.
// The manager is always initialized to allow per-PVC label-based opt-in, even when globally disabled.
// The cfg.Enabled flag is used in the eligibility check (IsEligible) to determine which PVCs to process.
func initSpaceReclamation(ctx context.Context, s *service, k8sClient kubernetes.Interface) {
	cfg := ReadSpaceReclamationConfig()
	log.Infof("SpaceReclamation: initializing with config: %v", cfg)

	mgr, err := NewSpaceReclamationManager(ctx, cfg, k8sClient, cfg.NodeName, s)
	if err != nil {
		log.Errorf("Failed to create SpaceReclamationManager: %v", err)
		return
	}
	log.Infof("SpaceReclamation: created manager, starting...")
	if err := mgr.Start(); err != nil {
		log.Errorf("Failed to start SpaceReclamationManager: %v", err)
		return
	}
	if cfg.Enabled {
		log.Infof("SpaceReclamation: started successfully (globally enabled)")
	} else {
		log.Infof("SpaceReclamation: started successfully (globally disabled, per-PVC labels will be honored)")
	}
	s.spaceReclaimMgr = mgr
}
