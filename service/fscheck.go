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

package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/gofsutil"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
)

// PVC label keys for per-volume FS check overrides
const (
	PVCLabelFSCheckEnabled = "csi.dell.com/fs_check_enabled"
	PVCLabelFSCheckMode    = "csi.dell.com/fs_check_mode"
)

// getFSCheckerFunc is a variable to allow mocking in tests.
var getFSCheckerFunc = gofsutil.GetFSChecker

// getDevMountsFunc is a variable to allow mocking in tests.
var getDevMountsFunc = getDevMounts

// newEventRecorderFunc is a variable to allow mocking in tests.
var newEventRecorderFunc = newEventRecorder

// fsCheckConfig holds FS check settings and dependencies for a single volume publish
type fsCheckConfig struct {
	enabled  bool                    // global setting from X_CSI_FS_CHECK_ENABLED
	mode     string                  // global setting: "checkOnly" or "checkAndRepair"
	k8sUtils k8sutils.UtilsInterface // for lazy PVC lookup; nil-safe
}

// fsCheckPVCObserver bridges gofsutil FSCheckObserver events to K8s PVC events and logs
type fsCheckPVCObserver struct {
	pvcName       string
	pvcNamespace  string
	devicePath    string
	fsType        string
	volumeID      string
	sawTimeout    bool
	events        []string
	eventRecorder record.EventRecorder // nil-safe; skips event posting if nil
}

// OnEvent implements gofsutil.FSCheckObserver
func (o *fsCheckPVCObserver) OnEvent(message string) {
	o.events = append(o.events, message)

	if message == gofsutil.FSCheckTimedOutEvent || message == gofsutil.FSRepairTimedOutEvent {
		o.sawTimeout = true
	}

	log.Infof("FS check event: %s on %s (%s)", message, o.devicePath, o.fsType)

	// Post K8s event on PVC
	if o.eventRecorder == nil || o.pvcName == "" || o.pvcNamespace == "" {
		return
	}

	eventType, reason := mapFSCheckEventToK8s(message)
	pvcRef := &corev1.ObjectReference{
		Kind:      "PersistentVolumeClaim",
		Name:      o.pvcName,
		Namespace: o.pvcNamespace,
	}
	eventMessage := fmt.Sprintf("Volume %s device %s (%s): %s", o.volumeID, o.devicePath, o.fsType, message)
	o.eventRecorder.Event(pvcRef, eventType, reason, eventMessage)
}

// Ensure fsCheckPVCObserver implements gofsutil.FSCheckObserver at compile time
var _ gofsutil.FSCheckObserver = (*fsCheckPVCObserver)(nil)

var (
	cachedEventRecorder record.EventRecorder
	eventRecorderOnce   sync.Once
)

// newEventRecorder creates a Kubernetes EventRecorder. Replaceable for testing.
func newEventRecorder() (record.EventRecorder, error) {
	config, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get in-cluster config: %w", err)
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
	}

	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})

	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("failed to add scheme: %w", err)
	}

	return eventBroadcaster.NewRecorder(scheme, corev1.EventSource{Component: "csi-powermax-node"}), nil
}

func initEventRecorder() record.EventRecorder {
	eventRecorderOnce.Do(func() {
		recorder, err := newEventRecorderFunc()
		if err != nil {
			log.Warnf("Failed to initialize FS check event recorder: %v - PVC events will not be posted", err)
			return
		}
		cachedEventRecorder = recorder
	})
	return cachedEventRecorder
}

// isAccessModeReadOnlyOrMulti returns true if the access mode is read-only or multi-node
func isAccessModeReadOnlyOrMulti(accMode *csi.VolumeCapability_AccessMode) bool {
	if accMode == nil {
		return false
	}
	switch accMode.GetMode() {
	case csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
		csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER,
		csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER:
		return true
	}
	return false
}

var pvNameFromPathRegex = regexp.MustCompile(`/.*/pods/[^/]+/volumes/kubernetes\.io~csi/([^/]+)/mount`)

