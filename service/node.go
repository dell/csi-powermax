/*
 Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"math"
	"math/rand"
	"net"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dell/csmlog"
	"github.com/dell/gonvme"
	corev1 "k8s.io/api/core/v1"

	"github.com/dell/csi-powermax/v2/pkg/file"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/vmware/govmomi/object"

	pmax "github.com/dell/gopowermax/v2"

	"github.com/dell/gobrick"
	csictx "github.com/dell/gocsi/context"
	gofsutil "github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/coreos/go-systemd/v22/dbus"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	types "github.com/dell/gopowermax/v2/types/v100"
)

var (
	maximumStartupDelay         = 30
	getMappedVolMaxRetry        = 20
	nodePublishSleepTime        = 3 * time.Second
	lipSleepTime                = 5 * time.Second
	multipathSleepTime          = 5 * time.Second
	removeDeviceSleepTime       = 5000 * time.Millisecond
	deviceDeletionTimeout       = 5000 * time.Millisecond
	deviceDeletionPoll          = 50 * time.Millisecond
	maxBlockDevicesPerWWN       = 16
	targetMountRecheckSleepTime = 3 * time.Second
	multipathMutex              sync.Mutex
	deviceDeleteMutex           sync.Mutex
	disconnectVolumeRetryTime   = 1 * time.Second
	nodePendingState            = pendingState{
		pendingMutex: &sync.Mutex{},
	}
	sysBlock             = "/sys/block" // changed for unit testing
	dev                  = "/dev/"
	maxDisconnectRetries = 3
	// Maximum number of re-tries for some Unisphere calls (reduced in unit-tests)
	pmaxQueryAttempts = 20
)

type maskingViewTargetInfo struct {
	target           goiscsi.ISCSITarget
	IsCHAPConfigured bool
}

type maskingViewNVMeTargetInfo struct {
	target gonvme.NVMeTarget
}

// Mockable function variable for unit testing
var getIPInterfaces func(ctx context.Context, symID string, portGroups []string, pmaxClient pmax.Pmax) (map[string]int32, error) = getIPInterfacesImpl

// getNVMeTCPTargetsFromPortGroups retrieves NVMeTCP targets (portal + NQN) from the configured port groups.
// Discovery is scoped to the configured portals, and only those portals are returned for direct connects.
var getNVMeTCPTargetsFromPortGroups func(s *service, ctx context.Context, symID string, portGroups []string, pmaxClient pmax.Pmax) ([]gonvme.NVMeTarget, error) = (*service).getNVMeTCPTargetsFromPortGroupsImpl

// getNVMeTCPTargetsFromPortals is the single point at which the driver runs NVMe
// target discovery, so it is also the single point at which host-managed mode
// suppresses it. Gating here rather than at each caller means a future caller
// cannot reintroduce discovery by accident.
func (s *service) getNVMeTCPTargetsFromPortals(ctx context.Context, symID string, portals map[string]int32, pmaxClient pmax.Pmax) ([]gonvme.NVMeTarget, error) {
	if s.isNVMeTCPHostManaged() {
		return s.hostManagedNVMeTCPTargetsForPortals(ctx, symID, portals, pmaxClient)
	}

	var targets []gonvme.NVMeTarget
	seen := make(map[string]struct{})
	for portal := range portals {
		discoveredTargets, err := s.nvmetcpClient.DiscoverNVMeTCPTargets(portal, false)
		if err != nil {
			csmlog.WithContext(ctx).Errorf("failed to discover NVMe targets on portal %s: %s", portal, err.Error())
			continue
		}
		for _, target := range discoveredTargets {
			if _, ok := portals[target.Portal]; !ok {
				continue
			}
			key := target.TargetNqn + "\x00" + target.Portal
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			targets = append(targets, target)
		}
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("no NVMe targets for symid %s", symID)
	}
	return targets, nil
}

func (s *service) getNVMeTCPTargetsFromPortGroupsImpl(ctx context.Context, symID string, portGroups []string, pmaxClient pmax.Pmax) ([]gonvme.NVMeTarget, error) {
	ipInterfaces, err := getIPInterfaces(ctx, symID, portGroups, pmaxClient)
	if err != nil {
		return nil, err
	}
	return s.getNVMeTCPTargetsFromPortals(ctx, symID, ipInterfaces, pmaxClient)
}

// Mapping between symid and all remote targets on the sym
// key - string, value - []NVMETCPTarget
var symToAllNVMeTCPTargets sync.Map

// Mapping between symid and all remote targets on the sym
// key - string, value - []ISCSITargetInfo
var symToAllISCSITargets sync.Map

// Mapping between symid and remote targets for this node
// key - string, value - []maskingViewTargetInfo
var symToMaskingViewTargets sync.Map

// Map to store if sym has fc connectivity or not
var isSymConnFC sync.Map

// InvalidateSymToMaskingViewTargets - invalidates the cache
// Only used for testing
func (s *service) InvalidateSymToMaskingViewTargets() {
	deletefunc := func(key interface{}, _ interface{}) bool {
		symToMaskingViewTargets.Delete(key)
		return true
	}
	symToMaskingViewTargets.Range(deletefunc)
}

// vmHost is vCenter obj
var vmHost *VMHost

func (s *service) NodeStageVolume(
	ctx context.Context,
	req *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeStageVolume",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeStageVolume called")

	privTgt := req.GetStagingTargetPath()
	if privTgt == "" {
		return nil, status.Error(codes.InvalidArgument, "Target Path is required")
	}

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it, check if pending op for this volume ID
	id := req.GetVolumeId()
	_, symID, devID, remoteSymID, remoteVolID, err := s.parseCsiID(id)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}

	// Metro site-failure handling: proactively detect the winning array.
	// Extract RDF group from the volume context so CheckMetroState can query
	// the specific SRDF Metro group for this volume.
	if remoteSymID != "" && s.isMetroSiteFailureHandlingEnabled() {
		volCtx := req.GetVolumeContext()
		localRDFGroupNo := volCtx[path.Join(s.opts.ReplicationContextPrefix, LocalRDFGroupParam)]
		remoteRDFGroupNo := volCtx[path.Join(s.opts.ReplicationContextPrefix, RemoteRDFGroupParam)]
		if err := s.logMetroStateCheck(ctx, "NodeStageVolume", symID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo); err != nil {
			return nil, err
		}
	}

	// Non-uniform Metro: determine which arrays this node manages.
	// filterArraysByZoneInfo excludes arrays that don't match the node's zone labels,
	// so only the locally reachable array is in ManagedArrays.
	localManaged := slices.Contains(s.opts.ManagedArrays, symID)
	remoteManaged := remoteSymID != "" && slices.Contains(s.opts.ManagedArrays, remoteSymID)
	var pmaxClient pmax.Pmax
	if remoteSymID != "" && !localManaged && remoteManaged {
		// Site2 node: only remote array is managed (e.g. node at site2, volume primary is R1)
		csmlog.Infof("Local array %s is not managed by this node, using remote array %s only (non-uniform Metro)", symID, remoteSymID)
		pmaxClient, err = s.GetPowerMaxClient(remoteSymID)
	} else if remoteSymID != "" && localManaged && !remoteManaged {
		// Site1 node: only local array is managed
		csmlog.Infof("Remote array %s is not managed by this node, using local array %s only (non-uniform Metro)", remoteSymID, symID)
		remoteSymID = ""
		pmaxClient, err = s.GetPowerMaxClient(symID)
	} else {
		// Uniform mode or both arrays managed
		pmaxClient, err = s.GetPowerMaxClient(symID, remoteSymID)
	}
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	volID := volumeIDType(id)
	if err := volID.checkAndUpdatePendingState(&nodePendingState); err != nil {
		return nil, err
	}
	defer volID.clearPending(&nodePendingState)

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		csmlog.WithContext(ctx).Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}
	// Check if fileSystem
	if accTypeIsNFS([]*csi.VolumeCapability{req.GetVolumeCapability()}) {
		// devID is fsID
		return file.StageFileSystem(ctx, reqID, symID, devID, privTgt, req.GetPublishContext(), pmaxClient)
	}
	// Parse the CSI VolumeId and validate against the volume
	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		// If the volume isn't found, we cannot stage it
		return nil, err
	}
	volumeWWN := vol.EffectiveWWN

	// Save volume WWN to node disk
	err = s.writeWWNFile(id, volumeWWN)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Could not write WWN file: %s: %v", volumeWWN, err)
	}

	// Attach RDM
	if s.opts.IsVsphereEnabled {
		err := s.attachRDM(devID, volumeWWN)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		csmlog.WithContext(ctx).Debugf("attach RDM on VM complete...")
	}

	// Get publishContext
	publishContext := req.GetPublishContext()
	volumeLUNAddress := publishContext[PublishContextLUNAddress]
	var keyCount int
	targetIdentifiers := ""
	if count, ok := publishContext[PortIdentifierKeyCount]; ok {
		keyCount, _ = strconv.Atoi(count)
		for i := 1; i <= keyCount; i++ {
			portIdentifierKey := fmt.Sprintf("%s_%d", PortIdentifiers, i)
			targetIdentifiers += publishContext[portIdentifierKey]
		}
	} else {
		targetIdentifiers = publishContext[PortIdentifiers]
	}

	f := csmlog.Fields{
		"CSIRequestID":      reqID,
		"DeviceID":          devID,
		"ID":                req.VolumeId,
		"LUNAddress":        volumeLUNAddress,
		"PrivTgt":           privTgt,
		"SymmetrixID":       symID,
		"TargetIdentifiers": targetIdentifiers,
		"WWN":               volumeWWN,
	}
	csmlog.WithFields(f).Info("NodeStageVolume")
	ctx = setLogFields(ctx, f)

	nodeCHRoot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)
	var devicePath string

	// Connect local device — skip if local publish context is absent
	// (non-uniform Metro: local publish was skipped because node has no host on R1)
	if volumeLUNAddress == "" && !localManaged {
		csmlog.Infof("Skipping local device connection for volume %s: local publish context absent (non-uniform Metro)", devID)
	} else {
		localPublishContextData := publishContextData{
			deviceWWN:        "0x" + volumeWWN,
			volumeLUNAddress: volumeLUNAddress,
			array:            symID,
		}
		iscsiTargets, fcTargets, nvmeTCPTargets, useFC, useNVMeTCP := s.getArrayTargets(ctx, targetIdentifiers, symID, pmaxClient)

		// Metro under host-managed NVMe/TCP: both arrays must be reachable over
		// host-established sessions before any device is staged (FR-8). Staging
		// one leg of a Metro volume and failing the other leaves the workload
		// running on a single array without saying so.
		//
		// The protocol is taken from useNVMeTCP, the per-volume answer, rather
		// than s.opts.TransportProtocol, which holds the operator's *preferred*
		// protocol and is empty whenever the driver auto-detects.
		//
		// remoteSymID is cleared above for non-uniform Metro, where this node is
		// expected to reach one array only, so the rule applies to uniform Metro.
		if useNVMeTCP && remoteSymID != "" && s.isNVMeTCPHostManaged() {
			if err := s.verifyHostManagedNVMeTCPMetro(ctx, symID, remoteSymID, pmaxClient); err != nil {
				return nil, err
			}
		}

		if useFC {
			s.initFCConnector(nodeCHRoot)
			localPublishContextData.fcTargets = fcTargets
		} else if useNVMeTCP {
			s.initNVMeTCPConnector(nodeCHRoot)
			localPublishContextData.deviceWWN = volumeWWN
			localPublishContextData.nvmetcpTargets = nvmeTCPTargets
		} else {
			s.initISCSIConnector(nodeCHRoot)
			localPublishContextData.iscsiTargets = iscsiTargets
		}
		devicePath, err = s.connectDevice(ctx, localPublishContextData)
		if err != nil {
			return nil, err
		}
	}
	// Connect Remote Device
	if remoteSymID != "" {
		remDeviceWWN := publishContext[PublishContextDeviceWWN]
		remVolumeLUNAddress := publishContext[RemotePublishContextLUNAddress]

		// Determine if this is uniform or non-uniform Metro by checking which keys exist
		isUniformMetro := remVolumeLUNAddress != ""

		if !isUniformMetro {
			// Non-uniform Metro: use standard keys (controller published on one array only)
			remVolumeLUNAddress = publishContext[PublishContextLUNAddress]
		}

		if remDeviceWWN == "" || remVolumeLUNAddress == "" {
			// Non-uniform Metro: remote publish was skipped so remote context is absent.
			// The local device paths are sufficient — SRDF Metro federation ensures
			// the volume is accessible via the local array.
			csmlog.WithContext(ctx).Infof("Skipping remote device connection for volume %s: remote publish context absent (non-uniform Metro)", devID)
		} else {
			remotePublishContextData := publishContextData{
				deviceWWN:        "0x" + remDeviceWWN,
				volumeLUNAddress: remVolumeLUNAddress,
				array:            remoteSymID,
			}
			f := csmlog.Fields{
				"CSIRequestID":      reqID,
				"DeviceID":          remoteVolID,
				"ID":                req.VolumeId,
				"LUNAddress":        remVolumeLUNAddress,
				"PrivTgt":           privTgt,
				"SymmetrixID":       symID,
				"RemoteSymID":       remoteSymID,
				"TargetIdentifiers": targetIdentifiers,
				"WWN":               remDeviceWWN,
			}
			csmlog.WithFields(f).Info("NodeStageVolume for Remote Device")
			ctx = setLogFields(ctx, f)

			remoteTargetIdentifiers := ""
			if isUniformMetro {
				// Uniform Metro: use REMOTE_ keys
				if count, ok := publishContext[RemotePortIdentifierKeyCount]; ok {
					keyCount, _ = strconv.Atoi(count)
					for i := 1; i <= keyCount; i++ {
						portIdentifierKey := fmt.Sprintf("%s_%d", RemotePortIdentifiers, i)
						remoteTargetIdentifiers += publishContext[portIdentifierKey]
					}
				} else {
					remoteTargetIdentifiers = publishContext[RemotePortIdentifiers]
				}
			} else {
				// Non-uniform Metro: use standard keys
				if count, ok := publishContext[PortIdentifierKeyCount]; ok {
					keyCount, _ = strconv.Atoi(count)
					for i := 1; i <= keyCount; i++ {
						portIdentifierKey := fmt.Sprintf("%s_%d", PortIdentifiers, i)
						remoteTargetIdentifiers += publishContext[portIdentifierKey]
					}
				} else {
					remoteTargetIdentifiers = publishContext[PortIdentifiers]
				}
			}
			remIscsiTargets, remFcTargets, remNVMeTCPTargets, remUseFC, remUseNVMe := s.getArrayTargets(ctx, remoteTargetIdentifiers, remoteSymID, pmaxClient)
			csmlog.WithContext(ctx).Infof("Remote ISCSI Targets %v", remIscsiTargets)
			if remUseFC {
				s.initFCConnector(nodeCHRoot)
				remotePublishContextData.fcTargets = remFcTargets
			} else if remUseNVMe {
				s.initNVMeTCPConnector(nodeCHRoot)
				remotePublishContextData.deviceWWN = volumeWWN
				remotePublishContextData.nvmetcpTargets = remNVMeTCPTargets
			} else {
				s.initISCSIConnector(nodeCHRoot)
				remotePublishContextData.iscsiTargets = remIscsiTargets
			}
			remoteDevicePath, err := s.connectDevice(ctx, remotePublishContextData)
			if err != nil {
				return nil, err
			}
			f["RemoteDevicePath"] = remoteDevicePath
			f["RemoteLUNAddress"] = remVolumeLUNAddress
			f["TargetIdentifiers"] = remoteTargetIdentifiers
			f["RemoteWWN"] = remDeviceWWN
		}
	}

	csmlog.WithFields(f).Infof("NodeStageVolume completed for devicePath: %s", devicePath)

	return &csi.NodeStageVolumeResponse{}, nil
}

type publishContextData struct {
	deviceWWN        string
	volumeLUNAddress string
	// array is the Symmetrix ID the targets below belong to. Host-managed
	// NVMe/TCP reports it so an operator can tell which side of a Metro pair
	// failed a precondition.
	array          string
	iscsiTargets   []ISCSITargetInfo
	fcTargets      []FCTargetInfo
	nvmetcpTargets []NVMeTCPTargetInfo
}

// ISCSITargetInfo represents basic information about iSCSI target
type ISCSITargetInfo struct {
	Portal string
	Target string
}

// FCTargetInfo represents basic information about FC target
type FCTargetInfo struct {
	WWPN string
}

// NVMeTCPTargetInfo represents basic information about NVMeTCP target
type NVMeTCPTargetInfo struct {
	Portal string
	Target string
}

func nvmeTargetMatchesPublishIdentifier(publishIdentifier, targetNQN string) bool {
	return publishIdentifier == targetNQN || strings.HasPrefix(publishIdentifier, targetNQN+":")
}

func (s *service) connectDevice(ctx context.Context, data publishContextData) (string, error) {
	logFields := getLogFields(ctx)
	var err error
	// The volumeLUNAddress is hex.
	lun, err := strconv.ParseInt(data.volumeLUNAddress, 16, 0)
	if err != nil {
		csmlog.WithFields(logFields).Errorf("failed to convert lun number to int: %s", err.Error())
		return "", err
	}
	var device gobrick.Device
	if s.useFC {
		if s.opts.IsVsphereEnabled {
			device, err = s.connectRDMDevice(ctx, int(lun), data)
		} else {
			device, err = s.connectFCDevice(ctx, int(lun), data)
		}
	} else if s.useNVMeTCP {
		device, err = s.connectNVMeTCPDevice(ctx, data)
	} else {
		device, err = s.connectISCSIDevice(ctx, int(lun), data)
	}

	if err != nil {
		csmlog.WithContext(ctx).Errorf("Unable to find device after multiple discovery attempts: %s", err.Error())
		return "", status.Errorf(codes.Internal,
			"Unable to find device after multiple discovery attempts: %s", err.Error())
	}
	devicePath := path.Join("/dev/", device.Name)
	return devicePath, nil
}

func (s *service) connectISCSIDevice(ctx context.Context,
	lun int, data publishContextData,
) (gobrick.Device, error) {
	logFields := getLogFields(ctx)
	var targets []gobrick.ISCSITargetInfo
	for _, t := range data.iscsiTargets {
		targets = append(targets, gobrick.ISCSITargetInfo{Target: t.Target, Portal: t.Portal})
	}
	// separate context to prevent 15 seconds cancel from kubernetes
	connectorCtx, cFunc := context.WithTimeout(context.Background(), time.Second*120)
	connectorCtx = setLogFields(connectorCtx, logFields)
	defer cFunc()
	// TBD connectorCtx = copyTraceObj(ctx, connectorCtx)
	connectorCtx = setLogFields(connectorCtx, logFields)
	return s.iscsiConnector.ConnectVolume(connectorCtx, gobrick.ISCSIVolumeInfo{
		Targets: targets,
		Lun:     lun,
	})
}

func (s *service) connectFCDevice(ctx context.Context,
	lun int, data publishContextData,
) (gobrick.Device, error) {
	logFields := getLogFields(ctx)
	var targets []gobrick.FCTargetInfo
	for _, t := range data.fcTargets {
		targets = append(targets, gobrick.FCTargetInfo{WWPN: t.WWPN})
	}
	// separate context to prevent 15 seconds cancel from kubernetes
	connectorCtx, cFunc := context.WithTimeout(context.Background(), time.Second*120)
	connectorCtx = setLogFields(connectorCtx, logFields)
	defer cFunc()
	// TBD connectorCtx = copyTraceObj(ctx, connectorCtx)
	connectorCtx = setLogFields(connectorCtx, logFields)
	return s.fcConnector.ConnectVolume(connectorCtx, gobrick.FCVolumeInfo{
		Targets: targets,
		Lun:     lun,
	})
}

func (s *service) connectRDMDevice(ctx context.Context,
	lun int, data publishContextData,
) (gobrick.Device, error) {
	logFields := getLogFields(ctx)
	var targets []gobrick.FCTargetInfo
	for _, t := range data.fcTargets {
		targets = append(targets, gobrick.FCTargetInfo{WWPN: t.WWPN})
	}
	// separate context to prevent 15 seconds cancel from kubernetes
	connectorCtx, cFunc := context.WithTimeout(context.Background(), time.Second*120)
	connectorCtx = setLogFields(connectorCtx, logFields)
	defer cFunc()
	// TBD connectorCtx = copyTraceObj(ctx, connectorCtx)
	connectorCtx = setLogFields(connectorCtx, logFields)
	return s.fcConnector.ConnectRDMVolume(connectorCtx, gobrick.RDMVolumeInfo{
		Targets: targets,
		Lun:     lun,
		WWN:     strings.Replace(data.deviceWWN, "0x", "", 1),
	})
}

func (s *service) connectNVMeTCPDevice(ctx context.Context, data publishContextData,
) (gobrick.Device, error) {
	logFields := getLogFields(ctx)
	var targets []gobrick.NVMeTargetInfo
	for _, t := range data.nvmetcpTargets {
		targets = append(targets, gobrick.NVMeTargetInfo{Target: t.Target, Portal: t.Portal})
	}

	csmlog.WithContext(ctx).Debugf("connectNVMeTCPDevice: connecting volume with targets %v", targets)

	// In host-managed mode the session must already exist. Checking here, before
	// gobrick runs, lets the driver distinguish "the host never connected" from
	// "the host is connected but the device is absent" — gobrick reports both as
	// the same opaque failure (FR-9, AC-6).
	if s.isNVMeTCPHostManaged() {
		eligible, err := s.evaluateHostManagedNVMeTCPTargets(ctx, data.array, data.nvmetcpTargets)
		if err != nil {
			return gobrick.Device{}, err
		}
		if len(eligible) == 0 {
			return gobrick.Device{}, hostManagedMissingSessionError(data.array, data.nvmetcpTargets)
		}
	}

	// separate context to prevent 15 seconds cancel from kubernetes
	connectorCtx, cFunc := context.WithTimeout(context.Background(), time.Second*120)
	connectorCtx = setLogFields(connectorCtx, logFields)
	defer cFunc()
	wwn := data.deviceWWN
	// TBD connectorCtx = copyTraceObj(ctx, connectorCtx)
	connectorCtx = setLogFields(connectorCtx, logFields)
	device, err := s.nvmeTCPConnector.ConnectVolume(connectorCtx, gobrick.NVMeVolumeInfo{
		Targets: targets,
		WWN:     wwn,
	}, false)
	if err != nil && s.isNVMeTCPHostManaged() {
		// An eligible session was confirmed moments ago, so a failure here means
		// the device did not appear on it — an array-side or masking problem, not
		// a host connectivity one.
		return gobrick.Device{}, hostManagedDeviceNotVisibleError(data.array, wwn, err)
	}
	return device, err
}

func (s *service) NodeUnstageVolume(
	ctx context.Context,
	req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeUnstageVolume",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeUnstageVolume called")

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it, check if pending op for this volume ID
	id := req.GetVolumeId()
	_, symID, devID, remoteSymIDUns, remoteDevIDUns, err := s.parseCsiID(id)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}

	// Non-uniform Metro: select the right array client based on what this node manages.
	localManagedUns := slices.Contains(s.opts.ManagedArrays, symID)
	remoteManagedUns := remoteSymIDUns != "" && slices.Contains(s.opts.ManagedArrays, remoteSymIDUns)
	var pmaxClient pmax.Pmax
	if remoteSymIDUns != "" && !localManagedUns && remoteManagedUns {
		csmlog.WithContext(ctx).Infof("Local array %s is not managed by this node, using remote array %s only (non-uniform Metro)", symID, remoteSymIDUns)
		pmaxClient, err = s.GetPowerMaxClient(remoteSymIDUns)
	} else if remoteSymIDUns != "" && localManagedUns && !remoteManagedUns {
		csmlog.WithContext(ctx).Infof("Remote array %s is not managed by this node, using local array %s only (non-uniform Metro)", remoteSymIDUns, symID)
		pmaxClient, err = s.GetPowerMaxClient(symID)
	} else {
		pmaxClient, err = s.GetPowerMaxClient(symID, remoteSymIDUns)
	}
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Metro site-failure handling: proactively detect the winning array.
	// NodeUnstageVolume has no VolumeContext; look up the volume's RDF group
	// from the array to pass the correct group to CheckMetroState.
	if remoteSymIDUns != "" && s.isMetroSiteFailureHandlingEnabled() {
		localRDFGroupNo := ""
		remoteRDFGroupNo := ""
		lookupSymID, lookupDevID := symID, devID
		lookupIsRemote := !localManagedUns && remoteManagedUns
		if lookupIsRemote {
			lookupSymID, lookupDevID = remoteSymIDUns, remoteDevIDUns
		}
		if vol, volErr := pmaxClient.GetVolumeByID(ctx, lookupSymID, lookupDevID); volErr == nil && len(vol.RDFGroupIDList) > 0 {
			lookupRDFGroupNo := strconv.Itoa(vol.RDFGroupIDList[0].RDFGroupNumber)
			if lookupIsRemote {
				remoteRDFGroupNo = lookupRDFGroupNo
			} else {
				localRDFGroupNo = lookupRDFGroupNo
			}
			if pair, pairErr := pmaxClient.GetRDFDevicePairInfo(ctx, lookupSymID, lookupRDFGroupNo, lookupDevID); pairErr == nil {
				if lookupIsRemote {
					localRDFGroupNo = strconv.Itoa(pair.RemoteRdfGroupNumber)
				} else {
					remoteRDFGroupNo = strconv.Itoa(pair.RemoteRdfGroupNumber)
				}
			}
		}
		if err := s.logMetroStateCheck(ctx, "NodeUnstageVolume", symID, remoteSymIDUns, localRDFGroupNo, remoteRDFGroupNo); err != nil {
			return nil, err
		}
	}

	var volID volumeIDType = volumeIDType(id)
	if err := volID.checkAndUpdatePendingState(&nodePendingState); err != nil {
		return nil, err
	}
	defer volID.clearPending(&nodePendingState)

	// Remove the staging directory.
	stageTgt := req.GetStagingTargetPath()
	if stageTgt == "" {
		return nil, status.Error(codes.InvalidArgument, "A Staging Target argument is required")
	}

	err = gofsutil.Unmount(context.Background(), stageTgt)
	if err != nil {
		csmlog.WithContext(ctx).Infof("NodeUnstageVolume error unmount stage target %s: %s", stageTgt, err.Error())
	}
	removeWithRetry(stageTgt) // #nosec G20

	if len(devID) > 5 {
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	// READ volume WWN from the local file copy
	var volumeWWN string
	volumeWWN, err = s.readWWNFile(id)
	if err != nil || s.useNVMeTCP {
		csmlog.WithContext(ctx).Infof("Fallback to retrieve WWN from server: %s", err)

		// Fallback - retrieve the WWN from the array. Much more expensive.
		// Probe the node if required and make sure startup called
		err = s.nodeProbe(ctx)
		if err != nil {
			csmlog.WithContext(ctx).Error("nodeProbe failed with error :" + err.Error())
			return nil, err
		}

		// Parse the CSI VolumeId and validate against the volume
		_, _, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
		if err != nil {
			// If the volume isn't found, or we fail to validate the name/id, k8s will retry NodeUnstage forever so...
			// Make it stop...
			if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
				return &csi.NodeUnstageVolumeResponse{}, nil
			}
			return nil, err
		}
		volumeWWN = vol.EffectiveWWN
		if s.useNVMeTCP {
			volumeWWN = vol.NGUID
		}
	}

	// Parse the volume ID to get the symID and devID
	_, symID, devID, _, _, err = s.parseCsiID(id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Remove the mount private directory before disconnecting volume
	// This ensures the device is not in use when multipath flush is attempted
	privTgt := getPrivateMountPoint(s.privDir, id)
	if err := gofsutil.Unmount(context.Background(), privTgt); err != nil {
		csmlog.WithContext(ctx).Infof("Unmount of private target %s: %s (may already be unmounted)", privTgt, err.Error())
	}
	// Unmount /noderoot variant for bind mount environments
	if err := gofsutil.Unmount(context.Background(), "/noderoot"+privTgt); err != nil {
		csmlog.WithContext(ctx).Debugf("Unmount of /noderoot variant: %s", err.Error())
	}
	err = removeWithRetry(privTgt)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	if err := s.disconnectVolume(reqID, symID, devID, volumeWWN); err != nil {
		return nil, err
	}

	s.removeWWNFile(id)

	if s.opts.IsVsphereEnabled {
		err := s.detachRDM(devID, volumeWWN)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		csmlog.WithContext(ctx).Debugf("rescanning HBAs on host done...")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// attachRDM attaches an RDM using volumeWWN to the host
func (s *service) attachRDM(volID, volumeWWN string) error {
	host, err := s.getVMHostSystem()
	if err != nil {
		csmlog.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	csmlog.Debugf("found host: (%v)", host)

	// Rescan the HBA
	if err := vmHost.RescanAllHba(host); err != nil {
		csmlog.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	csmlog.Debugf("rescanning HBAs on host done...")

	// Attach RDM
	err = vmHost.AttachRDM(vmHost.VM, volumeWWN)
	if err != nil {
		csmlog.Errorf("Could not attach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("could not attach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	return nil
}

// detachRDM detaches an RDM using volumeWWN from the host
func (s *service) detachRDM(volID, volumeWWN string) error {
	// perform detach RDM call

	// Find the host system
	host, err := s.getVMHostSystem()
	if err != nil {
		csmlog.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	csmlog.Debugf("found host: (%v)", host)

	// Detach RDM
	err = vmHost.DetachRDM(vmHost.VM, volumeWWN)
	if err != nil {
		csmlog.Errorf("Could not detach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("could not detach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	csmlog.Debugf("detach RDM complete ...")

	// Rescan the HBA
	if err := vmHost.RescanAllHba(host); err != nil {
		csmlog.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}

	return nil
}

// disconnectVolume disconnects a volume from a node and will verify it is disconnected
// by no more error in disconnectVolumeByWWN call retrying if necessary.
func (s *service) disconnectVolume(reqID, symID, devID, volumeWWN string) error {
	if s.arrayTransportProtocolMap[symID] == FcTransportProtocol {
		var err error
		symlinkPath, _, _ := gofsutil.WWNToDevicePathX(context.Background(), volumeWWN)
		for i := 1; i <= maxDisconnectRetries; i++ {
			f := csmlog.Fields{
				"CSIRequestID": reqID,
				"DeviceID":     devID,
				"Retry":        i,
				"SymmetrixID":  symID,
				"symlinkPath":  symlinkPath,
				"WWN":          volumeWWN,
			}
			csmlog.WithFields(f).Info("NodeUnstageVolume disconnect volume FC")
			// Disconnect the volume using device name
			nodeUnstageCtx, cancel := context.WithTimeout(context.Background(), time.Second*120)
			nodeUnstageCtx = setLogFields(nodeUnstageCtx, f)
			if err = s.fcConnector.DisconnectVolumeByWWN(nodeUnstageCtx, volumeWWN); err == nil {
				cancel()
				csmlog.Debugf("DisconnectVolumeByWWN complete for %s", volumeWWN)
				os.Remove(symlinkPath) // #nosec G20
				return nil
			}
			csmlog.Errorf("error disconnecting volumefor retry: %d with error: %s", i, err)
			cancel()
			time.Sleep(disconnectVolumeRetryTime)
		}
		return status.Errorf(codes.Internal, "disconnectVolume exceeded retry limit WWN %s", volumeWWN)
	}
	for i := 1; i <= maxDisconnectRetries; i++ {
		var deviceName, symlinkPath, devicePath string
		symlinkPath, devicePath, _ = gofsutil.WWNToDevicePathX(context.Background(), volumeWWN)
		if devicePath == "" {
			if i == 0 {
				csmlog.Infof("NodeUnstage Note- Didn't find device path for volume %s", volumeWWN)
			}
			return nil
		}
		devicePathComponents := strings.Split(devicePath, "/")
		deviceName = devicePathComponents[len(devicePathComponents)-1]
		f := csmlog.Fields{
			"CSIRequestID": reqID,
			"DeviceID":     devID,
			"DeviceName":   deviceName,
			"DevicePath":   devicePath,
			"Retry":        i,
			"SymmetrixID":  symID,
			"WWN":          volumeWWN,
		}
		csmlog.WithFields(f).Info("NodeUnstageVolume disconnectVolume")

		// Disconnect the volume using device name
		nodeUnstageCtx, cancel := context.WithTimeout(context.Background(), time.Second*120)
		nodeUnstageCtx = setLogFields(nodeUnstageCtx, f)
		switch s.arrayTransportProtocolMap[symID] {
		case Vsphere:
			_ = s.fcConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		case IscsiTransportProtocol:
			_ = s.iscsiConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		case NvmeTCPTransportProtocol:
			err := s.nvmeTCPConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
			cancel()
			if err != nil {
				csmlog.WithFields(f).Errorf("failed to disconnect volume by device name %s with error %s", deviceName, err.Error())
				continue
			}
			return nil
		}
		cancel()
		time.Sleep(disconnectVolumeRetryTime)

		// Check that the /sys/block/DeviceName actually exists
		if _, err := os.ReadDir(sysBlock + deviceName); err != nil {
			// If not, make sure the symlink is removed
			os.Remove(symlinkPath) // #nosec G20
		}
	}
	// Recheck volume disconnected
	devPath, _ := gofsutil.WWNToDevicePath(context.Background(), volumeWWN)
	if devPath == "" {
		return nil
	}
	return status.Errorf(codes.Internal, "disconnectVolume exceeded retry limit WWN %s devPath %s", volumeWWN, devPath)
}

// NodePublishVolume handles the CSI request to publish a volume to a target directory.
func (s *service) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodePublishVolume",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodePublishVolume called")

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()
	_, symID, devID, remoteSymID, remoteVolID, err := s.parseCsiID(id)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}

	// Non-uniform Metro: determine which arrays this node manages.
	// filterArraysByZoneInfo excludes arrays that don't match the node's zone labels,
	// so only the locally reachable array is in ManagedArrays.
	localManaged := slices.Contains(s.opts.ManagedArrays, symID)
	remoteManaged := remoteSymID != "" && slices.Contains(s.opts.ManagedArrays, remoteSymID)

	// Determine which array and volume ID to use based on node's connectivity
	effectiveSymID := symID
	effectiveDevID := devID
	var pmaxClient pmax.Pmax
	if remoteSymID != "" && !localManaged && remoteManaged {
		// Site2 node: only remote array is managed (e.g. node at site2, volume primary is R1)
		csmlog.WithContext(ctx).Infof("NodePublishVolume: Local array %s is not managed by this node, using remote array %s (non-uniform Metro)", symID, remoteSymID)
		effectiveSymID = remoteSymID
		effectiveDevID = remoteVolID
		pmaxClient, err = s.GetPowerMaxClient(remoteSymID)
	} else if remoteSymID != "" && localManaged && !remoteManaged {
		// Site1 node: only local array is managed
		csmlog.WithContext(ctx).Infof("NodePublishVolume: Remote array %s is not managed by this node, using local array %s (non-uniform Metro)", remoteSymID, symID)
		pmaxClient, err = s.GetPowerMaxClient(symID)
	} else {
		// Uniform mode or both arrays managed
		pmaxClient, err = s.GetPowerMaxClient(symID, remoteSymID)
	}
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		csmlog.WithContext(ctx).Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}
	// Get publishContext
	publishContext := req.GetPublishContext()

	// check if it is FileSystem volume
	if accTypeIsNFS([]*csi.VolumeCapability{req.GetVolumeCapability()}) {
		return file.PublishFileSystem(ctx, req, reqID, effectiveSymID, effectiveDevID, pmaxClient)
	}

	// Parse the CSI VolumeId and validate against the volume
	// For non-uniform Metro, we need to query the volume on the array this node can access
	var vol *types.Volume
	vol, err = pmaxClient.GetVolumeByID(ctx, effectiveSymID, effectiveDevID)
	if err != nil {
		if strings.Contains(err.Error(), cannotBeFound) {
			return nil, status.Errorf(codes.NotFound,
				"Volume not found (Array: %s, Volume: %s) status %s",
				effectiveSymID, effectiveDevID, err.Error())
		}
		return nil, status.Errorf(codes.Internal,
			"failure checking volume (Array: %s, Volume: %s) status %s",
			effectiveSymID, effectiveDevID, err.Error())
	}

	// Get volumeContext
	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		csmlog.WithContext(ctx).Infof("VolumeContext:")
		for key, value := range volumeContext {
			csmlog.WithContext(ctx).Infof("    [%s]=%s", key, value)
		}
	}
	// Get publishContext
	deviceWWN := publishContext[PublishContextDeviceWWN]
	volumeLUNAddress := publishContext[PublishContextLUNAddress]
	if deviceWWN == "" {
		csmlog.WithContext(ctx).Error("Device WWN required to be in PublishContext")
		return nil, status.Error(codes.InvalidArgument, "Device WWN required to be in PublishContext")
	}
	var keyCount int
	targetIdentifiers := ""
	if count, ok := publishContext[PortIdentifierKeyCount]; ok {
		keyCount, _ = strconv.Atoi(count)
		for i := 1; i <= keyCount; i++ {
			portIdentifierKey := fmt.Sprintf("%s_%d", PortIdentifiers, i)
			targetIdentifiers += publishContext[portIdentifierKey]
		}
	} else {
		targetIdentifiers = publishContext[PortIdentifiers]
	}
	csmlog.WithContext(ctx).Infof("node publishing volume: %s lun: %s", deviceWWN, volumeLUNAddress)

	var symlinkPath string
	var devicePath string
	if s.useNVMeTCP {
		symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), vol.NGUID)
		if err != nil || symlinkPath == "" {
			errmsg := fmt.Sprintf("Device path not found for WWN %s: %s", deviceWWN, err)
			csmlog.WithContext(ctx).Error(errmsg)
			return nil, status.Error(codes.NotFound, errmsg)
		}
	} else {
		symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
		if err != nil || symlinkPath == "" {
			errmsg := fmt.Sprintf("Device path not found for WWN %s: %s", deviceWWN, err)
			csmlog.WithContext(ctx).Error(errmsg)
			return nil, status.Error(codes.NotFound, errmsg)
		}
	}

	f := csmlog.Fields{
		"CSIRequestID":      reqID,
		"DeviceID":          effectiveDevID,
		"DevicePath":        devicePath,
		"ID":                req.VolumeId,
		"Name":              volumeContext["Name"],
		"WWN":               deviceWWN,
		"PrivateDir":        s.privDir,
		"SymlinkPath":       symlinkPath,
		"SymmetrixID":       effectiveSymID,
		"TargetPath":        req.GetTargetPath(),
		"TargetIdentifiers": targetIdentifiers,
	}

	csmlog.WithFields(f).Info("Calling publishVolume")
	fsCfg := &fsCheckConfig{
		enabled:  s.opts.FsCheckEnabled,
		mode:     s.opts.FsCheckMode,
		k8sUtils: s.k8sUtils,
	}
	if err := publishVolume(req, s.privDir, symlinkPath, reqID, fsCfg); err != nil {
		return nil, err
	}
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume handles the CSI request to unpublish a volume from a particular target directory.
func (s *service) NodeUnpublishVolume(
	ctx context.Context,
	req *csi.NodeUnpublishVolumeRequest) (
	*csi.NodeUnpublishVolumeResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeUnpublishVolume",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeUnpublishVolume called")

	var reqID string
	var err error
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the target path
	target := req.GetTargetPath()
	if target == "" {
		csmlog.WithContext(ctx).Error("target path required")
		return nil, status.Error(codes.InvalidArgument,
			"target path required")
	}

	// Look through the mount table for the target path.
	var targetMount gofsutil.Info
	if targetMount, err = s.getTargetMount(target); err != nil {
		return nil, err
	}

	if targetMount.Device == "" {
		// This should not happen normally... idempotent requests should be rare.
		// If we incorrectly exit here, conflicting devices will be left
		csmlog.WithContext(ctx).Debug(fmt.Sprintf("No target mount found... waiting %v to re-verify no target %s mount", targetMountRecheckSleepTime, target))
		time.Sleep(targetMountRecheckSleepTime)
		if targetMount, err = s.getTargetMount(target); err != nil {
			csmlog.WithContext(ctx).Info(fmt.Sprintf("Still no mount entry for target, so assuming this is an idempotent call: %s", target))
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
	}

	csmlog.WithContext(ctx).Infof("targetMount: %#v", targetMount)
	devicePath := targetMount.Device
	if devicePath == "devtmpfs" || devicePath == "" {
		devicePath = targetMount.Source
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	f := csmlog.Fields{
		"CSIRequestID": reqID,
		"DevicePath":   devicePath,
		"ID":           id,
		"PrivateDir":   s.privDir,
		"TargetPath":   req.GetTargetPath(),
	}

	// Unmount the target path.
	csmlog.WithFields(f).Info("Calling Unmount of TargetPath")
	err = gofsutil.Unmount(context.Background(), req.GetTargetPath())
	if err != nil {
		csmlog.WithFields(f).Info("TargetPath unmount: " + err.Error())
	}

	csmlog.WithFields(f).Info("Calling unpublishVolume")
	var lastUnmounted bool
	if lastUnmounted, err = unpublishVolume(req, s.privDir, devicePath, reqID); err != nil {
		return nil, err
	}

	csmlog.WithContext(ctx).Infof("lastUnmounted %v", lastUnmounted)
	if lastUnmounted {
		removeWithRetry(target) // #nosec G20
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// It is unusual that we have not removed the last mount (i.e. lastUnmounted == false)
	// Recheck to make sure the target is unmounted.
	csmlog.WithFields(f).Info("Not the last mount - rechecking target mount is gone")
	if targetMount, err = s.getTargetMount(target); err != nil {
		return nil, err
	}
	if targetMount.Device != "" {
		csmlog.WithFields(f).Error("Target mount still present... returning failure")
		return nil, status.Error(codes.Internal, "Target Mount still present")
	}
	// Get the device mounts
	dev, err := GetDevice(devicePath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	csmlog.WithFields(f).Info("rechecking dev mounts")
	mnts, err := getDevMounts(dev)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if len(mnts) > 0 {
		csmlog.WithFields(f).Infof("Device mounts still present: %#v", mnts)
	}
	removeWithRetry(target) // #nosec G20
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (s *service) getTargetMount(target string) (gofsutil.Info, error) {
	var targetMount gofsutil.Info
	mounts, err := gofsutil.GetMounts(context.Background())
	if err != nil {
		csmlog.Error("could not reliably determine existing mount status")
		return targetMount, status.Error(codes.Internal, "could not reliably determine existing mount status")
	}
	for _, mount := range mounts {
		if mount.Path == target {
			targetMount = mount
			csmlog.Infof("matching targetMount %s target %s", target, mount.Path)
		}
	}
	return targetMount, nil
}

func (s *service) nodeProbe(ctx context.Context) error {
	csmlog.WithContext(ctx).Debug("Entering nodeProbe")
	defer csmlog.WithContext(ctx).Debug("Exiting nodeProbe")
	if s.opts.NodeName == "" {
		return status.Errorf(codes.FailedPrecondition,
			"Error getting NodeName from the environment")
	}

	err := s.createPowerMaxClients(ctx)
	if err != nil {
		return err
	}

	// make sure we are logged into all arrays
	if s.nodeIsInitialized {
		// nothing to do for FC / FC vsphere
		if s.opts.TransportProtocol != FcTransportProtocol && !s.opts.IsVsphereEnabled {
			// Make sure that there is only discovery/login attempt at one time
			s.nodeProbeMutex.Lock()
			defer s.nodeProbeMutex.Unlock()
			_ = s.ensureLoggedIntoEveryArray(ctx, false)
		}
	}
	return nil
}

func (s *service) nodeProbeBySymID(ctx context.Context, symID string) error {
	csmlog.WithContext(ctx).Debugf("Entering nodeProbeBySymID for array %s", symID)
	defer csmlog.WithContext(ctx).Debugf("Exiting nodeProbe for array %s", symID)

	if s.opts.NodeName == "" {
		return status.Errorf(codes.FailedPrecondition,
			"Error getting NodeName from the environment")
	}

	err := s.createPowerMaxClients(ctx)
	if err != nil {
		return err
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		return err
	}
	hostID, _, _ := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.csiNodeID())
	// get the host from the array
	if !s.useNVMeTCP {
		hostID, _, _ = s.GetHostSGAndMVIDFromNodeID(s.csiNodeID(), !s.useFC)
	}
	// Host adoption: if a host was adopted on this array, use the adopted host ID
	if adoptedID := s.getAdoptedHostID(symID); adoptedID != "" {
		hostID = adoptedID
	}

	host, err := pmaxClient.GetHostByID(ctx, symID, hostID)
	if err != nil {
		if strings.Contains(err.Error(), notFound) && s.useNFS {
			csmlog.WithContext(ctx).Debugf("Error %s, while probing %s but since it's NFS this is expected", err.Error(), symID)
			return nil
		}
		// nodeId is not right/it's not NFS and still host is not preset
		csmlog.WithContext(ctx).Infof("Error %s, while probing %s", err.Error(), symID)
		return err
	}

	csmlog.WithContext(ctx).Debugf("Successfully got Host %s on %s", symID, host.HostID)

	if s.useFC {
		csmlog.WithContext(ctx).Debugf("Checking if FC initiators are logged in or not")
		initiatorList, err := pmaxClient.GetInitiatorList(ctx, symID, "", false, true)
		if err != nil {
			csmlog.WithContext(ctx).Error("Could not get initiator list: " + err.Error())
			return err
		}
		for _, arrayInitrID := range initiatorList.InitiatorIDs {
			for _, hostInitID := range host.Initiators {
				if arrayInitrID == hostInitID || strings.HasSuffix(arrayInitrID, hostInitID) {
					initiator, err := pmaxClient.GetInitiatorByID(ctx, symID, arrayInitrID)
					if err != nil {
						return err
					}
					if initiator.OnFabric && initiator.LoggedIn {
						return nil
					}
				}
			}
		}
		return fmt.Errorf("no active fc sessions")
	}
	if s.useIscsi {
		// Check if host is connected to iscsi
		// Get iscsi initiators.
		IQNs, iSCSIErr := s.iscsiClient.GetInitiators("")
		if iSCSIErr != nil {
			return iSCSIErr
		}
		if host.NumberMaskingViews > 0 {
			err = s.performIscsiLoginOnSymID(ctx, symID, IQNs, host.MaskingviewIDs[0], pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("error performing iscsi login %s", err.Error())
				return err
			}
		} else {
			csmlog.WithContext(ctx).Infof("skippping login on host %s as no masking view exist", host.HostID)
		}

		csmlog.WithContext(ctx).Debugf("Checking if iscsi sessions are active on node or not")
		sessions, _ := s.iscsiClient.GetSessions()
		for _, target := range s.iscsiTargets[symID] {
			for _, session := range sessions {
				csmlog.WithContext(ctx).Debugf("matching %v with %v", target, session)
				if session.Target == target && session.ISCSISessionState == goiscsi.ISCSISessionStateLOGGEDIN {
					if s.useNFS {
						s.useNFS = false
					}
					return nil
				}
			}
		}
		return fmt.Errorf("no active iscsi sessions")
	} else if s.useNVMeTCP {
		if host.NumberMaskingViews > 0 {
			err = s.performNVMETCPLoginOnSymID(ctx, symID, host.MaskingviewIDs[0], pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("error performing NVMETCP login %s", err.Error())
				return err
			}
		} else {
			csmlog.WithContext(ctx).Infof("skippping login on host %s as no masking view exist", host.HostID)
		}
		csmlog.WithContext(ctx).Debugf("Checking if nvme sessions are active on node or not")
		sessions, _ := s.nvmetcpClient.GetSessions()
		targets, ok := s.nvmeTargets.Load(symID)
		if !ok {
			return fmt.Errorf("no active nvme sessions")
		}
		for _, target := range targets.([]string) {
			for _, session := range sessions {
				csmlog.WithContext(ctx).Debugf("matching %v with %v", target, session)
				csmlog.WithContext(ctx).Infof("target = %v, session.Target = %v", target, session.Target)
				if strings.HasPrefix(target, session.Target) && session.NVMESessionState == gonvme.NVMESessionStateLive {
					if s.useNFS {
						s.useNFS = false
					}
					return nil
				}
			}
		}
		return fmt.Errorf("no active nvme sessions")
	}
	return fmt.Errorf("no active sessions")
}

func (s *service) NodeGetCapabilities(
	ctx context.Context,
	_ *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeGetCapabilities",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeGetCapabilities called")

	capabilities := []*csi.NodeServiceCapability{
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_EXPAND_VOLUME,
				},
			},
		},
		{
			Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_SINGLE_NODE_MULTI_WRITER,
				},
			},
		},
	}
	if s.opts.IsHealthMonitorEnabled {
		healthMonitorCapabilities := []*csi.NodeServiceCapability{
			{
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_GET_VOLUME_STATS,
					},
				},
			}, {
				Type: &csi.NodeServiceCapability_Rpc{
					Rpc: &csi.NodeServiceCapability_RPC{
						Type: csi.NodeServiceCapability_RPC_VOLUME_CONDITION,
					},
				},
			},
		}
		capabilities = append(capabilities, healthMonitorCapabilities...)
	}

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: capabilities,
	}, nil
}

func getIPInterfacesImpl(ctx context.Context, symID string, portGroups []string, pmaxClient pmax.Pmax) (map[string]int32, error) {
	ipInterfaces := make(map[string]int32)
	for _, pg := range portGroups {
		portGroup, err := pmaxClient.GetPortGroupByID(ctx, symID, pg)
		if err != nil {
			return nil, err
		}

		for _, portKey := range portGroup.SymmetrixPortKey {
			port, err := pmaxClient.GetPort(ctx, symID, portKey.DirectorID, portKey.PortID)
			if err != nil {
				return nil, err
			}
			for _, ip := range port.SymmetrixPort.IPAddresses {
				ipInterfaces[ip] = port.SymmetrixPort.TCPPort
			}
		}
	}
	return ipInterfaces, nil
}

func (s *service) isISCSIConnected(err error) bool {
	errMsg := err.Error()
	if strings.Contains(errMsg, "exit status 24") || // ISCSI_ERR_LOGIN_AUTH_FAILED - login failed due to authorization failure
		strings.Contains(errMsg, "exit status 15") { // ISCSI_ERR_SESS_EXISTS - session is logged in
		return true
	}
	return false
}

// reachableEndPoint checks if this endpoint is reachable or not
func (s *service) reachableEndPoint(endpoint string) bool {
	// this endpoint has IP:PORT
	_, err := net.DialTimeout("tcp", endpoint, 2*time.Second)
	return err == nil
}

func (s *service) createTopologyMap(ctx context.Context, nodeName string) map[string]string {
	topology := map[string]string{}
	iscsiArrays := make([]string, 0)
	nvmeTCPArrays := make([]string, 0)
	nfsArrays := make([]string, 0)
	var protocol string
	var ok bool

	arrays := s.retryableGetSymmetrixIDList()

	for _, id := range arrays.SymmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(id)
		if err != nil {
			csmlog.WithContext(ctx).Error(err.Error())
			continue
		}

		if nfsServerList, err := pmaxClient.GetNFSServerList(ctx, id); err != nil {
			csmlog.WithContext(ctx).Errorf("failed to get the NFS server list, error: %s", err.Error())
		} else if nfsServerList != nil {
			for _, nfs := range nfsServerList.Entries {
				nfsServer, err := pmaxClient.GetNFSServerByID(ctx, id, nfs.ID)
				if err != nil {
					csmlog.WithContext(ctx).Errorf("failed to get the NFS server %s by id, error: %s", nfs.ID, err.Error())
					continue
				}

				if nfsServer != nil {
					if nfsServer.NFSV3Enabled || nfsServer.NFSV4Enabled {
						nfsArrays = append(nfsArrays, id)
						break
					}
				}
			}
		}

		if s.arrayTransportProtocolMap != nil {
			if protocol, ok = s.arrayTransportProtocolMap[id]; ok && (protocol == FcTransportProtocol || protocol == Vsphere) {
				continue
			}
		}

		ipInterfaces, err := getIPInterfaces(ctx, id, s.opts.PortGroups, pmaxClient)
		if err != nil {
			csmlog.WithContext(ctx).Errorf("unable to fetch ip interfaces for %s: %s", id, err.Error())
			continue
		}
		if len(ipInterfaces) == 0 {
			csmlog.WithContext(ctx).Errorf("Couldn't find any ip interfaces on any of the port-groups")
		}

		if protocol == NvmeTCPTransportProtocol {
			if isLoggedIn, ok := s.GetLoggedInNVMeArrays(id); ok && isLoggedIn {
				nvmeTCPArrays = append(nvmeTCPArrays, id)
				continue
			}

			// Host-managed mode decides reachability from the sessions the host
			// already established, never by discovering and connecting (FR-4).
			if s.isNVMeTCPHostManaged() {
				if s.hostManagedNVMeTCPArrayReachable(ctx, id, pmaxClient) {
					nvmeTCPArrays = append(nvmeTCPArrays, id)
				}
				continue
			}

			// Get port-group-scoped NVMeTCP targets (portal + NQN) and connect directly
			portGroupTargets, pgErr := s.getNVMeTCPTargetsFromPortals(ctx, id, ipInterfaces, pmaxClient)
			if pgErr != nil {
				csmlog.WithContext(ctx).Errorf("Failed to get NVMeTCP targets from port groups for array(%s): %s", id, pgErr.Error())
			}
			for _, tgt := range portGroupTargets {
				connectErr := s.nvmetcpClient.NVMeTCPConnect(tgt, false)
				if connectErr != nil {
					csmlog.WithContext(ctx).Errorf("Failed to connect to NVMe target %s on portal %s of array(%s): %s",
						tgt.TargetNqn, tgt.Portal, id, connectErr.Error())
				} else {
					nvmeTCPArrays = append(nvmeTCPArrays, id)
					break
				}
			}
		} else {
			if isLoggedIn, ok := s.GetLoggedInArrays(id); ok && isLoggedIn {
				iscsiArrays = append(iscsiArrays, id)
				continue
			}

			for ip, port := range ipInterfaces {
				// first check if this portal is reachable from this machine or not
				if s.reachableEndPoint(fmt.Sprintf("%s:%d", ip, port)) {
					_, err := s.iscsiClient.DiscoverTargets(ip, false)
					if err != nil && !s.isISCSIConnected(err) {
						csmlog.WithContext(ctx).Errorf("Failed to connect to the IP interface(%s) of array(%s)", ip, id)
						continue
					}
					iscsiArrays = append(iscsiArrays, id)
					break
				}
				csmlog.WithContext(ctx).Infof("IP interface(%s) of array(%s) is not reachable from the node", ip, id)
			}
		}
	}

	for array, protocol := range s.arrayTransportProtocolMap {
		if protocol == FcTransportProtocol && s.checkIfArrayProtocolValid(nodeName, array, strings.ToLower(FcTransportProtocol)) {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(FcTransportProtocol)] = s.getDriverName()
		}
		if protocol == Vsphere {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(Vsphere)] = s.getDriverName()
		}
	}

	for _, array := range iscsiArrays {
		if _, ok := topology[s.getDriverName()+"/"+array]; !ok &&
			s.checkIfArrayProtocolValid(nodeName, array, strings.ToLower(IscsiTransportProtocol)) {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(IscsiTransportProtocol)] = s.getDriverName()
		}
	}

	for _, array := range nvmeTCPArrays {
		if _, ok := topology[s.getDriverName()+"/"+array]; !ok &&
			s.checkIfArrayProtocolValid(nodeName, array, strings.ToLower(NvmeTCPTransportProtocol)) {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NvmeTCPTransportProtocol)] = s.getDriverName()
		}
	}

	for _, array := range nfsArrays {
		topology[s.getDriverName()+"/"+array] = s.getDriverName()
		topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NFS)] = s.getDriverName()
	}

	csmlog.WithContext(ctx).Infof("Topology for node (%s) : %+v", nodeName, topology)
	return topology
}

// checkIfArrayProtocolValid returns true if the  pair (array and protocol) is applicable for the given node based on config
// if the pair is present in allow rules, it is applied in the topology keys map
// if the pair is present in deny rules, it is skipped in the topology keys map
func (s *service) checkIfArrayProtocolValid(nodeName string, array string, protocol string) bool {
	if !s.opts.IsTopologyControlEnabled {
		return true
	}

	key := fmt.Sprintf("%s.%s", array, protocol)
	csmlog.Debugf("Checking topology config for allow rules for key (%s)", key)
	// Check topo key pair as per rules in allow list
	if allowedList, ok := s.allowedTopologyKeys[nodeName]; ok {
		if !checkIfKeyIsIncludedOrNot(allowedList, key) {
			return false
		}
	} else if allowedList, ok := s.allowedTopologyKeys["*"]; ok {
		if !checkIfKeyIsIncludedOrNot(allowedList, key) {
			return false
		}
	}

	csmlog.Debugf("Checking topology config for deny rules for key (%s)", key)
	// Check topo keys as per rules in denied list
	if deniedList, ok := s.deniedTopologyKeys[nodeName]; ok {
		if checkIfKeyIsIncludedOrNot(deniedList, key) {
			return false
		}
	} else if deniedList, ok := s.deniedTopologyKeys["*"]; ok {
		if checkIfKeyIsIncludedOrNot(deniedList, key) {
			return false
		}
	}
	csmlog.Debugf("applied topo key for node %s : %+v", nodeName, key)
	return true
}

// checkIfKeyIsIncludedOrNot will crosscheck the key with the applied rules in the config.
// returns true if it founds the key in the rules.
func checkIfKeyIsIncludedOrNot(rulesList []string, key string) bool {
	found := false
	for _, rule := range rulesList {
		if strings.Contains(key, rule) {
			found = true
			break
		}
	}
	return found
}

// NodeGetInfo minimal version. Returns the NodeId
// MaxVolumesPerNode (optional) is left as 0 which means unlimited, and AccessibleTopology is left nil.
func (s *service) NodeGetInfo(
	ctx context.Context,
	_ *csi.NodeGetInfoRequest) (
	*csi.NodeGetInfoResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeGetInfo",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeGetInfo called")

	// Get the Node ID
	if s.opts.NodeName == "" {
		csmlog.WithContext(ctx).Error("Unable to get Node Name from the environment")
		return nil, status.Error(codes.FailedPrecondition,
			"Unable to get Node Name from the environment")
	}

	topology := s.createTopologyMap(ctx, s.opts.NodeName)
	if len(topology) == 0 {
		csmlog.WithContext(ctx).Errorf("No topology keys could be generated")
		return nil, status.Error(codes.FailedPrecondition, "no topology keys could be generated")
	}

	var maxPowerMaxVolumesPerNode int64
	labels, err := s.k8sUtils.GetNodeLabels(s.opts.NodeFullName)
	if err != nil {
		csmlog.WithContext(ctx).Infof("failed to get Node Labels with error '%s'", err.Error())
	}
	if val, ok := labels["max-powermax-volumes-per-node"]; ok {
		maxPowerMaxVolumesPerNode, err = strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value '%s' specified for 'max-powermax-volumes-per-node' node label", val)
		}
		if s.opts.IsVsphereEnabled {
			if maxPowerMaxVolumesPerNode == 0 || maxPowerMaxVolumesPerNode > 60 {
				csmlog.WithContext(ctx).Errorf("Node label max-powermax-volumes-per-node should not be greater than 60 or set to any negative value for RDM volumes, Setting to default value 60")
				maxPowerMaxVolumesPerNode = 60
			}
		}
		csmlog.WithContext(ctx).Infof("node label 'max-powermax-volumes-per-node' is available and is set to value '%v'", maxPowerMaxVolumesPerNode)
	} else {
		// As per the csi spec the plugin MUST NOT set negative values to
		// 'MaxVolumesPerNode' in the NodeGetInfoResponse response
		csmlog.WithContext(ctx).Infof("Node label 'max-powermax-volumes-per-node' is not available. Retrieving the value from yaml file")
		if s.opts.IsVsphereEnabled {
			if s.opts.MaxVolumesPerNode <= 0 || s.opts.MaxVolumesPerNode > 60 {
				csmlog.WithContext(ctx).Errorf("maxPowerMaxVolumesPerNode MUST NOT be greater than 60 or set to any negative value for RDM volumes. Setting to default value 60")
				s.opts.MaxVolumesPerNode = 60
			}
		} else {
			if s.opts.MaxVolumesPerNode < 0 {
				csmlog.WithContext(ctx).Errorf("maxPowerMaxVolumesPerNode MUST NOT be set to negative value, setting to default value 0")
				s.opts.MaxVolumesPerNode = 0
			}
		}
		maxPowerMaxVolumesPerNode = s.opts.MaxVolumesPerNode
	}
	for _, array := range s.opts.ManagedArrays {
		arrayLabels := s.opts.StorageArrays[array].Labels
		for arrayLabelKey, arrayLabelVal := range arrayLabels {
			csmlog.WithContext(ctx).Infof("adding label '%s' with value '%s' to the topology map", arrayLabelKey, arrayLabelVal)
			topology[arrayLabelKey] = arrayLabelVal.(string)
		}
	}

	if err := s.addAdoptedHostTopology(ctx, topology); err != nil {
		return nil, err
	}

	return &csi.NodeGetInfoResponse{
		NodeId: s.csiNodeID(),
		AccessibleTopology: &csi.Topology{
			Segments: topology,
		},
		MaxVolumesPerNode: maxPowerMaxVolumesPerNode,
	}, nil
}

func (s *service) NodeGetVolumeStats(
	ctx context.Context, req *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeGetVolumeStats",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeGetVolumeStats called")

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()
	volName, symID, _, _, _, err := s.parseCsiID(id)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}

	volPath := req.GetVolumePath()
	if volPath == "" {
		csmlog.WithContext(ctx).Error("Volume path required")
		return nil, status.Error(codes.InvalidArgument, "no Volume path found in request, a volume path is required")
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		csmlog.WithContext(ctx).Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}

	f := csmlog.Fields{
		"CSIRequestID":      reqID,
		"VolumePath":        volPath,
		"ID":                id,
		"VolumeName":        volName,
		"StagingTargetPath": req.GetStagingTargetPath(),
	}
	csmlog.WithFields(f).Info("Calling NodeGetVolumeStats")

	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// abnormal and msg tells the condition of the volume
	var abnormal bool
	var msg string

	// check if volume exist
	_, _, _, err = s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		abnormal = true
		msg = fmt.Sprintf("volume %s not found", id)
	}

	// check if volume is mounted
	if !abnormal {
		csmlog.WithContext(ctx).Debug("---- check 1 ----")
		replace := CSIPrefix + "-" + s.getClusterPrefix() + "-"
		volName = strings.Replace(volName, replace, "", 1)
		// remove the namespace from the volName as the mount paths will not have it
		volName = strings.Join(strings.Split(volName, "-")[:2], "-")
		isMounted, err := isVolumeMounted(ctx, volName, volPath)
		csmlog.WithContext(ctx).Debugf("---- isMounted ---- %t", isMounted)
		if err != nil {
			abnormal = true
			msg = fmt.Sprintf("Error getting mount info for volume %s", id)
		}
		if err == nil && !isMounted {
			abnormal = true
			msg = fmt.Sprintf("no mount info for volume %s", id)
		}
	}

	if !abnormal {
		csmlog.WithContext(ctx).Debug("---- check 2 ----")
		// check if volume path is accessible
		_, err = os.ReadDir(volPath)
		if err != nil {
			abnormal = true
			msg = fmt.Sprintf("volume Path is not accessible: %s", err)
		}
		csmlog.WithContext(ctx).Debug("---- path is readable ----")
	}
	if abnormal {
		// return the response based on abnormal condition
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Unit:      csi.VolumeUsage_UNKNOWN,
					Available: 0,
					Total:     0,
					Used:      0,
				},
			},
			VolumeCondition: &csi.VolumeCondition{
				Abnormal: abnormal,
				Message:  msg,
			},
		}, nil
	}
	// Get Volume stats metrics
	availableBytes, totalBytes, usedBytes, totalInodes, freeInodes, usedInodes, err := getVolumeStats(ctx, volPath)
	if err != nil {
		return &csi.NodeGetVolumeStatsResponse{
			Usage: []*csi.VolumeUsage{
				{
					Unit:      csi.VolumeUsage_UNKNOWN,
					Available: availableBytes,
					Total:     totalBytes,
					Used:      usedBytes,
				},
			},
			VolumeCondition: &csi.VolumeCondition{
				Abnormal: false,
				Message:  fmt.Sprintf("failed to get volume stats metrics : %s", err),
			},
		}, nil
	}

	// return the response with all the usage
	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: availableBytes,
				Total:     totalBytes,
				Used:      usedBytes,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: freeInodes,
				Total:     totalInodes,
				Used:      usedInodes,
			},
		},
		VolumeCondition: &csi.VolumeCondition{
			Abnormal: abnormal,
			Message:  "Volume in use",
		},
	}, nil
}

// getVolumeStats - Returns the stats for the volume mounted on given volume path
func getVolumeStats(ctx context.Context, volumePath string) (int64, int64, int64, int64, int64, int64, error) {
	availableBytes, totalBytes, usedBytes, totalInodes, freeInodes, usedInodes, err := gofsutil.FsInfo(ctx, volumePath)
	if err != nil {
		return 0, 0, 0, 0, 0, 0, status.Error(codes.Internal, fmt.Sprintf(
			"failed to get volume stats: %s", err,
		))
	}
	return availableBytes, totalBytes, usedBytes, totalInodes, freeInodes, usedInodes, err
}

// isVolumeMounted fetches the mount info for a volume
// and compare the volume mount path with the target
func isVolumeMounted(ctx context.Context, volName string, target string) (bool, error) {
	devmnt, err := gofsutil.GetMountInfoFromDevice(ctx, volName)
	if err != nil {
		return false, status.Errorf(codes.Internal,
			"could not reliably determine existing mount status: '%s'",
			err.Error())
	}
	if strings.Contains(devmnt.MountPoint, target) {
		return true, nil
	}

	// No mount exists, volume is not published
	csmlog.WithContext(ctx).Debugf("target '%s' does not exist", target)
	return false, nil
}

// nodeStartup performs a few necessary functions for the nodes to function properly
// - validates that at least one iSCSI initiator is defined
// - validates that a connection to Unisphere exists
// - invokes nodeHostSetup in a thread
//
// returns an error if unable to perform node startup tasks without error
func (s *service) nodeStartup(ctx context.Context) error {
	if s.nodeIsInitialized {
		return nil
	}
	// Maximum number of pending requests before overload returned
	nodePendingState.maxPending = 10

	// Copy the multipath.conf file from /noderoot/etc/multipath.conf (EnvNodeChroot)to /etc/multipath.conf if present
	// iscsiChroot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)
	// copyMultipathConfigFile(iscsiChroot, "")

	// make sure we have a connection to Unisphere
	if s.adminClient == nil {
		return fmt.Errorf("There is no Unisphere connection")
	}
	portWWNs := make([]string, 0)
	IQNs := make([]string, 0)
	hostNQN := make([]string, 0)
	var err error

	if !s.opts.IsVsphereEnabled {

		// Get fibre channel initiators
		portWWNs, err = gofsutil.GetFCHostPortWWNs(context.Background())
		if err != nil {
			csmlog.WithContext(ctx).Errorf("nodeStartup could not GetFCHostPortWWNs %s", err.Error())
		}

		// Get iscsi initiators.
		IQNs, err = s.iscsiClient.GetInitiators("")
		if err != nil {
			csmlog.WithContext(ctx).Errorf("nodeStartup could not GetInitiatorIQNs: %s", err.Error())
		}

		// Get nmve initiators
		hostNQN, err = s.nvmetcpClient.GetInitiators("")
		if err != nil {
			csmlog.WithContext(ctx).Errorf("nodeStartup could not GetInitiatorHostNQNs: %s", err.Error())
		}

		csmlog.WithContext(ctx).Infof("TransportProtocol %s FC portWWNs: %s ... IQNs: %s ... HostNQNs: %s ", s.opts.TransportProtocol, portWWNs, IQNs, hostNQN)

		// The driver needs at least one FC or iSCSI or NVME initiator to be defined
		if len(portWWNs) == 0 && len(IQNs) == 0 && len(hostNQN) == 0 {
			csmlog.WithContext(ctx).Errorf("No FC, iSCSI or NVMe initiators were found and at least 1 is required")
			s.useNFS = true
			return nil
		}

		err = s.nodeHostSetup(ctx, portWWNs, IQNs, hostNQN, s.opts.ManagedArrays)
		if err != nil {
			return err
		} // #nosec G20
	} else {
		err := s.setVMHost()
		if err != nil {
			return err
		}
		csmlog.WithContext(ctx).Debug("vmHost created successfully")
		s.useFC = true
		s.nodeIsInitialized = true
	}

	go s.startAPIService(ctx)
	return err
}

// setVMHost create client for the vCenter
func (s *service) setVMHost() error {
	// Create a VM host
	host, err := NewVMHost(true, s.opts.VCenterHostURL, s.opts.VCenterHostUserName, s.opts.VCenterHostPassword)
	if err != nil {
		csmlog.Errorf("can not create VM host object: (%s)", err.Error())
		return fmt.Errorf("Can not create VM host object: (%s)", err.Error())
	}
	vmHost = host
	return nil
}

func (s *service) getVMHostSystem() (*object.HostSystem, error) {
	// Find the host system
	host, err := vmHost.VM.HostSystem(vmHost.Ctx)
	if err != nil {
		if strings.Contains(err.Error(), "NotAuthenticated") {
			// Missing VMhost object, recreate

			err = s.setVMHost()
			if err != nil {
				return nil, err
			}
			// Find the host system
			host, err := vmHost.VM.HostSystem(vmHost.Ctx)
			if err != nil {
				return nil, err
			}
			return host, nil
		}
		return nil, err
	}
	return host, nil
}

func isValidHostID(hostID string) bool {
	rx := regexp.MustCompile("^[a-zA-Z0-9]{1}[a-zA-Z0-9_\\-]*$")
	return rx.MatchString(hostID)
}

// topologyLabelValueRegex matches the Kubernetes label value syntax: up to 63
// characters, starting and ending with an alphanumeric, with dashes, underscores
// and dots allowed in between.
var topologyLabelValueRegex = regexp.MustCompile(`^[a-zA-Z0-9]([-_.a-zA-Z0-9]*[a-zA-Z0-9])?$`)

// isValidTopologyLabelValue reports whether v can be published as a NodeGetInfo
// topology segment value. kubelet copies these segments onto the Node object as
// labels, and an invalid value makes the whole Node update fail.
func isValidTopologyLabelValue(v string) bool {
	if v == "" || len(v) > 63 {
		return false
	}
	return topologyLabelValueRegex.MatchString(v)
}

// verifyAndUpdateInitiatorsInADiffHost verifies that a set of node initiators are not in a different host than expected.
// If they are, it updates the host name for the initiators if ModifyHostName env variable is set.
// These can be either FC initiators (hex numbers) or iSCSI initiators (starting with iqn.)
// It returns the number of initiators for the host that were found.
// Do not mix both FC and iSCSI initiators in a single call.
func (s *service) verifyAndUpdateInitiatorsInADiffHost(ctx context.Context, symID string, nodeInitiators []string, hostID string, pmaxClient pmax.Pmax) ([]string, error) {
	validInitiators := make([]string, 0)
	var errormsg string
	initList, err := pmaxClient.GetInitiatorList(ctx, symID, "", false, false)
	if err != nil {
		csmlog.WithContext(ctx).Warn("Failed to fetch initiator list for the SYM :" + symID)
		return validInitiators, err
	}
	hostUpdated := false
	for _, nodeInitiator := range nodeInitiators {
		if strings.HasPrefix(nodeInitiator, "0x") {
			nodeInitiator = strings.Replace(nodeInitiator, "0x", "", 1)
		}

		for _, initiatorID := range initList.InitiatorIDs {
			if initiatorID == nodeInitiator || strings.Contains(initiatorID, nodeInitiator) {
				csmlog.WithContext(ctx).Infof("Checking initiator %s against host %s", initiatorID, hostID)
				initiator, err := pmaxClient.GetInitiatorByID(ctx, symID, initiatorID)
				if err != nil {
					csmlog.WithContext(ctx).Warn("Failed to fetch initiator details for initiator: " + initiatorID)
					continue
				}
				if initiator.Host != "" {
					if !strings.EqualFold(initiator.Host, hostID) {
						if s.opts.ModifyHostName {
							if !hostUpdated {
								// User has set ModifyHostName to modify host name in case of a mismatch
								csmlog.WithContext(ctx).Infof("UpdateHostName processing: %s to %s", initiator.Host, hostID)
								_, err := pmaxClient.UpdateHostName(ctx, symID, initiator.Host, hostID)
								if err != nil {
									errormsg = fmt.Sprintf("Failed to change host name from %s to %s: %s", initiator.Host, hostID, err)
									csmlog.WithContext(ctx).Warn(errormsg)
									continue
								}
								hostUpdated = true
							} else {
								errormsg = fmt.Sprintf("Skipping Updating Host %s for initiator: %s as updated host already present on: %s", initiator.Host,
									initiatorID, symID)
								csmlog.WithContext(ctx).Warn(errormsg)
								continue
							}
						} else {
							errormsg = fmt.Sprintf("initiator: %s is already a part of a different host: %s on: %s",
								initiatorID, initiator.Host, symID)
							csmlog.WithContext(ctx).Warn(errormsg)
							continue
						}
					}
				}
				csmlog.WithContext(ctx).Infof("Valid initiator: %s", initiatorID)
				validInitiators = appendIfMissing(validInitiators, nodeInitiator)
				errormsg = ""
			}
		}
	}
	if 0 < len(errormsg) {
		return validInitiators, fmt.Errorf("%s", errormsg)
	}

	if len(validInitiators) == 0 && strings.Contains(hostID, NvmeTCPTransportProtocol) {
		csmlog.WithContext(ctx).Infof("No existing NVMe hosts; new hosts will be created for all discovered initiators")
		return nodeInitiators, nil
	}

	return validInitiators, nil
}

// nodeHostSeup performs a few necessary functions for the nodes to function properly
// For FC:
// - A Host exists
// For ISCSI:
// - a Host exists within PowerMax, to identify this node
// - The Host contains the discovered iSCSI initiators
// - performs an iSCSI login
// For NVMETCP:
// - a Host exists within PowerMax, to identify this node
// - The Host contains the discovered NVMe initiators
// - performs an NVMeTCP login
func (s *service) nodeHostSetup(ctx context.Context, portWWNs []string, IQNs []string, NQNs []string, symmetrixIDs []string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	csmlog.WithContext(ctx).Info("**************************nodeHostSetup executing...*******************************")
	defer csmlog.WithContext(ctx).Info("**************************nodeHostSetup completed...*******************************")

	// we need to randomize a time before starting the interaction with unisphere
	// in order to reduce the concurrent workload on the system

	// determine a random delay period
	period := rand.Int() % maximumStartupDelay // #nosec G404
	// sleep ...
	csmlog.WithContext(ctx).Infof("Waiting for %d seconds", period)
	time.Sleep(time.Duration(period) * time.Second)

	// See if it's viable to use FC and/or ISCSI and/or NVMeTCP
	hostIDIscsi, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(s.csiNodeID())
	hostIDFC, _, _ := s.GetFCHostSGAndMVIDFromNodeID(s.csiNodeID())
	hostIDNVMeTCP, _, _ := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.csiNodeID())

	// Arrays whose host adoption could not be completed. The node is not considered
	// initialized while any remain, so the next Probe retries them instead of
	// leaving the node permanently unusable for those arrays.
	adoptionFailedArrays := make([]string, 0)

	// Loop through the symmetrix, looking for existing initiators
	for _, symID := range symmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(symID)
		if err != nil {
			csmlog.WithContext(ctx).Error(err.Error())
			continue
		}
		if s.arrayTransportProtocolMap == nil {
			s.arrayTransportProtocolMap = make(map[string]string)
		}
		if s.opts.IsVsphereEnabled {
			err := s.getHostForVsphere(ctx, symID, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Warnf("Host/HostGroup %s was not initialized on sym %s, err: %s", s.opts.VSphereHostName, symID, err.Error())
			} else {
				s.arrayTransportProtocolMap[symID] = Vsphere
			}
			continue
		}

		// Host adoption: when mode is "adopt" and FC WWPNs are present,
		// attempt to discover and adopt a pre-existing host before standard creation.
		fcAdopted := false
		if s.opts.HostManagementMode == HostMgmtModeAdopt && len(portWWNs) > 0 {
			adoptedHostName, adoptErr := s.discoverAndAdoptHost(ctx, symID, portWWNs, pmaxClient)
			if adoptErr != nil {
				csmlog.WithContext(ctx).Errorf("Host adoption failed on array %s for node %s: %s", symID, s.k8sNodeName(), adoptErr.Error())
				// Emit HostAdoptionFailed event for observability
				if recorder := initEventRecorder(); recorder != nil {
					nodeRef := &corev1.ObjectReference{
						Kind: "Node", Name: s.k8sNodeName(),
					}
					recorder.Eventf(nodeRef, corev1.EventTypeWarning, "HostAdoptionFailed",
						"Host adoption failed on array %s: %s", symID, adoptErr.Error())
				}
				// Per-array scope (RACE-3): record the failure so nodeStartup can be
				// retried for this array, and move on to the remaining arrays rather
				// than falling through to host creation.
				adoptionFailedArrays = append(adoptionFailedArrays, symID)
				continue
			}
			if adoptedHostName != "" {
				// Adoption succeeded — set FC protocol directly
				s.useFC = true
				nodeChroot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)
				s.initFCConnector(nodeChroot)
				s.arrayTransportProtocolMap[symID] = FcTransportProtocol
				isSymConnFC.Store(symID, true)
				fcAdopted = true
				// resetOtherProtocols — same as standard FC path
				s.useNVMeTCP = false
				s.useIscsi = false
				csmlog.WithContext(ctx).Infof("Host adoption: using adopted host %s for array %s, skipping standard host creation", adoptedHostName, symID)

				// Detect boot LUNs on the adopted host
				bootLUNs, detectErr := s.detectBootLUNs(ctx, symID, adoptedHostName, pmaxClient)
				if detectErr != nil {
					csmlog.WithContext(ctx).Warnf("Boot LUN detection failed for host %s on array %s: %s",
						adoptedHostName, symID, detectErr.Error())
				} else if len(bootLUNs) > 0 {
					totalBootVols := 0
					for _, bl := range bootLUNs {
						totalBootVols += bl.NumVolumes
					}
					csmlog.WithContext(ctx).Infof("Boot LUN detection: found %d boot/pre-existing volume(s) across %d non-CSI storage group(s) on host %s array %s",
						totalBootVols, len(bootLUNs), adoptedHostName, symID)
					// FR-3.3: the counter reports non-CSI volumes detected, not storage groups.
					bootLUNsDetectedTotal.Add(float64(totalBootVols))
					// Emit Kubernetes event for boot LUN detection
					if recorder := initEventRecorder(); recorder != nil {
						nodeRef := &corev1.ObjectReference{
							Kind: "Node", Name: s.k8sNodeName(),
						}
						recorder.Eventf(nodeRef, corev1.EventTypeNormal, "BootLUNDetected",
							"Detected %d boot/pre-existing volume(s) in %d non-CSI storage group(s) on host %s array %s",
							totalBootVols, len(bootLUNs), adoptedHostName, symID)
					}
					// Store boot LUN info for protection during volume operations
					s.setAdoptedHostBootLUNs(symID, bootLUNs)
				}
			}
			// adoptedHostName == "" means no host found — fall through to standard flow
		}

		// When an adopted host has been set for this array the transport protocol is
		// already settled as FC. Running the iSCSI/NVMeTCP discovery below would
		// re-enable those protocols (s.useIscsi is set whenever TransportProtocol is
		// unset) and the standard setup path would then create a competing CSI host
		// object and overwrite arrayTransportProtocolMap for this array.
		var validFCs, validNVMeTCPs, validIscsis []string
		if !fcAdopted {
			validFCs, err = s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, portWWNs, hostIDFC, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Error("Could not validate FC initiators " + err.Error())
			}
			csmlog.WithContext(ctx).Infof("valid FC initiators: %v", validFCs)
			if len(validFCs) > 0 && (s.opts.TransportProtocol == "" || s.opts.TransportProtocol == FcTransportProtocol) {
				// We do have to have pre-existing initiators that were zoned for FC
				s.useFC = true
			}

			validNVMeTCPs, err = s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, NQNs, hostIDNVMeTCP, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Error("Could not validate NVMeTCP initiators " + err.Error())
			} else if len(validNVMeTCPs) > 0 && s.opts.TransportProtocol == "" || s.opts.TransportProtocol == NvmeTCPTransportProtocol {
				// If pre-existing NVMeTCP initiators are not found, initiators/host should be created
				s.useNVMeTCP = true
			}
			csmlog.WithContext(ctx).Infof("valid NVMeTCP initiators: %v", validNVMeTCPs)

			validIscsis, err = s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, IQNs, hostIDIscsi, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Error("Could not validate iSCSI initiators" + err.Error())
			} else if s.opts.TransportProtocol == "" || s.opts.TransportProtocol == IscsiTransportProtocol {
				// We do not have to have pre-existing initiators to use Iscsi (we can create them)
				s.useIscsi = true
			}
			csmlog.WithContext(ctx).Infof("valid (existing) iSCSI initiators (must be manually created): %v", validIscsis)
			if len(validIscsis) == 0 {
				// IQNs are not yet part of any host on Unisphere
				validIscsis = IQNs
			}
		}

		if !s.useFC && !s.useIscsi && !s.useNVMeTCP {
			csmlog.WithContext(ctx).Error("No valid initiators- could not initialize NVMeTCP or FC or iSCSI")
			return err
		}

		nodeChroot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)

		if s.useNVMeTCP && !fcAdopted {
			// check nvme module availability on the host
			err = s.setupArrayForNVMeTCP(ctx, symID, validNVMeTCPs, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to do the NVMe setup for the Array(%s). Error - %s", symID, err.Error())
			} else {
				s.initNVMeTCPConnector(nodeChroot)
				s.arrayTransportProtocolMap[symID] = NvmeTCPTransportProtocol
				// resetOtherProtocols
				s.useFC = false
				s.useIscsi = false
			}
		}

		if s.useFC && !fcAdopted {
			formattedFCs := make([]string, 0)
			for _, initiatorID := range validFCs {
				elems := strings.Split(initiatorID, ":")
				formattedFCs = appendIfMissing(formattedFCs, "0x"+elems[len(elems)-1])
			}
			err := s.setupArrayForFC(ctx, symID, formattedFCs, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to do the FC setup the Array(%s). Error - %s", symID, err.Error())
			} else {
				s.initFCConnector(nodeChroot)
				s.arrayTransportProtocolMap[symID] = FcTransportProtocol
				isSymConnFC.Store(symID, true)
				// resetOtherProtocols
				s.useNVMeTCP = false
				s.useIscsi = false
			}
		}

		if s.useIscsi && !fcAdopted {
			err := s.ensureISCSIDaemonStarted()
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to start the ISCSI Daemon. Error - %s", err.Error())
			}
			err = s.setupArrayForIscsi(ctx, symID, validIscsis, pmaxClient)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to do the ISCSI setup for the Array(%s). Error - %s", symID, err.Error())
			} else {
				s.initISCSIConnector(nodeChroot)
				s.arrayTransportProtocolMap[symID] = IscsiTransportProtocol
				// resetOtherProtocols
				s.useNVMeTCP = false
				s.useNFS = false
			}
		}
	}

	if len(adoptionFailedArrays) > 0 {
		// Leave nodeIsInitialized false so the next Probe re-runs nodeStartup and
		// retries adoption for these arrays. NodeGetInfo would otherwise publish a
		// topology segment that is permanently missing their adopted host keys.
		return fmt.Errorf("Host adoption failed on array(s) %v for node %s",
			adoptionFailedArrays, s.k8sNodeName())
	}

	// Even if all block protocols failed, we mark the node
	// as initialized so that the node can be used for NFS mounts.
	// If this behavor will need to change, this flag would be removed from here
	// and instead would be set in each of the protocol setup blocks above on success.
	s.nodeIsInitialized = true

	return nil
}

// getHostForVsphere fetches predefined host or host group from array for vSphere
func (s *service) getHostForVsphere(ctx context.Context, array string, pmaxClient pmax.Pmax) (err error) {
	// Check if the Host exist
	_, err = pmaxClient.GetHostByID(ctx, array, s.opts.VSphereHostName)
	if err != nil {
		if strings.Contains(err.Error(), "cannot be found") {
			// Check if the HostGroup exist
			_, err = pmaxClient.GetHostGroupByID(ctx, array, s.opts.VSphereHostName)
		}
	}
	return err
}

func (s *service) setupArrayForFC(ctx context.Context, array string, portWWNs []string, pmaxClient pmax.Pmax) error {
	hostName, _, mvName := s.GetFCHostSGAndMVIDFromNodeID(s.csiNodeID())
	csmlog.WithContext(ctx).Infof("setting up array %s for Fibrechannel, host name: %s masking view: %s", array, hostName, mvName)
	_, err := s.createOrUpdateFCHost(ctx, array, hostName, portWWNs, pmaxClient)
	return err
}

// setupArrayForIscsi is called to set up a node for iscsi operation.
func (s *service) setupArrayForIscsi(ctx context.Context, array string, IQNs []string, pmaxClient pmax.Pmax) error {
	hostName, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.csiNodeID())
	csmlog.WithContext(ctx).Infof("setting up array %s for Iscsi, host name: %s masking view ID: %s %v", array, hostName, mvName, IQNs)

	// Create or update the IscsiHost and Initiators
	_, err := s.createOrUpdateIscsiHost(ctx, array, hostName, IQNs, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return err
	}
	_, err = s.getAndConfigureMaskingViewTargets(ctx, array, mvName, IQNs, pmaxClient)
	if err != nil && !(strings.Contains(err.Error(), "Masking View") && strings.Contains(err.Error(), "cannot be found")) {
		csmlog.WithContext(ctx).Error(err.Error())
		return err
	}
	return nil
}

// setupArrayForNVMeTCP is called to set up a node for NVMe operation.
func (s *service) setupArrayForNVMeTCP(ctx context.Context, array string, NQNs []string, pmaxClient pmax.Pmax) error {
	hostName, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.csiNodeID())
	csmlog.WithContext(ctx).Infof("setting up array %s for NVMeTCP, host name: %s masking view ID: %s %v", array, hostName, mvName, NQNs)

	nvmeHostID, err := s.nvmetcpClient.GetHostID()
	if err != nil {
		return fmt.Errorf("failed to get local NVMe host ID: %v", err)
	}

	initiatorIDs, err := makeNVMeInitiatorIDs(NQNs, nvmeHostID)
	if err != nil {
		return fmt.Errorf("failed to make NVMe initiator IDs (hostNQN:hostID): %v", err)
	}
	csmlog.WithContext(ctx).Infof("Using NVMe initiator IDs (hostNQN:hostID): %v", initiatorIDs)

	// Create or update the NVMe Host with initiator reference
	_, err = s.createOrUpdateNVMeTCPHost(ctx, array, hostName, initiatorIDs, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return err
	}

	// Discover targets on the host and connect initiators
	err = s.setupNVMeTCPTargetDiscovery(ctx, array, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return err
	}

	// It may take some time for the array to start reporting the newly logged in initiators.
	// To accommodate this, retry a few times before erroring out.

	// Wait for the NQNs to appear as in the array initiators list
	for attempt := 1; attempt <= pmaxQueryAttempts; attempt++ {
		if attempt > 1 { // First attempt does not need to wait
			// Sleep 10 seconds or until context is closed
			select {
			case <-ctx.Done():
				return fmt.Errorf("failed to validate NVMe initiators on array: context timeout")
			case <-time.After(10 * time.Second):
			}
		}
		csmlog.WithContext(ctx).Infof("Attempting to validate NVMe initiators on array (%d)", attempt)
		err = s.validateNVMeInitiators(ctx, array, initiatorIDs, pmaxClient)
		if err == nil {
			break // Success
		}
		csmlog.WithContext(ctx).Errorf("Not all NVMe initiators were found on array %s: %v", array, err)
	}

	return nil
}

func (s *service) validateNVMeInitiators(ctx context.Context, symID string, initiatorIDs []string, pmaxClient pmax.Pmax) error {
	// Get all initiators from the array and search for our local NQNs
	allInitiators, err := pmaxClient.GetInitiatorList(ctx, symID, "", false, false)
	if err != nil || allInitiators == nil {
		return fmt.Errorf("failed to get all initiators: %v", err)
	}
	csmlog.WithContext(ctx).Debugf("All initiators on array: %v", allInitiators.InitiatorIDs)

	for _, localID := range initiatorIDs {
		// localID = [nqn.2014-08.org.nvmexpress:uuid:27212f42-27d7-3250-5c80-b02adbbb66b5:76B04D56EAB26A2E1509A7E98D3DFDB6]
		nqnFound := false
		for _, initiatorID := range allInitiators.InitiatorIDs {
			// initiatorID = OR-1C:001:nqn.2014-08.org.nvmexpress:uuid:27212f42-27d7-3250-5c80-b02adbbb66b5:76B04D56EAB26A2E1509A7E98D3DFDB6
			if strings.HasSuffix(initiatorID, localID) {
				nqnFound = true
				break
			}
		}
		if !nqnFound {
			return fmt.Errorf("Initiator %s not found", localID)
		}
	}

	csmlog.WithContext(ctx).Infof("All local NVMe initiators are registered on the array")

	return nil
}

// getAndConfigureMaskingViewTargets - Returns a list of ISCSITargets for a given masking view
// also update the node database with CHAP authentication (if required) and perform discovery/login
func (s *service) getAndConfigureMaskingViewTargets(ctx context.Context, array, mvName string, IQNs []string, pmaxClient pmax.Pmax) ([]goiscsi.ISCSITarget, error) {
	// Check the masking view
	goISCSITargets := make([]goiscsi.ISCSITarget, 0)
	view, err := pmaxClient.GetMaskingViewByID(ctx, array, mvName)
	if err != nil {
		// masking view does not exist, not an error but no need to login
		csmlog.WithContext(ctx).Debugf("Masking View %s does not exist for array %s, skipping login", mvName, array)
		return goISCSITargets, err
	}
	csmlog.WithContext(ctx).Infof("Masking View: %s exists for array id: %s", mvName, array)
	// masking view exists, we need to log into some targets
	// this will also update the cache
	targets, err := s.getIscsiTargetsForMaskingView(ctx, array, view, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Debugf("%s", err.Error())
		return goISCSITargets, err
	}
	for _, tgt := range targets {
		goISCSITargets = append(goISCSITargets, tgt.target)
	}
	csmlog.WithContext(ctx).Debugf("Masking View Targets: %v", goISCSITargets)
	// First set the CHAP credentials
	if len(targets) > 0 {
		err = s.setCHAPCredentials(array, targets, IQNs)
		if err != nil {
			errorMsg := "Unable to set ISCSI CHAP credentials for some targets in the node database"
			csmlog.WithContext(ctx).Errorf("%s Error - %s", errorMsg, err.Error())
			return goISCSITargets, err
		}
		err = s.loginIntoISCSITargets(array, targets)
	}
	return goISCSITargets, err
}

// getAndConfigureMaskingViewTargets - Returns a list of NVMeTargets for a given masking view
// also update the node database with CHAP authentication (if required) and perform discovery/login
func (s *service) getAndConfigureMaskingViewTargetsNVMeTCP(ctx context.Context, array, mvName string, pmaxClient pmax.Pmax) ([]gonvme.NVMeTarget, error) {
	// Check the masking view
	goNVMeTargets := make([]gonvme.NVMeTarget, 0)
	view, err := pmaxClient.GetMaskingViewByID(ctx, array, mvName)
	if err != nil {
		// masking view does not exist, not an error but no need to login
		csmlog.WithContext(ctx).Debugf("Masking View %s does not exist for array %s, skipping login", mvName, array)
		return goNVMeTargets, err
	}
	csmlog.WithContext(ctx).Infof("Masking View: %s exists for array id: %s", mvName, array)
	// masking view exists, we need to log into some targets
	// this will also update the cache
	targets, err := s.getNVMeTCPTargetsForMaskingView(ctx, array, view, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Debugf("%s", err.Error())
		return goNVMeTargets, err
	}
	for _, tgt := range targets {
		goNVMeTargets = append(goNVMeTargets, tgt.target)
	}
	csmlog.WithContext(ctx).Debugf("Masking View Targets: %v", goNVMeTargets)
	// Login to targets
	if len(targets) > 0 {
		err = s.loginIntoNVMeTCPTargets(array, targets)
	}
	return goNVMeTargets, err
}

// setupNVMeTCPTargetDiscovery is called to discover NVMe targets from the host/node.
// It retrieves the NVMeTCP targets (portal + NQN) scoped to the configured port groups,
// then connects directly to only those targets, avoiding connections to targets
// outside the port group scope.
func (s *service) setupNVMeTCPTargetDiscovery(ctx context.Context, array string, pmaxClient pmax.Pmax) error {
	// Host-managed mode forbids both halves of this path: discovery is an NVMe
	// fabric operation and the connect that follows it creates a session the
	// driver does not own. Evaluate the host's existing sessions instead (FR-4).
	if s.isNVMeTCPHostManaged() {
		expected, err := s.expectedHostManagedNVMeTCPTargets(ctx, array, pmaxClient)
		if err != nil {
			return err
		}
		return s.adoptHostManagedNVMeTCPSessions(ctx, array, expected)
	}

	var combinedErrors []string
	atLeastOneConnected := false
	var totalTargetsConnected int

	portGroupTargets, err := getNVMeTCPTargetsFromPortGroups(s, ctx, array, s.opts.PortGroups, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("unable to fetch NVMeTCP targets from port groups for %s: %s", array, err.Error())
		return err
	}

	if len(portGroupTargets) == 0 {
		return fmt.Errorf("couldn't find any NVMeTCP targets on any of the port-groups %s", s.opts.PortGroups)
	}

	for _, tgt := range portGroupTargets {
		csmlog.WithContext(ctx).Debugf("Connecting to NVMe target %s on portal %s", tgt.TargetNqn, tgt.Portal)
		connectError := s.nvmetcpClient.NVMeTCPConnect(tgt, false)
		if connectError != nil {
			csmlog.WithContext(ctx).Errorf("Failed to connect to NVMe target %s on portal %s. Error: %s",
				tgt.TargetNqn, tgt.Portal, connectError.Error())
			combinedErrors = append(combinedErrors, fmt.Sprintf("target: %s, portal: %s, Error: %s",
				tgt.TargetNqn, tgt.Portal, connectError.Error()))
		} else {
			csmlog.WithContext(ctx).Infof("Successfully connected to NVMe target %s on portal %s", tgt.TargetNqn, tgt.Portal)
			atLeastOneConnected = true
			totalTargetsConnected++
		}
	}

	if !atLeastOneConnected {
		return fmt.Errorf("failed to connect to NVMe targets on any of the port-groups %s. Errors: %s",
			s.opts.PortGroups, strings.Join(combinedErrors, "; "))
	}
	csmlog.WithContext(ctx).Infof("Connected to %d NVMe targets out of %d from port-groups %s",
		totalTargetsConnected, len(portGroupTargets), strings.Join(s.opts.PortGroups, ", "))
	return nil
}

// loginIntoISCSITargets - for a given array id and list of masking view targets
// attempt login. The login method is different if CHAP is enabled
// also update the logged in arrays cache
func (s *service) loginIntoISCSITargets(array string, targets []maskingViewTargetInfo) error {
	var combinedErrors []string
	atLeastOneLoggedIn := false
	var totalTargetsDiscovered int

	var err error
	for _, tgt := range targets {
		if s.opts.EnableCHAP {
			err = s.iscsiClient.PerformLogin(tgt.target)
		} else {
			_, err = s.iscsiClient.DiscoverTargets(tgt.target.Portal, true)
		}
		if err != nil {
			csmlog.Errorf("Failed to login to target %s: %v",
				tgt.target.Target, err)
			combinedErrors = append(combinedErrors, fmt.Sprintf("target: %s, Error: %v",
				tgt.target.Target, err))
		} else {
			s.iscsiTargets[array] = append(s.iscsiTargets[array], tgt.target.Target)
			csmlog.Infof("Successfully logged to target: %s", tgt.target.Target)
			atLeastOneLoggedIn = true
			totalTargetsDiscovered++
		}
	}

	// If we successfully logged into at least one target, then mark the array as logged in
	if atLeastOneLoggedIn {
		s.UpdateLoggedInArrays(array, true)
	} else {
		return fmt.Errorf("failed to login to ISCSI targets on any of the portals. Errors: %s",
			strings.Join(combinedErrors, "; "))
	}
	csmlog.Infof("Logged into %d ISCSI targets out of %d on array %s",
		totalTargetsDiscovered, len(targets), array)
	return nil
}

func (s *service) UpdateLoggedInArrays(array string, value bool) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	s.loggedInArrays[array] = value
}

func (s *service) GetLoggedInArrays(array string) (isLoggedIn bool, ok bool) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	isLoggedIn, ok = s.loggedInArrays[array]
	return isLoggedIn, ok
}

// loginIntoNVMeTCPTargets - for a given array id and list of masking view targets
// connect directly to the specified targets (portal + NQN) without broad discovery.
// This ensures only port-group-scoped targets are connected.
// Also update the logged in arrays cache.
func (s *service) loginIntoNVMeTCPTargets(array string, targets []maskingViewNVMeTargetInfo) error {
	// In host-managed mode the host owns the fabric sessions. Use the sessions it
	// established for these masking-view targets and issue no connect (FR-4).
	if s.isNVMeTCPHostManaged() {
		expected := make([]NVMeTCPTargetInfo, 0, len(targets))
		for _, tgt := range targets {
			expected = append(expected, NVMeTCPTargetInfo{
				Target: tgt.target.TargetNqn,
				Portal: tgt.target.Portal,
			})
		}
		return s.adoptHostManagedNVMeTCPSessions(context.Background(), array, expected)
	}

	var combinedErrors []string
	atLeastOneLoggedIn := false
	var totalTargetsConnected int
	for _, tgt := range targets {
		csmlog.Debugf("Connecting to NVMe target %s on portal %s", tgt.target.TargetNqn, tgt.target.Portal)
		connectError := s.nvmetcpClient.NVMeTCPConnect(tgt.target, false)
		if connectError != nil {
			csmlog.Errorf("Failed to connect to the NVMe target: %s on portal %s. Error: %s",
				tgt.target.TargetNqn, tgt.target.Portal, connectError.Error())
			combinedErrors = append(combinedErrors, fmt.Sprintf("target: %s, portal: %s, Error: %s",
				tgt.target.TargetNqn, tgt.target.Portal, connectError.Error()))
		} else {
			nvmeTgts, ok := s.nvmeTargets.Load(array)
			if !ok {
				nvmeTgts = []string{}
			}
			s.nvmeTargets.Store(array, append(nvmeTgts.([]string), tgt.target.TargetNqn))
			csmlog.Infof("Successfully connected to target: %s on portal: %s",
				tgt.target.TargetNqn, tgt.target.Portal)
			atLeastOneLoggedIn = true
			totalTargetsConnected++
		}
	}

	// If we successfully connected to at least one target, then mark the array as logged in
	if atLeastOneLoggedIn {
		s.UpdateLoggedInNVMeArrays(array, true)
	} else {
		return fmt.Errorf("failed to connect to NVMe targets on any of the portals. Errors: %s",
			strings.Join(combinedErrors, "; "))
	}
	csmlog.Infof("Connected to %d NVMe targets out of %d on array %s", totalTargetsConnected, len(targets), array)
	return nil
}

func (s *service) UpdateLoggedInNVMeArrays(array string, value bool) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	s.loggedInNVMeArrays[array] = value
}

func (s *service) GetLoggedInNVMeArrays(array string) (isLoggedIn bool, ok bool) {
	s.cacheMutex.Lock()
	defer s.cacheMutex.Unlock()
	isLoggedIn, ok = s.loggedInNVMeArrays[array]
	return isLoggedIn, ok
}

// setCHAPCredentials - Sets the CHAP credentials for a list of masking view targets
// in the node database. Also if any credentials were updated, it updates the target cache
func (s *service) setCHAPCredentials(array string, targets []maskingViewTargetInfo, IQNs []string) error {
	if s.opts.EnableCHAP {
		if len(IQNs) > 0 {
			if s.opts.CHAPUserName != "" {
				errorMsg := "multiple IQNs found on host and CHAP username is not set. invalid configuration"
				csmlog.Debugf("%s", errorMsg)
				return fmt.Errorf("%s", errorMsg)
			}
		}
		chapUserName := s.opts.CHAPUserName
		modified := false
		for i := range targets {
			if chapUserName == "" {
				chapUserName = IQNs[0]
			}
			if !targets[i].IsCHAPConfigured {
				csmlog.Debugf("Setting CHAP credentials for targets: %v", targets)
				err := s.iscsiClient.SetCHAPCredentials(targets[i].target, chapUserName, s.opts.CHAPPassword)
				if err != nil {
					csmlog.Error(err.Error())
					// If we were able to set credentials for some targets successfully
					// even then we won't be updating the cache
					return err
				}
				csmlog.Debugf("Successfully set CHAP credentials for targets: %v", targets)
				targets[i].IsCHAPConfigured = true
				modified = true
			}
		}
		if modified {
			// Update the cache
			symToMaskingViewTargets.Store(array, targets)
		}
	}
	return nil
}

func getUnitStatus(units []dbus.UnitStatus, name string) *dbus.UnitStatus {
	for _, u := range units {
		if u.Name == name {
			return &u
		}
	}
	return nil
}

func (s *service) ensureISCSIDaemonStarted() error {
	target := "iscsid.service"
	err := s.createDbusConnection()
	if err != nil {
		return err
	}
	defer s.closeDbusConnection()
	units, err := s.dBusConn.ListUnits()
	if err != nil {
		csmlog.Errorf("Failed to list systemd units. Error - %s", err.Error())
		return err
	}
	unit := getUnitStatus(units, target)
	if unit == nil {
		// Failed to get the status of ISCSI Daemon
		errMsg := fmt.Sprintf("failed to find %s. Going to panic", target)
		csmlog.Error(errMsg)
		panic(fmt.Errorf("%s", errMsg))
	} else if unit.ActiveState != "active" {
		csmlog.Infof("%s is not active. Current state - %s", target, unit.ActiveState)
	} else {
		csmlog.Info("ISCSI Daemon is active")
		return nil
	}
	csmlog.Infof("Attempting to start %s", target)
	responsechan := make(chan string)
	// We try to replace the unit
	_, err = s.dBusConn.StartUnit(target, "replace", responsechan)
	if err != nil {
		// Failed to start the unit
		csmlog.Errorf("Failed to start %s. Error - %s", target, err.Error())
		if strings.Contains(err.Error(), "is masked") {
			// If unit is masked, it can't be started even manually
			panic(err)
		}
		return err
	}
	// Wait on the response channel
	job := <-responsechan
	if job != "done" {
		// Job didn't succeed
		errMsg := "Failed to get a successful response from the job to start ISCSI daemon"
		csmlog.Error(errMsg)
		return fmt.Errorf("%s", errMsg)
	}
	csmlog.Info("Successfully started ISCSI daemon")
	return nil
}

func (s *service) ensureLoggedIntoEveryArray(ctx context.Context, _ bool) error {
	arrays := &types.SymmetrixIDList{}

	// Get the list of arrays
	arrays = s.retryableGetSymmetrixIDList()

	// for each array known to unisphere, ensure we have performed ISCSI login for our masking views
	for _, array := range arrays.SymmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(array)
		csmlog.WithContext(ctx).Infof("got PowerMaxclient %+v", pmaxClient)
		if err != nil {
			csmlog.WithContext(ctx).Error(err.Error())
			continue
		}
		if _, ok := isSymConnFC.Load(array); ok {
			// Check if we have marked this array as FC earlier
			continue
		}

		if isLoggedIn, ok := s.GetLoggedInArrays(array); ok && isLoggedIn {
			// we have already logged into this array
			csmlog.WithContext(ctx).Debugf("(ISCSI) Already logged into the array: %s", array)
			break
		}
		if isLoggedIn, ok := s.GetLoggedInNVMeArrays(array); ok && isLoggedIn {
			// we have already logged into this array
			csmlog.WithContext(ctx).Debugf("(NVME) Already logged into the array: %s", array)
			break
		}
		if s.useIscsi {
			csmlog.WithContext(ctx).Debugf("(ISCSI) No logins were done earlier for %s", array)
			_, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.csiNodeID())
			csmlog.WithContext(ctx).Infof("Checking if MV %s exists", mvName)
			// Get iscsi initiators.
			IQNs, iSCSIErr := s.iscsiClient.GetInitiators("")
			if iSCSIErr != nil {
				return iSCSIErr
			}
			err = s.performIscsiLoginOnSymID(ctx, array, IQNs, mvName, pmaxClient)
			if err != nil {
				return fmt.Errorf("failed to login to (some) %s ISCSI targets. Error: %s", array, err.Error())
			}
		} else if s.useNVMeTCP {
			csmlog.WithContext(ctx).Debugf("(ISCSI) No logins were done earlier for %s", array)
			_, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.csiNodeID())
			csmlog.WithContext(ctx).Infof("Checking if MV %s exists", mvName)
			err = s.performNVMETCPLoginOnSymID(ctx, array, mvName, pmaxClient)
			if err != nil {
				return fmt.Errorf("failed to login to (some) %s ISCSI targets. Error: %s", array, err.Error())
			}
		}
	}
	return nil
}

func (s *service) performNVMETCPLoginOnSymID(ctx context.Context, array string, mvName string, pmaxClient pmax.Pmax) (err error) {
	mvTargets, ok := symToMaskingViewTargets.Load(array)
	if ok {
		err = s.loginIntoNVMeTCPTargets(array, mvTargets.([]maskingViewNVMeTargetInfo))
	} else {
		// for NVMeTCP
		_, tempErr := s.getAndConfigureMaskingViewTargetsNVMeTCP(ctx, array, mvName, pmaxClient)
		if tempErr != nil {
			if strings.Contains(tempErr.Error(), "does not exist") {
				// Ignore this error
				csmlog.WithContext(ctx).Debugf("Couldn't configure NVME targets as masking view: %s doesn't exist for array: %s",
					mvName, array)
				tempErr = nil
			} else {
				err = tempErr
				csmlog.WithContext(ctx).Errorf("Failed to configure NVME targets for masking view: %s, array: %s. Error: %s",
					mvName, array, tempErr.Error())
			}
		}
		if err != nil {
			return fmt.Errorf("failed to login in array: %s NVME targets. Error: %s", array, err.Error())
		}
	}
	return nil
}

func (s *service) performIscsiLoginOnSymID(ctx context.Context, array string, IQNs []string, mvName string, pmaxClient pmax.Pmax) (err error) {
	// Try to get the masking view targets from the cache
	mvTargets, ok := symToMaskingViewTargets.Load(array)
	if ok {
		// Entry is present in cache
		// This means that we discovered the targets
		// but haven't logged in for some reason
		csmlog.WithContext(ctx).Debugf("Cache hit for %s", array)
		maskingViewTargets := mvTargets.([]maskingViewTargetInfo)
		// First set the CHAP credentials if required
		err = s.setCHAPCredentials(array, maskingViewTargets, IQNs)
		if err != nil {
			// log the error and continue
			csmlog.WithContext(ctx).Errorf("Failed to set CHAP credentials for %v", maskingViewTargets)
			// Reset the error
			err = nil
		}
		err = s.loginIntoISCSITargets(array, maskingViewTargets)
	} else {
		// Entry not in cache
		// Configure MaskingView Targets - CHAP, Discovery/Login
		_, tempErr := s.getAndConfigureMaskingViewTargets(ctx, array, mvName, IQNs, pmaxClient)
		if tempErr != nil {
			if strings.Contains(tempErr.Error(), "does not exist") {
				// Ignore this error
				csmlog.WithContext(ctx).Debugf("Couldn't configure ISCSI targets as masking view: %s doesn't exist for array: %s",
					mvName, array)
				tempErr = nil
			} else {
				err = tempErr
				csmlog.WithContext(ctx).Errorf("Failed to configure ISCSI targets for masking view: %s, array: %s. Error: %s",
					mvName, array, tempErr.Error())
			}
		}
	}
	if err != nil {
		return fmt.Errorf("failed to login in array: %s ISCSI targets. Error: %s", array, err.Error())
	}
	return nil
}

func (s *service) getIscsiTargetsForMaskingView(ctx context.Context, array string, view *types.MaskingView, pmaxClient pmax.Pmax) ([]maskingViewTargetInfo, error) {
	if array == "" {
		return []maskingViewTargetInfo{}, fmt.Errorf("No array specified")
	}
	if view.PortGroupID == "" {
		return []maskingViewTargetInfo{}, fmt.Errorf("Masking view contains no PortGroupID")
	}
	// get the PortGroup in the masking view
	portGroup, err := pmaxClient.GetPortGroupByID(ctx, array, view.PortGroupID)
	if err != nil {
		return []maskingViewTargetInfo{}, err
	}
	targets := make([]maskingViewTargetInfo, 0)
	// for each Port
	for _, portKey := range portGroup.SymmetrixPortKey {
		pID := portKey.PortID
		port, err := pmaxClient.GetPort(ctx, array, portKey.DirectorID, pID)
		if err != nil {
			// unable to get port details
			continue
		}
		// for each IP address, save the target information
		for _, ip := range port.SymmetrixPort.IPAddresses {
			t := goiscsi.ISCSITarget{
				Portal:   ip,
				GroupTag: "0",
				Target:   port.SymmetrixPort.Identifier,
			}
			targets = append(targets, maskingViewTargetInfo{
				target:           t,
				IsCHAPConfigured: false,
			})
		}
	}
	symToMaskingViewTargets.Store(array, targets)
	return targets, nil
}

func (s *service) getNVMeTCPTargetsForMaskingView(ctx context.Context, array string, view *types.MaskingView, pmaxClient pmax.Pmax) ([]maskingViewNVMeTargetInfo, error) {
	if array == "" {
		return []maskingViewNVMeTargetInfo{}, fmt.Errorf("No array specified")
	}
	if view.PortGroupID == "" {
		return []maskingViewNVMeTargetInfo{}, fmt.Errorf("Masking view contains no PortGroupID")
	}
	configuredPortals, err := getIPInterfaces(ctx, array, s.opts.PortGroups, pmaxClient)
	if err != nil {
		return []maskingViewNVMeTargetInfo{}, err
	}
	viewPortals, err := getIPInterfaces(ctx, array, []string{view.PortGroupID}, pmaxClient)
	if err != nil {
		return []maskingViewNVMeTargetInfo{}, err
	}
	allowedPortals := make(map[string]int32)
	for portal, port := range viewPortals {
		if _, ok := configuredPortals[portal]; ok {
			allowedPortals[portal] = port
		}
	}
	if len(allowedPortals) == 0 {
		return []maskingViewNVMeTargetInfo{}, fmt.Errorf("masking view port group %s has no portals in the configured port groups", view.PortGroupID)
	}
	targets, err := s.getNVMeTCPTargetsFromPortals(ctx, array, allowedPortals, pmaxClient)
	if err != nil {
		return []maskingViewNVMeTargetInfo{}, err
	}

	maskingViewTargets := make([]maskingViewNVMeTargetInfo, 0, len(targets))
	for _, target := range targets {
		maskingViewTargets = append(maskingViewTargets, maskingViewNVMeTargetInfo{target: target})
	}
	symToMaskingViewTargets.Store(array, maskingViewTargets)
	return maskingViewTargets, nil
}

// hostAdoptionLatencyWarnThreshold is 67% of the 15 second per-node discovery and
// adoption budget from NFR-1; exceeding it is logged as a warning (NFR-4).
const hostAdoptionLatencyWarnThreshold = 10 * time.Second

// Host adoption Prometheus metrics. They are registered against the driver's own
// registry (DriverMetricsRegistry) rather than the default registerer, because that
// is the registry served on the driver's /metrics endpoint.
var (
	hostsAdoptedTotal = registerOrExisting(DriverMetricsRegistry(), prometheus.NewCounter(prometheus.CounterOpts{
		Name: "csi_powermax_hosts_adopted_total",
		Help: "Total number of hosts successfully adopted.",
	}))
	hostAdoptionErrorsTotal = registerOrExisting(DriverMetricsRegistry(), prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "csi_powermax_host_adoption_errors_total",
		Help: "Total number of host adoption errors, labelled by failure reason.",
	}, []string{"reason"}))
	bootLUNsDetectedTotal = registerOrExisting(DriverMetricsRegistry(), prometheus.NewCounter(prometheus.CounterOpts{
		Name: "csi_powermax_boot_luns_detected_total",
		Help: "Total number of non-CSI (boot LUN) volumes detected on adopted hosts.",
	}))
)

// bootLUNInfo stores information about detected boot LUNs on an adopted host.
type bootLUNInfo struct {
	StorageGroupID string // non-CSI storage group containing boot/pre-existing volumes
	NumVolumes     int    // number of volumes in the storage group
	MaskingViewID  string // masking view exposing the storage group
}

// getAdoptedHost returns the adopted host info for the given array.
func (s *service) getAdoptedHost(symID string) (adoptedHostInfo, bool) {
	s.adoptedHostsMutex.RLock()
	defer s.adoptedHostsMutex.RUnlock()
	info, ok := s.adoptedHosts[symID]
	return info, ok
}

// setAdoptedHost records the host adopted on the given array.
func (s *service) setAdoptedHost(symID string, info adoptedHostInfo) {
	s.adoptedHostsMutex.Lock()
	defer s.adoptedHostsMutex.Unlock()
	if s.adoptedHosts == nil {
		s.adoptedHosts = make(map[string]adoptedHostInfo)
	}
	s.adoptedHosts[symID] = info
}

// setAdoptedHostBootLUNs attaches detected boot LUN info to an already adopted host.
func (s *service) setAdoptedHostBootLUNs(symID string, bootLUNs []bootLUNInfo) {
	s.adoptedHostsMutex.Lock()
	defer s.adoptedHostsMutex.Unlock()
	info, ok := s.adoptedHosts[symID]
	if !ok {
		return
	}
	info.BootLUNs = bootLUNs
	s.adoptedHosts[symID] = info
}

// removeAdoptedHost removes the adopted host info for the given array.
func (s *service) removeAdoptedHost(symID string) {
	s.adoptedHostsMutex.Lock()
	defer s.adoptedHostsMutex.Unlock()
	delete(s.adoptedHosts, symID)
}

// adoptedHostsSnapshot returns a copy of the adopted host map, safe to range over.
func (s *service) adoptedHostsSnapshot() map[string]adoptedHostInfo {
	s.adoptedHostsMutex.RLock()
	defer s.adoptedHostsMutex.RUnlock()
	if len(s.adoptedHosts) == 0 {
		return nil
	}
	out := make(map[string]adoptedHostInfo, len(s.adoptedHosts))
	for symID, info := range s.adoptedHosts {
		out[symID] = info
	}
	return out
}

// getAdoptedHostID returns the adopted host ID for the given array, or empty string
// if no host was adopted for that array. Used by controller-side operations
// to resolve the correct host name for masking view creation.
func (s *service) getAdoptedHostID(symID string) string {
	info, ok := s.getAdoptedHost(symID)
	if !ok {
		return ""
	}
	return info.HostID
}

// isBootLUNStorageGroup checks whether the given storage group ID belongs to
// a detected boot LUN group on an adopted host for the given array.
// Returns true if the storage group is a non-CSI boot LUN group that must
// not be modified by CSI operations.
func (s *service) isBootLUNStorageGroup(symID string, storageGroupID string) bool {
	info, ok := s.getAdoptedHost(symID)
	if !ok {
		return false
	}
	for _, bl := range info.BootLUNs {
		if bl.StorageGroupID == storageGroupID {
			return true
		}
	}
	return false
}

// detectBootLUNs enumerates masking views on an adopted host and classifies
// storage groups by CSI prefix to detect boot/pre-existing LUNs.
// Returns a slice of bootLUNInfo for each non-CSI storage group found.
// An empty slice means no boot LUNs detected (all SGs are CSI-managed).
func (s *service) detectBootLUNs(ctx context.Context, symID string, hostID string, pmaxClient pmax.Pmax) ([]bootLUNInfo, error) {
	log := csmlog.WithContext(ctx)

	mvIDs, err := pmaxClient.GetHostMaskingViews(ctx, symID, hostID)
	if err != nil {
		return nil, fmt.Errorf("detectBootLUNs: failed to get masking views for host %s on array %s: %w",
			hostID, symID, err)
	}
	if len(mvIDs) == 0 {
		log.Infof("Boot LUN detection: host %s on array %s has no masking views", hostID, symID)
		return nil, nil
	}

	var bootLUNs []bootLUNInfo
	for _, mvID := range mvIDs {
		mv, err := pmaxClient.GetMaskingViewByID(ctx, symID, mvID)
		if err != nil {
			log.Warnf("Boot LUN detection: failed to get masking view %s on array %s: %s", mvID, symID, err.Error())
			continue
		}
		sgID := mv.StorageGroupID
		if sgID == "" {
			continue
		}
		// CSI-managed storage groups have the "csi-" prefix
		if strings.HasPrefix(sgID, CSIPrefix+"-") {
			log.Debugf("detectBootLUNs: storage group %s in masking view %s is CSI-managed, skipping", sgID, mvID)
			continue
		}
		// Non-CSI storage group — potential boot LUN group
		sg, err := pmaxClient.GetStorageGroup(ctx, symID, sgID)
		if err != nil {
			log.Warnf("Boot LUN detection: failed to get storage group %s on array %s: %s", sgID, symID, err.Error())
			continue
		}
		bootLUNs = append(bootLUNs, bootLUNInfo{
			StorageGroupID: sgID,
			NumVolumes:     sg.NumOfVolumes,
			MaskingViewID:  mvID,
		})
		log.Infof("Boot LUN detection: detected %d boot/pre-existing volume(s) in non-CSI storage group %s (masking view: %s) on array %s",
			sg.NumOfVolumes, sgID, mvID, symID)
	}

	return bootLUNs, nil
}

// addAdoptedHostTopology publishes the adopted host on each array into the
// NodeGetInfo topology segment (FR-2A.1), so ControllerPublishVolume can resolve the
// real host name instead of deriving a CSI-convention one. The "." separator matches
// the existing topology key convention (e.g. csi-powermax.dellemc.com/000197900111.fc).
//
// If any adopted host name is not a valid Kubernetes label value, this function returns
// an error. The controller requires the exact host name to look it up on the array, so
// we cannot sanitize or hash the name. Failing NodeGetInfo prevents the driver from
// starting with a misconfigured deployment.
func (s *service) addAdoptedHostTopology(ctx context.Context, topology map[string]string) error {
	driverName := s.getDriverName()
	for symID, info := range s.adoptedHostsSnapshot() {
		hostKey := driverName + "/" + symID + ".adoptedHost"
		protoKey := driverName + "/" + symID + ".adoptedTransportProtocol"
		if !isValidTopologyLabelValue(info.HostID) {
			return fmt.Errorf("adopted host name %q on array %s is not a valid Kubernetes label value; "+
				"must match regex [a-zA-Z0-9]([-_.a-zA-Z0-9]*[a-zA-Z0-9])? and be ≤63 characters. "+
				"Rename the host on the array to a valid name and restart the driver",
				info.HostID, symID)
		}
		topology[hostKey] = info.HostID
		topology[protoKey] = info.Protocol
		csmlog.WithContext(ctx).Infof("Host adoption topology: adding adopted host labels %s=%s, %s=%s",
			hostKey, info.HostID, protoKey, info.Protocol)
	}
	return nil
}

// resolveMaskingViewTarget determines what a newly created CSI masking view should
// reference for the given host, returning the target ID and whether it is a host
// (true) or a host group (false).
//
// A PowerMax masking view references exactly one host or one host group, and a host
// that belongs to a host group can only be masked through that group. Adopted hosts
// are frequently host group members (clustered deployments), so the CSI
// masking view has to target the group in that case. Pre-existing masking views are
// never modified — CSI always gets its own.
func (s *service) resolveMaskingViewTarget(ctx context.Context, symID string, host *types.Host, pmaxClient pmax.Pmax) (string, bool, error) {
	if host == nil {
		return "", false, fmt.Errorf("resolveMaskingViewTarget: nil host provided for array %s", symID)
	}
	if host.NumberHostGroups == 0 {
		return host.HostID, true, nil
	}

	hostGroupID, err := s.findHostGroupForHost(ctx, symID, host.HostID, pmaxClient)
	if err != nil {
		return "", false, err
	}
	if hostGroupID == "" {
		// The host reports group membership but no group lists it. Fall back to a
		// host-scoped masking view and let the array reject it if that is wrong.
		csmlog.WithContext(ctx).Warnf("Host %s on array %s reports %d host group(s) but none could be resolved; "+
			"creating a host-scoped masking view", host.HostID, symID, host.NumberHostGroups)
		return host.HostID, true, nil
	}
	csmlog.WithContext(ctx).Infof("Host %s on array %s is a member of host group %s; the CSI masking view will target the host group",
		host.HostID, symID, hostGroupID)
	return hostGroupID, false, nil
}

// findHostGroupForHost returns the ID of the host group containing hostID, or an
// empty string if none does.
func (s *service) findHostGroupForHost(ctx context.Context, symID, hostID string, pmaxClient pmax.Pmax) (string, error) {
	hostGroupList, err := pmaxClient.GetHostGroupList(ctx, symID)
	if err != nil {
		return "", fmt.Errorf("failed to list host groups on array %s while resolving the host group for host %s: %w", symID, hostID, err)
	}
	if hostGroupList == nil {
		return "", nil
	}
	for _, hostGroupID := range hostGroupList.HostGroupIDs {
		hostGroup, err := pmaxClient.GetHostGroupByID(ctx, symID, hostGroupID)
		if err != nil {
			csmlog.WithContext(ctx).Warnf("Failed to fetch host group %s on array %s: %s", hostGroupID, symID, err.Error())
			continue
		}
		for _, member := range hostGroup.Hosts {
			if strings.EqualFold(member.HostID, hostID) {
				return hostGroup.HostGroupID, nil
			}
		}
	}
	return "", nil
}

// validateAdoptedHostPortGroup checks that a CSI FC port group can be built for the
// adopted host: at least one of its initiators must be logged in on a SCSI_FC
// director port. Without one, masking a CSI volume to the host is impossible, so
// adoption is rejected with an actionable error rather than silently succeeding.
func (s *service) validateAdoptedHostPortGroup(ctx context.Context, symID string, host *types.Host, pmaxClient pmax.Pmax) error {
	if s.opts.IsVsphereEnabled {
		return nil
	}
	if host.HostType != "Fibre" {
		// Port group selection for non-Fibre hosts uses the configured port groups
		// rather than the host's own ports, so there is nothing to verify here.
		csmlog.WithContext(ctx).Warnf("Host adoption: host %s on array %s reports host type %q rather than \"Fibre\"; skipping FC port group validation",
			host.HostID, symID, host.HostType)
		return nil
	}
	ports, err := s.getUsableFCPortsForHost(ctx, symID, host, pmaxClient)
	if err != nil {
		return fmt.Errorf("could not determine usable FC ports for host %s on array %s: %w", host.HostID, symID, err)
	}
	if len(ports) == 0 {
		return fmt.Errorf("host %s on array %s has no initiator logged in on a SCSI_FC director port, "+
			"so no CSI port group can be built for it; the storage administrator must verify the host's "+
			"zoning and that its FC initiators are on fabric and logged in", host.HostID, symID)
	}
	csmlog.WithContext(ctx).Infof("Host adoption: host %s on array %s has %d usable SCSI_FC port(s): %v",
		host.HostID, symID, len(ports), ports)
	return nil
}

// hostConflictError is satisfied by the typed conflict error gopowermax returns from
// the host lookup API. It is declared here rather than importing the concrete type so
// the driver keeps building against gopowermax releases that predate it; the message
// check in isHostConflictError covers that interim.
type hostConflictError interface {
	IsHostConflict() bool
}

// isHostConflictError reports whether err means the node's WWPNs resolve to more than
// one host on the array. That is a permanent condition — an administrator has to
// consolidate the WWPNs — so callers must reject rather than retry.
func isHostConflictError(err error) bool {
	var conflict hostConflictError
	if errors.As(err, &conflict) {
		return conflict.IsHostConflict()
	}
	return strings.Contains(err.Error(), "GetHostByInitiators: conflict")
}

// discoverAndAdoptHost attempts to discover and adopt a pre-existing host object
// on the given PowerMax array by matching the node's FC WWPN initiators.
//
// Return contract:
//   - (hostName, nil): Host discovered and adopted — hostName is the adopted host's original name
//   - ("", nil): No host found for any WWPN — safe to fall through to standard host creation
//   - ("", error): API error or validation failure — do NOT fall through to create
//
// On success, the adopted host name and "FC" protocol are cached in s.adoptedHosts.
func (s *service) discoverAndAdoptHost(ctx context.Context, symID string, wwpns []string, pmaxClient pmax.Pmax) (string, error) {
	log := csmlog.WithContext(ctx)
	log.Infof("Host adoption: discovering host on array %s for node %s with %d FC WWPNs: %v",
		symID, s.k8sNodeName(), len(wwpns), wwpns)

	// NFR-1 budgets host discovery and adoption at 15 seconds per node; NFR-4 asks
	// for a warning once 67% of that budget is consumed.
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		if elapsed > hostAdoptionLatencyWarnThreshold {
			log.Warnf("Host adoption: discovery on array %s took %s, exceeding the %s warning threshold",
				symID, elapsed.Round(time.Millisecond), hostAdoptionLatencyWarnThreshold)
			return
		}
		log.Infof("Host adoption: discovery on array %s completed in %s", symID, elapsed.Round(time.Millisecond))
	}()

	if len(wwpns) == 0 {
		hostAdoptionErrorsTotal.WithLabelValues("no_wwpns").Inc()
		return "", fmt.Errorf("discoverAndAdoptHost: no FC WWPNs provided for array %s", symID)
	}

	// Strip "0x" prefix from WWPNs if present (OS-level format → bare WWPN)
	bareWWPNs := make([]string, len(wwpns))
	for i, wwpn := range wwpns {
		bareWWPNs[i] = strings.TrimPrefix(wwpn, "0x")
	}

	// NFR-1 budgets host discovery and adoption at 15 seconds per node.
	// Enforce this via a scoped context timeout to prevent long retry loops
	// when the Unisphere API is degraded (Issue 1).
	adoptCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	// Retry discovery up to pmaxQueryAttempts on API errors. A host conflict is a
	// permanent, logical condition, so it is rejected on the first attempt rather
	// than consuming the full retry budget.
	var host *types.Host
	var err error
	for attempt := 1; attempt <= pmaxQueryAttempts; attempt++ {
		if attempt > 1 {
			select {
			case <-adoptCtx.Done():
				return "", fmt.Errorf("discoverAndAdoptHost: adoption budget exceeded or context cancelled for array %s: %w", symID, adoptCtx.Err())
			case <-time.After(5 * time.Second):
			}
			log.Infof("Host adoption: retry attempt %d/%d for array %s", attempt, pmaxQueryAttempts, symID)
		}

		host, err = pmaxClient.GetHostByInitiators(adoptCtx, symID, bareWWPNs)
		if err == nil {
			break
		}
		if isHostConflictError(err) {
			hostAdoptionErrorsTotal.WithLabelValues("host_conflict").Inc()
			log.Errorf("Host adoption: WWPN conflict on array %s for node %s: %s", symID, s.k8sNodeName(), err.Error())
			return "", fmt.Errorf("discoverAndAdoptHost: host conflict on array %s; "+
				"the storage administrator must consolidate the node's WWPNs onto a single host object: %w", symID, err)
		}
		log.Warnf("Host adoption: failed to query host by WWPNs on array %s (attempt %d/%d): %s",
			symID, attempt, pmaxQueryAttempts, err.Error())
	}
	if err != nil {
		// API error after all retries — do NOT fall through to create (RACE-3)
		hostAdoptionErrorsTotal.WithLabelValues("api_error").Inc()
		return "", fmt.Errorf("discoverAndAdoptHost: failed to discover host on array %s for node %s after %d attempts: %w",
			symID, s.k8sNodeName(), pmaxQueryAttempts, err)
	}

	// (nil, nil) — confirmed no host, safe to fall through
	if host == nil {
		log.Infof("Host adoption: no existing host found for WWPNs on array %s, falling through to standard host creation", symID)
		return "", nil
	}

	// Skip CSI-convention hosts (names starting with "csi-") - these were created
	// by the driver in "create" mode and should be handled by the standard flow
	if strings.HasPrefix(host.HostID, "csi-") {
		log.Infof("Host adoption: found CSI-convention host %s on array %s, ignoring and falling through to standard host creation", host.HostID, symID)
		// Remove from cache if present (stale data from previous adoption)
		s.removeAdoptedHost(symID)
		return "", nil
	}

	log.Infof("Host adoption: found host %s on array %s, validating WWPN coverage", host.HostID, symID)

	// Host found — validate coverage-based WWPN match
	if err := validateFCHostForAdoption(adoptCtx, host, bareWWPNs, s.opts.HostAdoptionMinOverlapRatio); err != nil {
		hostAdoptionErrorsTotal.WithLabelValues("validation_failed").Inc()
		log.Errorf("Host adoption: validation failed for host %s: %s", host.HostID, err.Error())
		return "", fmt.Errorf("discoverAndAdoptHost: host %s on array %s failed validation: %w",
			host.HostID, symID, err)
	}

	// FR-1.3: an adopted host is only usable if CSI can build a port group for it.
	// Checking now surfaces a zoning or director problem at node startup with an
	// actionable message, instead of at the first ControllerPublishVolume.
	if err := s.validateAdoptedHostPortGroup(adoptCtx, symID, host, pmaxClient); err != nil {
		hostAdoptionErrorsTotal.WithLabelValues("port_group_incompatible").Inc()
		log.Errorf("Host adoption: port group validation failed for host %s: %s", host.HostID, err.Error())
		return "", fmt.Errorf("discoverAndAdoptHost: host %s on array %s failed port group validation: %w",
			host.HostID, symID, err)
	}

	log.Infof("Host adoption: validation passed for host %s, proceeding with adoption", host.HostID)
	log.Infof("Host adoption: successfully adopted host %s on array %s (initiators: %v)",
		host.HostID, symID, host.Initiators)

	// Emit Kubernetes event for observability
	if recorder := initEventRecorder(); recorder != nil {
		nodeRef := &corev1.ObjectReference{
			Kind:      "Node",
			Name:      s.k8sNodeName(),
			Namespace: "",
		}
		recorder.Eventf(nodeRef, corev1.EventTypeNormal, "HostAdopted",
			"Adopted pre-existing host %s on array %s with %d initiator(s)",
			host.HostID, symID, len(host.Initiators))
	}

	// Increment Prometheus metric
	hostsAdoptedTotal.Inc()

	// Cache the adopted host info
	s.setAdoptedHost(symID, adoptedHostInfo{
		HostID:   host.HostID,
		Protocol: FcTransportProtocol,
	})

	return host.HostID, nil
}

func (s *service) createOrUpdateFCHost(ctx context.Context, array string, nodeName string, portWWNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	csmlog.WithContext(ctx).Info(fmt.Sprintf("Processing FC Host array: %s, nodeName: %s, initiators: %v", array, nodeName, portWWNs))
	if array == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(portWWNs) == 0 {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No port WWNs specified")
	}

	// Get the set of initiators to use
	hostInitiators := make([]string, 0)
	initList, err := pmaxClient.GetInitiatorList(ctx, array, "", false, false)
	if err != nil {
		csmlog.WithContext(ctx).Error("Could not get initiator list: " + err.Error())
		return nil, err
	}
	stringSliceRegexReplace(portWWNs, "^0x", "")
	for _, portWWN := range portWWNs {
		for _, initiator := range initList.InitiatorIDs {
			if strings.HasSuffix(initiator, portWWN) {
				hostInitiators = appendIfMissing(hostInitiators, portWWN)
			}
		}
	}
	csmlog.WithContext(ctx).Infof("hostInitiators: %s", hostInitiators)

	// See if the host is present
	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	if err != nil {
		// host does not exist, create it
		csmlog.WithContext(ctx).Infof("Array %s FC Host %s does not exist. Creating it.", array, nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, hostInitiators, nil, pmaxClient)
		if err != nil {
			return nil, err
		}
	} else {
		// make sure we don't update an iscsi host
		arrayInitiators := stringSliceRegexMatcher(host.Initiators, "(0x)*[0-9a-fA-F]")
		stringSliceRegexReplace(arrayInitiators, "^.*:.*:", "")
		// host does exist, update it if necessary
		if len(arrayInitiators) != 0 && !stringSlicesEqual(arrayInitiators, hostInitiators) {
			csmlog.WithContext(ctx).Infof("updating host: %s initiators to: %s", nodeName, hostInitiators)
			_, err := s.retryableUpdateHostInitiators(ctx, array, host, portWWNs, pmaxClient)
			if err != nil {
				return nil, err
			}
		}
	}
	return host, nil
}

func (s *service) createOrUpdateIscsiHost(ctx context.Context, array string, nodeName string, IQNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	csmlog.WithContext(ctx).Debug(fmt.Sprintf("Processing Iscsi Host array: %s, nodeName: %s, initiators: %v", array, nodeName, IQNs))
	if array == "" {
		return nil, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return nil, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(IQNs) == 0 {
		return nil, fmt.Errorf("createOrUpdateHost: No IQNs specified")
	}

	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	if err != nil {
		// host does not exist, create it
		csmlog.WithContext(ctx).Infof("ISCSI host %s does not exist, creating it", nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, IQNs, nil, pmaxClient)
		if err != nil {
			return nil, fmt.Errorf("Unable to create Host: %v", err)
		}
	} else {
		// Make sure we don't update an FC host
		hostInitiators := stringSliceRegexMatcher(host.Initiators, "^iqn\\.")
		// host does exist, update it if necessary
		if len(hostInitiators) != 0 && !stringSlicesEqual(hostInitiators, IQNs) {
			csmlog.WithContext(ctx).Infof("updating host: %s initiators to: %s", nodeName, IQNs)
			if _, err := s.retryableUpdateHostInitiators(ctx, array, host, IQNs, pmaxClient); err != nil {
				return nil, err
			}
		}
	}
	return host, nil
}

func (s *service) createOrUpdateNVMeTCPHost(ctx context.Context, array string, nodeName string, NQNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	csmlog.WithContext(ctx).Debug(fmt.Sprintf("Processing NVMeTCP Host array: %s, nodeName: %s, initiators: %v", array, nodeName, NQNs))

	if array == "" {
		return nil, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return nil, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(NQNs) == 0 {
		return nil, fmt.Errorf("createOrUpdateHost: No NQNs specified")
	}

	// process the NQNs
	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	if err != nil {
		// host does not exist, create it
		csmlog.WithContext(ctx).Infof("NVMe host %s does not exist, creating it", nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, NQNs, nil, pmaxClient)
		if err != nil {
			return nil, fmt.Errorf("unable to create host: %v", err)
		}
	} else {
		// Make sure we fetch only the NVMe hosts
		hostInitiators := stringSliceRegexMatcher(host.Initiators, "^nqn\\.")
		// host does exist, update it if necessary
		if len(hostInitiators) != 0 && !stringSlicesEqual(hostInitiators, NQNs) {
			csmlog.WithContext(ctx).Infof("updating host: %s initiators to: %s", nodeName, NQNs)
			if _, err := s.retryableUpdateHostInitiators(ctx, array, host, NQNs, pmaxClient); err != nil {
				return nil, err
			}
		}
	}
	return host, nil
}

func makeNVMeInitiatorIDs(NQNs []string, nvmeHostID string) ([]string, error) {
	// Normalize the local NVMe host ID to match the format used by the array for initiator hostID.
	// Example: c32abcdf-35f9-4800-88ad-396225c90b70 -> C32ABCDF35F9480088AD396225C90B70
	nvmeHostID = strings.ReplaceAll(nvmeHostID, "-", "")
	nvmeHostID = strings.ToUpper(nvmeHostID)

	initiatorIDs := make([]string, len(NQNs))

	// NVMe initiator ID format used in PowerMax API Host object has to include the NVMe host identity
	for i, nqn := range NQNs {
		if !strings.HasSuffix(nqn, ":"+nvmeHostID) {
			initiatorIDs[i] = nqn + ":" + nvmeHostID
		} else {
			initiatorIDs[i] = nqn
		}
	}

	return initiatorIDs, nil
}

// retryableCreateHost
func (s *service) retryableCreateHost(ctx context.Context, array string, nodeName string, hostInitiators []string, _ *types.HostFlags, pmaxClient pmax.Pmax) (*types.Host, error) {
	var err error
	var host *types.Host

	// Retry up to pmaxQueryAttempts times to create host on array
	for attempt := 1; attempt <= pmaxQueryAttempts; attempt++ {
		if attempt > 1 { // First attempt does not need to wait
			// Sleep 5 seconds or until context is closed
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("failed to create host on array: context timeout")
			case <-time.After(5 * time.Second):
			}
		}
		csmlog.WithContext(ctx).Infof("Attempting to create host %s with initiators %v on array %s (%d)",
			nodeName, hostInitiators, array, attempt)
		host, err = pmaxClient.CreateHost(ctx, array, nodeName, hostInitiators, nil)
		if err == nil {
			return host, nil
		}
		csmlog.WithContext(ctx).Errorf("Failed to create host on array: %v", err)
	}
	return nil, fmt.Errorf("failed to create host on array after %d attempts", pmaxQueryAttempts)
}

// retryableUpdateHostInitiators wraps UpdateHostInitiators in a retry loop
func (s *service) retryableUpdateHostInitiators(ctx context.Context, array string, host *types.Host, initiators []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	var err error
	var updatedHost *types.Host

	// Retry pmaxQueryAttempts times to update host on array
	for attempt := 1; attempt <= pmaxQueryAttempts; attempt++ {
		if attempt > 1 { // First attempt does not need to wait
			// Sleep 5 seconds or until context is closed
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("failed to update host initiators on array: context timeout")
			case <-time.After(5 * time.Second):
			}
		}
		csmlog.WithContext(ctx).Infof("Attempting to update host %s with initiators %v on array %s (%d)",
			host.HostID, initiators, array, attempt)
		updatedHost, err = pmaxClient.UpdateHostInitiators(ctx, array, host, initiators)
		if err == nil {
			return updatedHost, nil
		}
		csmlog.WithContext(ctx).Errorf("Failed to update host initiators on array: %v", err)
	}
	return nil, fmt.Errorf("failed to update host initiators on array after %d attempts", pmaxQueryAttempts)
}

// retryableGetSymmetrixIDList returns the list of arrays
func (s *service) retryableGetSymmetrixIDList() *types.SymmetrixIDList {
	return &types.SymmetrixIDList{
		SymmetrixIDs: s.opts.ManagedArrays,
	}
}

// NodeExpandVolume helps extending a volume size on a node
func (s *service) NodeExpandVolume(
	ctx context.Context,
	req *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse, error,
) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "NodeExpandVolume",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("NodeExpandVolume called")

	var reqID string
	var err error
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// We are getting target path that points to mounted path on "/"
	// This doesn't help us, though we should trace the path received
	volumePath := req.GetVolumePath()
	if volumePath == "" {
		csmlog.WithContext(ctx).Error("Volume path required")
		return nil, status.Error(codes.InvalidArgument,
			"Volume path required")
	}

	id := req.GetVolumeId()
	_, symID, _, _, _, err := s.parseCsiID(id)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return nil, status.Error(codes.NotFound, err.Error())
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		csmlog.WithContext(ctx).Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}
	// Check if it s a file System
	if accTypeIsNFS([]*csi.VolumeCapability{req.GetVolumeCapability()}) {
		csmlog.WithContext(ctx).Debug("file system is expanded, nothing to do on node...")
		return &csi.NodeExpandVolumeResponse{}, nil
	}

	// Parse the CSI VolumeId and validate against the volume
	_, _, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		// If the volume isn't found, we cannot stage it
		return nil, err
	}
	volumeWWN := vol.EffectiveWWN

	// Get the pmax volume name so that it can be searched in the system
	// to find mount information
	// Examples of possible volumeIdentifiers: tn1-csi-ABC-csm-3f7da6bf8d-test, csi-ABC-csm-3f7da6bf8d-test
	parts := strings.Split(vol.VolumeIdentifier, "-")

	var volName string
	clusterPrefix := s.getClusterPrefix()
	// remove the tenant volume prefix (csm-authorization) and namespace from the volName as the mount paths will not have it
	for i, p := range parts {
		if p == CSIPrefix {
			if i+3 < len(parts) {
				if parts[i+1] == clusterPrefix {
					volName = parts[i+2] + "-" + parts[i+3]
					csmlog.WithContext(ctx).Infof("Found volume name: %s", volName)
				}
			} else {
				csmlog.WithContext(ctx).Errorf("expected volume identifier format *-%s-%s-[volumeName]-*, got malformed identifier: %s", CSIPrefix, clusterPrefix, vol.VolumeIdentifier)
				return nil, status.Error(codes.Internal, "Invalid volume identifer: "+vol.VolumeIdentifier)
			}
		}
	}

	// Locate and fetch all (multipath/regular) mounted paths using this volume
	devMnt, err := gofsutil.GetMountInfoFromDevice(ctx, volName)
	if err != nil {
		var devName string
		// No mounts were found. Perhaps it is a raw block device, which would not be mounted.
		deviceNames, _ := gofsutil.GetSysBlockDevicesForVolumeWWN(context.Background(), volumeWWN)
		if len(deviceNames) > 0 {
			for _, deviceName := range deviceNames {
				if strings.HasPrefix(deviceName, "nvme") {
					nvmeControllerDevice, err := gofsutil.GetNVMeController(deviceName)
					if err != nil {
						csmlog.WithContext(ctx).Errorf("Failed to rescan device (%s) with error (%s)", deviceName, err.Error())
						return nil, status.Error(codes.Internal, err.Error())
					}
					if nvmeControllerDevice != "" {
						devicePath := dev + nvmeControllerDevice
						csmlog.WithContext(ctx).Infof("Rescanning unmounted (raw block) device %s to expand size", devicePath)
						err = s.nvmetcpClient.DeviceRescan(devicePath)
						if err != nil {
							csmlog.WithContext(ctx).Errorf("Failed to rescan device (%s) with error (%s)", devicePath, err.Error())
							return nil, status.Error(codes.Internal, err.Error())
						}
					}
				} else {
					devicePath := sysBlock + "/" + deviceName
					csmlog.WithContext(ctx).Infof("Rescanning unmounted (raw block) device %s to expand size", deviceName)
					err = gofsutil.DeviceRescan(context.Background(), devicePath)
					if err != nil {
						csmlog.WithContext(ctx).Errorf("Failed to rescan device (%s) with error (%s)", devicePath, err.Error())
						return nil, status.Error(codes.Internal, err.Error())
					}
				}
				devName = deviceName
			}
			mpathDev, err := gofsutil.GetMpathNameFromDevice(ctx, devName)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to fetch mpath name for device (%s) with error (%s)", devName, err.Error())
				return nil, status.Error(codes.Internal, err.Error())
			}
			if mpathDev != "" {
				err = gofsutil.ResizeMultipath(context.Background(), mpathDev)
				if err != nil {
					csmlog.WithContext(ctx).Errorf("Failed to resize filesystem: device  (%s) with error (%s)", mpathDev, err.Error())
					return nil, status.Error(codes.Internal, err.Error())
				}
			}
			return &csi.NodeExpandVolumeResponse{}, nil
		}
		csmlog.WithContext(ctx).Errorf("Failed to find mount info for (%s) with error (%s)", volName, err.Error())
		return nil, status.Error(codes.Internal,
			fmt.Sprintf("Failed to find mount info for (%s) with error (%s)", volName, err.Error()))
	}
	csmlog.WithContext(ctx).Infof("Mount info for volume %s: %+v", volName, devMnt)

	size := req.GetCapacityRange().GetRequiredBytes()

	f := csmlog.Fields{
		"CSIRequestID": reqID,
		"VolumeName":   volName,
		"VolumePath":   volumePath,
		"Size":         size,
		"VolumeWWN":    volumeWWN,
	}
	csmlog.WithFields(f).Info("Calling resize the file system")

	// Rescan the scsi devices for the volume expanded on the array
	if !s.useNVMeTCP {
		for _, device := range devMnt.DeviceNames {
			devicePath := sysBlock + "/" + device
			err = gofsutil.DeviceRescan(context.Background(), devicePath)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to rescan device (%s) with error (%s)", devicePath, err.Error())
				return nil, status.Error(codes.Internal, err.Error())
			}
		}
	}

	// Expand the filesystem with the actual expanded volume size.
	if devMnt.MPathName != "" {
		err = gofsutil.ResizeMultipath(context.Background(), devMnt.MPathName)
		if err != nil {
			csmlog.WithContext(ctx).Errorf("Failed to resize filesystem: device  (%s) with error (%s)", devMnt.MountPoint, err.Error())
			return nil, status.Error(codes.Internal, err.Error())
		}
	}
	// For a regular device, get the device path (devMnt.DeviceNames[1]) where the filesystem is mounted
	// PublishVolume creates devMnt.DeviceNames[0] but is left unused for regular devices
	var devicePath string
	if len(devMnt.DeviceNames) > 1 {
		devicePath = "/dev/" + devMnt.DeviceNames[1]
	} else {
		devicePath = "/dev/" + devMnt.DeviceNames[0]
	}

	// Determine file system type
	fsType, err := gofsutil.FindFSType(context.Background(), devMnt.MountPoint)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to fetch filesystem for volume  (%s) with error (%s)", devMnt.MountPoint, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	csmlog.WithContext(ctx).Infof("Found %s filesystem mounted on volume %s", fsType, devMnt.MountPoint)

	// Resize the filesystem
	err = gofsutil.ResizeFS(context.Background(), devMnt.MountPoint, devicePath, devMnt.PPathName, devMnt.MPathName, fsType)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to resize filesystem: mountpoint (%s) device (%s) with error (%s)",
			devMnt.MountPoint, devicePath, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.NodeExpandVolumeResponse{}, nil
}

// Gets the iscsi target iqn values that can be used for rescanning.
func (s *service) getISCSITargets(ctx context.Context, symID string, pmaxClient pmax.Pmax) ([]ISCSITargetInfo, error) {
	var targets []ISCSITargetInfo
	var ips interface{}
	var ok bool

	ips, ok = symToAllISCSITargets.Load(symID)
	if ok {
		targets = ips.([]ISCSITargetInfo)
		csmlog.WithContext(ctx).Infof("Found ISCSI targets %v in cache", targets)
	} else {
		pmaxTargets, err := pmaxClient.GetISCSITargets(ctx, symID)
		if err != nil {
			return targets, status.Error(codes.Internal, fmt.Sprintf("Could not get iscsi target information: %s", err.Error()))
		}
		for _, pmaxTarget := range pmaxTargets {
			for _, ipaddr := range pmaxTarget.PortalIPs {
				target := ISCSITargetInfo{
					Target: pmaxTarget.IQN,
					Portal: ipaddr,
				}
				targets = append(targets, target)
			}
		}
		symToAllISCSITargets.Store(symID, targets)
		csmlog.WithContext(ctx).Infof("Updated targets %v in cache", targets)
	}

	return targets, nil
}

// Gets the iscsi target iqn values that can be used for rescanning.
func (s *service) getNVMeTCPTargets(ctx context.Context, symID string, pmaxClient pmax.Pmax) ([]NVMeTCPTargetInfo, error) {
	var targets []NVMeTCPTargetInfo
	var ips interface{}
	var ok bool

	ips, ok = symToAllNVMeTCPTargets.Load(symID)
	if ok {
		targets = ips.([]NVMeTCPTargetInfo)
		csmlog.WithContext(ctx).Infof("Found NVMeTCP targets %v in cache", targets)
	} else {
		// TODO NVME
		pmaxTargets, err := pmaxClient.GetNVMeTCPTargets(ctx, symID)
		if err != nil {
			return targets, status.Error(codes.Internal, fmt.Sprintf("Could not get invme target information: %s", err.Error()))
		}
		for _, pmaxTarget := range pmaxTargets {
			for _, ipaddr := range pmaxTarget.PortalIPs {
				target := NVMeTCPTargetInfo{
					Target: pmaxTarget.NQN,
					Portal: ipaddr,
				}
				targets = append(targets, target)
			}
		}
		symToAllNVMeTCPTargets.Store(symID, targets)
		csmlog.WithContext(ctx).Infof("Updated targets %v in cache", targets)
	}

	return targets, nil
}

// Returns the array targets and a boolean that is true if Fibrechannel.
// If the target is ISCSI, it also updates ISCSI node database if CHAP
// authentication was requested
func (s *service) getArrayTargets(ctx context.Context, targetIdentifiers string, symID string, pmaxClient pmax.Pmax) ([]ISCSITargetInfo, []FCTargetInfo, []NVMeTCPTargetInfo, bool, bool) {
	iscsiTargets := make([]ISCSITargetInfo, 0)
	fcTargets := make([]FCTargetInfo, 0)
	nvmeTargets := make([]NVMeTCPTargetInfo, 0)

	isFC := false
	isNVMeTCP := false
	arrayTargets := strings.Split(targetIdentifiers, ",")
	csmlog.WithContext(ctx).Infof("getAndConfigureTargets : targetIdentifiers  %+v\n", targetIdentifiers)
	// Remove the last empty element from the slice as there is a trailing ","
	if len(arrayTargets) == 1 && (arrayTargets[0] == targetIdentifiers) {
		csmlog.WithContext(ctx).Error("Failed to parse the target identifier string: " + targetIdentifiers)
	} else if len(arrayTargets) > 1 {
		arrayTargets = arrayTargets[:len(arrayTargets)-1]
		for i := 0; i < len(arrayTargets); i++ {
			if strings.HasPrefix(arrayTargets[i], "0x") { // fc
				tgt := strings.Replace(arrayTargets[i], "0x", "", 1)
				isFC = true
				fcTarget := FCTargetInfo{
					WWPN: tgt,
				}
				fcTargets = append(fcTargets, fcTarget)
			}
			if strings.HasPrefix(arrayTargets[i], "nqn") {
				isNVMeTCP = true
			}
		}

		if isNVMeTCP {
			nvmeTargets = s.getAndConfigureArrayNVMeTCPTargets(ctx, arrayTargets, symID, pmaxClient)
		} else if !isFC {
			iscsiTargets = s.getAndConfigureArrayISCSITargets(ctx, arrayTargets, symID, pmaxClient)
		}
	}
	return iscsiTargets, fcTargets, nvmeTargets, isFC, isNVMeTCP
}

func (s *service) getAndConfigureArrayNVMeTCPTargets(ctx context.Context, arrayTargets []string, symID string, pmaxClient pmax.Pmax) []NVMeTCPTargetInfo {
	csmlog.WithContext(ctx).Debugf("Entering getAndConfigureArrayNVMeTCPTargets for symID: %s, arrayTargets: %+v", symID, arrayTargets)

	nvmetcpTargets := make([]NVMeTCPTargetInfo, 0)
	seenTargets := make(map[string]struct{})
	appendTarget := func(target, portal string) {
		key := target + "\x00" + portal
		if _, ok := seenTargets[key]; ok {
			return
		}
		seenTargets[key] = struct{}{}
		nvmetcpTargets = append(nvmetcpTargets, NVMeTCPTargetInfo{Target: target, Portal: portal})
	}
	var allTargets []NVMeTCPTargetInfo
	allTargetsLoaded := false
	loadScopedTargets := func() {
		if allTargetsLoaded {
			return
		}
		allTargetsLoaded = true
		targets, err := getNVMeTCPTargetsFromPortGroups(s, ctx, symID, s.opts.PortGroups, pmaxClient)
		if err != nil {
			csmlog.WithContext(ctx).Errorf("Failed to get scoped NVMeTCP targets for array(%s): %s", symID, err.Error())
			return
		}
		for _, target := range targets {
			allTargets = append(allTargets, NVMeTCPTargetInfo{
				Target: target.TargetNqn,
				Portal: target.Portal,
			})
		}
		csmlog.WithContext(ctx).Debugf("scoped allTargets  %+v", allTargets)
	}

	targetsFromCache, ok := symToMaskingViewTargets.Load(symID)
	if ok {
		csmlog.WithContext(ctx).Debugf("targetsFromCache found for symID: %s", symID)
		switch targetsFromCache.(type) {
		case []maskingViewNVMeTargetInfo:
			cachedTargets := targetsFromCache.([]maskingViewNVMeTargetInfo)
			csmlog.WithContext(ctx).Debugf("cachedTargets  %+v", cachedTargets)
			// Check if the array targets are all present in the cache
			for _, arrayTarget := range arrayTargets {
				found := false
				for _, cachedTgt := range cachedTargets {
					if nvmeTargetMatchesPublishIdentifier(arrayTarget, cachedTgt.target.TargetNqn) {
						appendTarget(cachedTgt.target.TargetNqn, cachedTgt.target.Portal)
						found = true
					}
				}

				if !found {
					csmlog.WithContext(ctx).Debugf("Target %s not found in cache for symID: %s", arrayTarget, symID)
					// Some array targets are not present in cache
					// This mostly means that the Port group was modified post
					// driver boot. Invalidate the cache
					symToMaskingViewTargets.Delete(symID)
					csmlog.WithContext(ctx).Debugf("Invalidated cache for symID: %s", symID)
					// Look in the configured port groups for the target
					loadScopedTargets()
					isFound := false
					for _, tgt := range allTargets {
						if nvmeTargetMatchesPublishIdentifier(arrayTarget, tgt.Target) {
							appendTarget(tgt.Target, tgt.Portal)
							isFound = true
						}
					}
					if !isFound {
						csmlog.WithContext(ctx).Debugf("Target %s not found in allTargets for symID: %s", arrayTarget, symID)
						// This will be an extremely rare case
						// A new ISCSI target/portal IP has been configured
						// on the array and added to the Port Group
						// after the node driver cache information
						// Invalidate the cache. Return whatever targets we have found until now
						symToAllNVMeTCPTargets.Delete(symID)
						csmlog.WithContext(ctx).Debugf("Invalidated allTargets cache for symID: %s", symID)
						break
					}
				}
			}
			csmlog.WithContext(ctx).Infof("returned cached information: %+v", nvmetcpTargets)
			return nvmetcpTargets
		default:
			csmlog.WithContext(ctx).Infof("Invalidate cache as it not the right type.")
			symToAllNVMeTCPTargets.Delete(symID)
			csmlog.WithContext(ctx).Debugf("Invalidated allTargets cache for symID: %s", symID)
			csmlog.WithContext(ctx).Infof("Failed to find NVMe targets in cache.")
			// symToMaskingViewTargets.Delete(symID)
			// do nothing, will fall through and rebuild the cache
		}
	}
	csmlog.WithContext(ctx).Infof("There is no cached info, build it")
	// There is no cached information
	_, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.csiNodeID())

	csmlog.WithContext(ctx).Debugf("mvName: %s", mvName)
	// Get the Masking View Targets and configure CHAP if required
	// This call updates the cache as well
	goNVMETCPTargets, err := s.getAndConfigureMaskingViewTargetsNVMeTCP(ctx, symID, mvName, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to get and configure masking view targets. Error: %s", err.Error())
	}
	csmlog.WithContext(ctx).Infof("array targets = %+v\n", arrayTargets)
	csmlog.WithContext(ctx).Infof("nvme targets from getAndConfigureMaskingViewTargets %+v\n", goNVMETCPTargets)
	for _, arrayTarget := range arrayTargets {
		found := false
		for _, gonvmetcpTarget := range goNVMETCPTargets {
			if nvmeTargetMatchesPublishIdentifier(arrayTarget, gonvmetcpTarget.TargetNqn) {
				appendTarget(gonvmetcpTarget.TargetNqn, gonvmetcpTarget.Portal)
				found = true
			}
		}
		if !found {
			csmlog.WithContext(ctx).Errorf("Internal Error - Target: %s not found on array: %s", arrayTarget, symID)
		}
	}
	csmlog.WithContext(ctx).Infof("New cache information = %+v\n", nvmetcpTargets)
	return nvmetcpTargets
}

func (s *service) getAndConfigureArrayISCSITargets(ctx context.Context, arrayTargets []string, symID string, pmaxClient pmax.Pmax) []ISCSITargetInfo {
	iscsiTargets := make([]ISCSITargetInfo, 0)
	allTargets, _ := s.getISCSITargets(ctx, symID, pmaxClient)
	IQNs, err := s.iscsiClient.GetInitiators("")
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to fetch initiators for the host. Error: %s", err.Error())
		return iscsiTargets
	}
	cachedTargets, ok := symToMaskingViewTargets.Load(symID)
	if ok {
		found := false
		switch cachedTargets.(type) {
		case []maskingViewTargetInfo:
			targets := cachedTargets.([]maskingViewTargetInfo)
			// Enable CHAP if required
			err = s.setCHAPCredentials(symID, targets, IQNs)
			if err != nil {
				// Log the error and continue
				csmlog.WithContext(ctx).Errorf("Failed to set CHAP credentials for targets: %v. Error: %s", targets, err.Error())
			} else {
				// Update the cache if required
				cacheUpdated := false
				for i := range targets {
					if !targets[i].IsCHAPConfigured {
						targets[i].IsCHAPConfigured = true
						cacheUpdated = true
					}
				}
				if cacheUpdated {
					symToMaskingViewTargets.Store(symID, targets)
				}
			}
			// Check if the array targets are all present in the cache
			for _, arrayTarget := range arrayTargets {
				for _, tgt := range targets {
					if arrayTarget == tgt.target.Target {
						iscsiTarget := ISCSITargetInfo{
							Target: arrayTarget,
							Portal: tgt.target.Portal,
						}
						iscsiTargets = append(iscsiTargets, iscsiTarget)
						found = true
					}
				}
				if !found {
					// Some array targets are not present in cache
					// This mostly means that the Port group was modified post
					// driver boot. Invalidate the cache
					symToMaskingViewTargets.Delete(symID)
					// Look in the cache for all targets on the array
					isFound := false
					for _, tgt := range allTargets {
						if arrayTarget == tgt.Target {
							iscsiTarget := ISCSITargetInfo{
								Target: arrayTarget,
								Portal: tgt.Portal,
							}
							iscsiTargets = append(iscsiTargets, iscsiTarget)
							isFound = true
						}
					}
					if !isFound {
						// This will be an extremely rare case
						// A new ISCSI target/portal IP has been configured
						// on the array and added to the Port Group
						// after the node driver cache information
						// Invalidate the cache. Return whatever targets we have found until now
						symToAllISCSITargets.Delete(symID)
						break
					}
				}
			}
			csmlog.WithContext(ctx).Infof("Found cached targets: %v", targets)
			return iscsiTargets
		default:
			// cache is wrong type, invalidate it and rebuild
			symToAllISCSITargets.Delete(symID)
		}
	}
	// There is no cached information
	_, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.csiNodeID())

	csmlog.WithContext(ctx).Debugf("mvName: %s", mvName)

	// Get the Masking View Targets and configure CHAP if required
	// This call updates the cache as well
	goISCSITargets, err := s.getAndConfigureMaskingViewTargets(ctx, symID, mvName, IQNs, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to get and configure masking view targets. Error: %s", err.Error())
	}
	for _, arrayTarget := range arrayTargets {
		found := false
		for _, goiscsiTarget := range goISCSITargets {
			if arrayTarget == goiscsiTarget.Target {
				iscsiTarget := ISCSITargetInfo{
					Target: arrayTarget,
					Portal: goiscsiTarget.Portal,
				}
				iscsiTargets = append(iscsiTargets, iscsiTarget)
				found = true
			}
		}
		if !found {
			csmlog.WithContext(ctx).Errorf("Internal Error - Target: %s not found on array: %s", arrayTarget, symID)
		}
	}
	return iscsiTargets
}

// writeWWNFile writes a volume's WWN to a file copy on the node
func (s *service) writeWWNFile(id, volumeWWN string) error {
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	err := os.WriteFile(wwnFileName, []byte(volumeWWN), 0o644) // #nosec G306
	if err != nil {
		return status.Errorf(codes.Internal, "Could not write WWN file %s: %v", wwnFileName, err)
	}
	return nil
}

// readWWNFile reads the WWN from a file copy on the node
func (s *service) readWWNFile(id string) (string, error) {
	// READ volume WWN
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	wwnBytes, err := os.ReadFile(wwnFileName) // #nosec G304
	if err != nil {
		return "", status.Errorf(codes.Internal, "Could not read WWN file %s: %v", wwnFileName, err)
	}
	volumeWWN := string(wwnBytes)
	return volumeWWN, nil
}

// removeWWNFile removes the WWN file from the node local disk
func (s *service) removeWWNFile(id string) {
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	os.Remove(wwnFileName) // #nosec G20
}

// validateFCHostForAdoption performs coverage-based WWPN match validation on a host
// for host adoption (FR-1.3).
//
// Coverage is (expected WWPNs present on the host) / (total expected WWPNs):
//   - Zero coverage is always rejected — a complete HBA replacement or a foreign host.
//   - 2-WWPN nodes require 100% coverage. This is deliberately not configurable: with
//     only two initiators a single match is a coin flip, and adopting the wrong host
//     would expose another server's boot LUN.
//   - Nodes with 3 or more WWPNs use the configurable minOverlapRatio.
//
// Extra WWPNs on the host and missing WWPNs within the allowed threshold are warned
// about but do not block adoption, covering HBA replacement, HBA addition and partial
// zoning visibility.
func validateFCHostForAdoption(ctx context.Context, host *types.Host, expectedWWPNs []string, minOverlapRatio float64) error {
	log := csmlog.WithContext(ctx)
	if host == nil {
		return fmt.Errorf("validateFCHostForAdoption: nil host provided")
	}
	if len(expectedWWPNs) == 0 {
		return fmt.Errorf("validateFCHostForAdoption: empty expected WWPN list")
	}

	log.Infof("Host adoption: validating host %s (initiators %v) against %d expected WWPNs %v, min overlap ratio %.2f",
		host.HostID, host.Initiators, len(expectedWWPNs), expectedWWPNs, minOverlapRatio)

	expectedSet := make(map[string]bool, len(expectedWWPNs))
	for _, wwpn := range expectedWWPNs {
		expectedSet[wwpn] = true
	}
	hostWWPNs := make(map[string]bool, len(host.Initiators))
	for _, initID := range host.Initiators {
		hostWWPNs[initID] = true
	}

	matches := 0
	missing := make([]string, 0)
	for _, wwpn := range expectedWWPNs {
		if hostWWPNs[wwpn] {
			matches++
			continue
		}
		missing = append(missing, wwpn)
	}
	extra := make([]string, 0)
	for _, initID := range host.Initiators {
		if !expectedSet[initID] {
			extra = append(extra, initID)
		}
	}

	log.Infof("Host adoption: host %s covers %d/%d expected WWPNs", host.HostID, matches, len(expectedWWPNs))

	// Zero overlap: reject (complete HBA replacement or foreign host case)
	if matches == 0 {
		return fmt.Errorf("validateFCHostForAdoption: host %s has zero matching WWPNs out of %d expected (%v); "+
			"this indicates a complete HBA replacement or an unrelated host — "+
			"the storage administrator must verify the host configuration on the array",
			host.HostID, len(expectedWWPNs), expectedWWPNs)
	}

	if len(expectedWWPNs) == 2 {
		// 2-WWPN systems require 100% coverage regardless of configuration
		if matches != len(expectedWWPNs) {
			return fmt.Errorf("validateFCHostForAdoption: host %s has insufficient WWPN overlap for a 2-WWPN system: "+
				"%d/%d matches (100%% coverage required), missing %v; "+
				"the storage administrator must resolve the mismatch on the array",
				host.HostID, matches, len(expectedWWPNs), missing)
		}
	} else {
		requiredMatches := int(math.Ceil(float64(len(expectedWWPNs)) * minOverlapRatio))
		// Ensure at least 1 match for non-zero thresholds
		if requiredMatches == 0 && minOverlapRatio > 0 {
			requiredMatches = 1
		}
		if matches < requiredMatches {
			return fmt.Errorf("validateFCHostForAdoption: host %s has insufficient WWPN overlap: %d/%d matches "+
				"(need %.0f%% coverage, %d matches), missing %v; "+
				"the storage administrator must resolve the mismatch on the array",
				host.HostID, matches, len(expectedWWPNs), minOverlapRatio*100, requiredMatches, missing)
		}
	}

	// Warnings apply to every HBA count, including 2-WWPN systems where extra
	// initiators on the host are permitted alongside full coverage.
	if len(missing) > 0 {
		log.Warnf("Host adoption: host %s is missing expected WWPNs %v (adopting anyway with %d/%d matches)",
			host.HostID, missing, matches, len(expectedWWPNs))
	}
	if len(extra) > 0 {
		log.Warnf("Host adoption: host %s has extra WWPNs not belonging to this node: %v (adopting anyway with %d/%d matches)",
			host.HostID, extra, matches, len(expectedWWPNs))
	}
	return nil
}
