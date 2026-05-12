package service

import (
	"context"
	"path"
	"strconv"
	"strings"

	pmax "github.com/dell/gopowermax/v2"
	"github.com/container-storage-interface/spec/lib/go/csi"
)

// ---------------------------------------------------------------------------
// Backend path selection
// ---------------------------------------------------------------------------

type backendPath int

const (
	backendLegacy backendPath = iota
	backendU4P104
)

// Unisphere API version constants. Use these instead of raw integers
// when branching on the API version so that every version-dependent
// check references a single, well-known constant.
const (
	APIVersion101 = 101
	APIVersion103 = 103
	APIVersion104 = 104
)

func (p backendPath) String() string {
	switch p {
	case backendLegacy:
		return "legacy"
	case backendU4P104:
		return "u4p104"
	default:
		return "unknown"
	}
}

var minU4PBackendVersion = []int{10, 4, 0, 4}

func parseUnisphereVersion(version string) ([]int, bool) {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" {
		return nil, false
	}
	trimmed = strings.TrimPrefix(strings.TrimPrefix(trimmed, "V"), "v")
	parts := strings.Split(trimmed, ".")
	if len(parts) < len(minU4PBackendVersion) {
		return nil, false
	}
	parsed := make([]int, len(parts))
	for i, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		parsed[i] = value
	}
	return parsed, true
}

func isVersionAtLeast(version string, minimum []int) bool {
	parsed, ok := parseUnisphereVersion(version)
	if !ok {
		return false
	}
	for i := 0; i < len(minimum); i++ {
		if parsed[i] > minimum[i] {
			return true
		}
		if parsed[i] < minimum[i] {
			return false
		}
	}
	return true
}

// selectBackendPath determines whether to use the 10.4 or legacy code path
// based on the Unisphere version and the operation profile. NFS,
// replication, and thick provisioning are not yet supported on the 10.4 path,
// so any request that involves them is routed to legacy regardless of the version.
func selectBackendPath(profile backendSelectionProfile, version string) backendPath {
	if profile.IsFile || profile.ReplicationEnabled || profile.IsThick {
		return backendLegacy
	}
	if isVersionAtLeast(version, minU4PBackendVersion) {
		return backendU4P104
	}
	return backendLegacy
}

// ---------------------------------------------------------------------------
// Selection profiles
// ---------------------------------------------------------------------------

type backendSelectionProfile struct {
	IsFile             bool
	HasContentSource   bool
	ReplicationEnabled bool
	ReplicationMode    string
	IsThick            bool
}

func createVolumeSelectionProfile(isFile bool, contentSource *csi.VolumeContentSource, replicationEnabled bool, repMode string, isThick bool) backendSelectionProfile {
	return backendSelectionProfile{
		IsFile:             isFile,
		HasContentSource:   contentSource != nil,
		ReplicationEnabled: replicationEnabled,
		ReplicationMode:    repMode,
		IsThick:            isThick,
	}
}

func publishVolumeSelectionProfile(isFile bool, remoteSymID string) backendSelectionProfile {
	return backendSelectionProfile{
		IsFile:             isFile,
		HasContentSource:   false,
		ReplicationEnabled: remoteSymID != "",
		ReplicationMode:    "",
	}
}

// ---------------------------------------------------------------------------
// Interfaces
// ---------------------------------------------------------------------------

type volumeCreator interface {
	Create(ctx context.Context, req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error)
}

type volumePublisher interface {
	Publish(ctx context.Context, req *csi.ControllerPublishVolumeRequest) (*csi.ControllerPublishVolumeResponse, error)
}