// parsePVNameFromTargetPath extracts the PV name from the target path.
// Expected format: <kubelet>/pods/<uid>/volumes/kubernetes.io~csi/<pv-name>/mount
func parsePVNameFromTargetPath(targetPath string) string {
	matches := pvNameFromPathRegex.FindStringSubmatch(targetPath)
	if len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// isSupportedFSType returns true if the filesystem type supports FS check
func isSupportedFSType(fsType string) bool {
	switch fsType {
	case "ext4", "ext3", "ext2", "xfs":
		return true
	}
	return false
}

// performFSCheck runs FS check/repair if conditions are met.
// PVC label lookup is deferred until after cheap preconditions pass (FR-3).
// Returns nil to proceed with mount, or a gRPC status error to abort.
func performFSCheck(
	ctx context.Context,
	sysDevice *Device,
	fsCfg *fsCheckConfig,
	accMode *csi.VolumeCapability_AccessMode,
	volumeID string,
	targetPath string,
) error {
	// 1. Nil config means feature is completely unavailable
	if fsCfg == nil {
		log.Info("Skipping FS check: feature disabled")
		return nil
	}

	// Note: We do NOT early-return for !fsCfg.enabled here because a PVC label
	// (csi.dell.com/fs_check_enabled=true) can override the global disabled setting (AC-6).
	// The global enabled flag is passed to resolvePVCOverrides at step 7, which determines
	// the final effective enabled state after consulting PVC labels.

	// 2. Skip for read-only or multi-node access modes (cheap)
	if isAccessModeReadOnlyOrMulti(accMode) {
		log.Info("Skipping FS check: read-only or multi-node access mode")
		return nil
	}

	// 3. Check existing filesystem type (cheap)
	existingFs, err := gofsutil.GetDiskFormat(ctx, sysDevice.FullPath)
	if err != nil {
		log.Warnf("Could not determine disk format for %s: %s. Skipping FS check.", sysDevice.FullPath, err)
		return nil
	}

	// 4. Skip if newly formatted
	if existingFs == "" {
		log.Info("Skipping FS check: newly formatted volume")
		return nil
	}

	// 5. Skip if unsupported FS type (ext2, ext3, ext4, xfs supported)
	if !isSupportedFSType(existingFs) {
		log.Warnf("Skipping FS check: unsupported filesystem type %q", existingFs)
		return nil
	}

	// 6. Skip if device already has mounts on this node (FR-2)
	devMnts, err := getDevMountsFunc(sysDevice)
	if err != nil {
		log.Warnf("Could not check mount status for %s: %s. Skipping FS check.", sysDevice.FullPath, err)
		return nil
	}
	if len(devMnts) > 0 {
		log.Infof("Skipping FS check: volume already mounted on this node at %s", devMnts[0].Path)
		return nil
	}

	// 7. Lazy PVC label lookup (expensive - only runs after all cheap checks pass)
	enabled, mode, pvcName, pvcNamespace := resolvePVCOverrides(ctx, fsCfg, volumeID, targetPath)
	if !enabled {
		log.Info("Skipping FS check: feature disabled (global or PVC override)")
		return nil
	}

	// 8. Initialize event recorder (singleton)
	eventRecorder := initEventRecorder()

	// 9. Create observer
	observer := &fsCheckPVCObserver{
		pvcName:       pvcName,
		pvcNamespace:  pvcNamespace,
		devicePath:    sysDevice.FullPath,
		fsType:        existingFs,
		volumeID:      volumeID,
		eventRecorder: eventRecorder,
	}

	// 10. Get FS checker
	checker, err := getFSCheckerFunc(sysDevice.FullPath, existingFs, observer)
	if err != nil {
		return fmt.Errorf("failed to create FS checker for %s (%s): %w", sysDevice.FullPath, existingFs, err)
	}

	// 11. Run check
	doRepair := mode == "checkAndRepair"
	log.Infof("Starting file system check on %s (%s), repair=%v", sysDevice.FullPath, existingFs, doRepair)

	if err := checker.Check(ctx, doRepair); err != nil {
		if observer.sawTimeout {
			msg := fmt.Sprintf("File system check timed out on device %s (%s). The operation will be retried.", sysDevice.FullPath, existingFs)
			log.Error(msg)
			return status.Error(codes.Aborted, msg)
		}

		msg := fmt.Sprintf("File system check failed on device %s (volume ID: %s, fs: %s): %s. Manual intervention required. Do not attempt to mount this volume until the file system has been repaired.",
			sysDevice.FullPath, volumeID, existingFs, err.Error())
		log.Error(msg)

		// Post extra PVC Warning event with actionable message (FR-4)
		if eventRecorder != nil && pvcName != "" && pvcNamespace != "" {
			pvcRef := &corev1.ObjectReference{
				Kind:      "PersistentVolumeClaim",
				Name:      pvcName,
				Namespace: pvcNamespace,
			}
			eventRecorder.Event(pvcRef, string(corev1.EventTypeWarning), "FSCheckFailed",
				fmt.Sprintf("File system on device %s (fs: %s) cannot be mounted safely. Manual intervention required.", sysDevice.FullPath, existingFs))
		}

		return status.Error(codes.Internal, msg)
	}

	log.Infof("FS check completed successfully on %s (%s)", sysDevice.FullPath, existingFs)
	return nil
}

// resolvePVCOverrides performs the lazy PVC label lookup and returns the effective settings.
// On any failure, it gracefully falls back to global settings (AC-17).
func resolvePVCOverrides(
	ctx context.Context,
	fsCfg *fsCheckConfig,
	volumeID string,
	targetPath string,
) (enabled bool, mode string, pvcName string, pvcNamespace string) {
	enabled = fsCfg.enabled
	mode = fsCfg.mode

	// Parse PV name from target path
	pvName := parsePVNameFromTargetPath(targetPath)
	if pvName == "" {
		log.Debugf("Could not extract PV name from target path %q; using global FS check config", targetPath)
		return
	}

	// Check k8sUtils availability
	if fsCfg.k8sUtils == nil {
		log.Debug("k8sUtils not available; using global FS check config")
		return
	}

	// Look up the PVC via the PV
	pvc, err := fsCfg.k8sUtils.GetPVCForVolume(ctx, pvName, volumeID)
	if err != nil {
		log.Warnf("Could not look up PVC for PV %s (volume %s): %s. Using global FS check config.", pvName, volumeID, err)
		return
	}
	if pvc == nil {
		log.Debugf("PVC not found for PV %s; using global FS check config", pvName)
		return
	}

	// Populate PVC info for event posting
	pvcName = pvc.Name
	pvcNamespace = pvc.Namespace

	// Apply PVC label overrides
	if pvc.Labels != nil {
		if val, ok := pvc.Labels[PVCLabelFSCheckEnabled]; ok {
			switch strings.ToLower(val) {
			case "true":
				enabled = true
			case "false":
				enabled = false
			default:
				log.Warnf("Invalid value %q for PVC label %s on %s/%s; ignoring",
					val, PVCLabelFSCheckEnabled, pvc.Namespace, pvc.Name)
			}
		}

		if enabled {
			if val, ok := pvc.Labels[PVCLabelFSCheckMode]; ok {
				switch strings.ToLower(val) {
				case "checkonly":
					mode = "checkOnly"
				case "checkandrepair":
					mode = "checkAndRepair"
				default:
					log.Warnf("Invalid value %q for PVC label %s on %s/%s; ignoring",
						val, PVCLabelFSCheckMode, pvc.Namespace, pvc.Name)
				}
			}
		}
	}

	log.Infof("Resolved FS check config for volume %s: enabled=%v, mode=%s, pvc=%s/%s",
		volumeID, enabled, mode, pvcNamespace, pvcName)
	return
}

// mapFSCheckEventToK8s maps a gofsutil observer event to K8s event type and reason
func mapFSCheckEventToK8s(message string) (eventType, reason string) {
	switch message {
	case gofsutil.StartedFSCheckEvent:
		return string(corev1.EventTypeNormal), "FSCheckStarted"
	case gofsutil.FoundNoErrorsEvent:
		return string(corev1.EventTypeNormal), "FSCheckSucceeded"
	case gofsutil.FinishedFSRepairEvent:
		return string(corev1.EventTypeNormal), "FSCheckRepaired"
	case gofsutil.FoundErrorsEvent:
		return string(corev1.EventTypeWarning), "FSCheckFailed"
	case gofsutil.StartFSRepairEvent:
		return string(corev1.EventTypeNormal), "FSRepairStarted"
	case gofsutil.FoundDirtyLogEvent:
		return string(corev1.EventTypeNormal), "FSCheckDirtyLog"
	case gofsutil.StartLogReplayEvent:
		return string(corev1.EventTypeNormal), "FSLogReplayStarted"
	case gofsutil.LogReplayDoneEvent:
		return string(corev1.EventTypeNormal), "FSLogReplayDone"
	case gofsutil.FSCheckTimedOutEvent:
		return string(corev1.EventTypeWarning), "FSCheckTimedOut"
	case gofsutil.FSRepairFailedEvent:
		return string(corev1.EventTypeWarning), "FSRepairFailed"
	case gofsutil.FSRepairTimedOutEvent:
		return string(corev1.EventTypeWarning), "FSRepairTimedOut"
	case gofsutil.LogReplayFailedEvent:
		return string(corev1.EventTypeWarning), "FSLogReplayFailed"
	case gofsutil.FSCheckFailedEvent:
		return string(corev1.EventTypeWarning), "FSCheckFailed"
	default:
		return string(corev1.EventTypeNormal), "FSCheckEvent"
	}
}