type U4P104ServiceDeps interface {
	resolveParameter(params map[string]string, symID, key, defaultVal string) string
	isDynamicSGEnabled() bool
	getReplicationPrefix() string
	getReplicationContextPrefix() string
	getClusterPrefix() string
	parseCsiID(csiID string) (volName, arrayID, devID, remoteSymID, remoteVolID string, err error)
	isSnapshotLicensed(ctx context.Context, symID string, pmaxClient pmax.Pmax) error
	getDynamicSG(ctx context.Context, arrayID, baseSGName string) (string, bool, error)
	getStorageArrayLabels(arrayID string) map[string]string
	isBlockEnabled() bool
}

// ---------------------------------------------------------------------------
// Factory functions
// ---------------------------------------------------------------------------

func creatorFor(s *service, pmaxClient pmax.Pmax, pmaxClient104 pmax.Pmax, symmetrixID, reqID string, params map[string]string, symmIDFoundInAZ bool, version string, apiVersion int, vcs []*csi.VolumeCapability, contentSource *csi.VolumeContentSource) (backendPath, volumeCreator) {
	isFile := vcs != nil && accTypeIsNFS(vcs)
	replicationEnabled := params != nil && params[path.Join(s.getReplicationPrefix(), RepEnabledParam)] == "true"
	isThick := params != nil && params[ThickVolumesParam] == "true"
	profile := createVolumeSelectionProfile(isFile, contentSource, replicationEnabled, "", isThick)
	selectedPath := selectBackendPath(profile, version)

	if selectedPath == backendU4P104 {
		u4p104 := &u4p104VolumeCreator{
			s:               s,
			pmaxClient:      pmaxClient,
			pmaxClient104:   pmaxClient104,
			symmetrixID:     symmetrixID,
			reqID:           reqID,
			params:          params,
			symmIDFoundInAZ: symmIDFoundInAZ,
			apiVersion:      apiVersion,
		}
		return selectedPath, u4p104
	}
	legacy := &legacyVolumeCreator{
		s:               s,
		pmaxClient:      pmaxClient,
		symmetrixID:     symmetrixID,
		reqID:           reqID,
		params:          params,
		symmIDFoundInAZ: symmIDFoundInAZ,
		apiVersion:      apiVersion,
	}
	return selectedPath, legacy
}

func publisherFor(ctx context.Context, s *service, pmaxClient pmax.Pmax, pmaxClient104 pmax.Pmax, symID, devID, volID, volumeName, remoteSymID, remoteVolumeID, reqID string, version string, vc *csi.VolumeCapability) volumePublisher {
	isFile := vc != nil && accTypeIsNFS([]*csi.VolumeCapability{vc})
	profile := publishVolumeSelectionProfile(isFile, remoteSymID)
	selectedPath := selectBackendPath(profile, version)

	// vSphere uses host groups and fixed naming conventions that don't map
	// cleanly to the 10.4 PublishMaskingViews API — route to legacy.
	if selectedPath == backendU4P104 && s.opts.IsVsphereEnabled {
		selectedPath = backendLegacy
	}

	// Scope gate: static provisioning of a file system — when fsType is empty
	// and a file system already exists on the device, fall back to legacy.
	if selectedPath == backendU4P104 && vc != nil && vc.GetMount().GetFsType() == "" {
		_, err := pmaxClient.GetFileSystemByID(ctx, symID, devID)
		if err == nil {
			selectedPath = backendLegacy
		}
	}

	if selectedPath == backendU4P104 {
		return &u4p104VolumePublisher{
			s:              s,
			legacyClient:   pmaxClient,
			client104:      pmaxClient104,
			symID:          symID,
			devID:          devID,
			volID:          volID,
			volumeName:     volumeName,
			remoteSymID:    remoteSymID,
			remoteVolumeID: remoteVolumeID,
			reqID:          reqID,
		}
	}
	return &legacyVolumePublisher{
		s:              s,
		pmaxClient:     pmaxClient,
		symID:          symID,
		devID:          devID,
		volID:          volID,
		volumeName:     volumeName,
		remoteSymID:    remoteSymID,
		remoteVolumeID: remoteVolumeID,
		reqID:          reqID,
	}
}
