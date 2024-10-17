/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"io/ioutil"
	"math/rand"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dell/gonvme"

	"github.com/dell/csi-powermax/v2/pkg/file"

	"github.com/vmware/govmomi/object"

	pmax "github.com/dell/gopowermax/v2"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/coreos/go-systemd/v22/dbus"
	"github.com/dell/gobrick"
	csictx "github.com/dell/gocsi/context"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	log "github.com/sirupsen/logrus"

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
	nodePendingState            pendingState
	sysBlock                    = "/sys/block" // changed for unit testing
)

type maskingViewTargetInfo struct {
	target           goiscsi.ISCSITarget
	IsCHAPConfigured bool
}

type maskingViewNVMeTargetInfo struct {
	target gonvme.NVMeTarget
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
var isSymConnFC = make(map[string]bool)

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
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID, remoteSymID)
	if err != nil {
		log.Error(err.Error())
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
		log.Error("nodeProbe failed with error :" + err.Error())
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
		log.Error("Could not write WWN file: " + volumeWWN)
	}

	// Attach RDM
	if s.opts.IsVsphereEnabled {
		err := s.attachRDM(devID, volumeWWN)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		log.Debugf("attach RDM on VM complete...")
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

	f := log.Fields{
		"CSIRequestID":      reqID,
		"DeviceID":          devID,
		"ID":                req.VolumeId,
		"LUNAddress":        volumeLUNAddress,
		"PrivTgt":           privTgt,
		"SymmetrixID":       symID,
		"TargetIdentifiers": targetIdentifiers,
		"WWN":               volumeWWN,
	}
	log.WithFields(f).Info("NodeStageVolume")
	ctx = setLogFields(ctx, f)

	localPublishContextData := publishContextData{
		deviceWWN:        "0x" + volumeWWN,
		volumeLUNAddress: volumeLUNAddress,
	}
	iscsiTargets, fcTargets, nvmeTCPTargets, useFC, useNVMeTCP := s.getArrayTargets(ctx, targetIdentifiers, symID, pmaxClient)
	nodeCHRoot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)
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
	devicePath, err := s.connectDevice(ctx, localPublishContextData)
	if err != nil {
		return nil, err
	}
	// Connect Remote Device
	if remoteSymID != "" {
		remDeviceWWN := publishContext[RemotePublishContextDeviceWWN]
		remVolumeLUNAddress := publishContext[RemotePublishContextLUNAddress]
		remotePublishContextData := publishContextData{
			deviceWWN:        "0x" + remDeviceWWN,
			volumeLUNAddress: remVolumeLUNAddress,
		}
		if remDeviceWWN == "" {
			log.Error("Remote device WWN required to be in PublishContext")
			return nil, status.Error(codes.InvalidArgument, "Remote device WWN required to be in PublishContext")
		}
		f := log.Fields{
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
		log.WithFields(f).Info("NodeStageVolume for Remote Device")
		ctx = setLogFields(ctx, f)

		remoteTargetIdentifiers := ""
		if count, ok := publishContext[RemotePortIdentifierKeyCount]; ok {
			keyCount, _ = strconv.Atoi(count)
			for i := 1; i <= keyCount; i++ {
				portIdentifierKey := fmt.Sprintf("%s_%d", RemotePortIdentifiers, i)
				remoteTargetIdentifiers += publishContext[portIdentifierKey]
			}
		} else {
			remoteTargetIdentifiers = publishContext[RemotePortIdentifiers]
		}
		remIscsiTargets, remFcTargets, remNVMeTCPTargets, remUseFC, remUseNVMe := s.getArrayTargets(ctx, remoteTargetIdentifiers, remoteSymID, pmaxClient)
		log.Infof("Remote ISCSI Targets %v", remIscsiTargets)
		if remUseFC {
			s.initFCConnector(nodeCHRoot)
			remotePublishContextData.fcTargets = remFcTargets
		} else if remUseNVMe {
			s.initNVMeTCPConnector(nodeCHRoot)
			localPublishContextData.nvmetcpTargets = remNVMeTCPTargets
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

	log.WithFields(f).WithField("devPath", devicePath).Info("NodeStageVolume completed")
	return &csi.NodeStageVolumeResponse{}, nil
}

type publishContextData struct {
	deviceWWN        string
	volumeLUNAddress string
	iscsiTargets     []ISCSITargetInfo
	fcTargets        []FCTargetInfo
	nvmetcpTargets   []NVMeTCPTargetInfo
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

func (s *service) connectDevice(ctx context.Context, data publishContextData) (string, error) {
	logFields := getLogFields(ctx)
	var err error
	// The volumeLUNAddress is hex.
	lun, err := strconv.ParseInt(data.volumeLUNAddress, 16, 0)
	if err != nil {
		log.WithFields(logFields).Errorf("failed to convert lun number to int: %s", err.Error())
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
		log.Errorf("Unable to find device after multiple discovery attempts: %s", err.Error())
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
	// separate context to prevent 15 seconds cancel from kubernetes
	connectorCtx, cFunc := context.WithTimeout(context.Background(), time.Second*120)
	connectorCtx = setLogFields(connectorCtx, logFields)
	defer cFunc()
	wwn := data.deviceWWN
	// TBD connectorCtx = copyTraceObj(ctx, connectorCtx)
	connectorCtx = setLogFields(connectorCtx, logFields)
	return s.nvmeTCPConnector.ConnectVolume(connectorCtx, gobrick.NVMeVolumeInfo{
		Targets: targets,
		WWN:     wwn,
	}, false)
}

func (s *service) NodeUnstageVolume(
	ctx context.Context,
	req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse, error,
) {
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it, check if pending op for this volume ID
	id := req.GetVolumeId()
	_, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
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
		log.Infof("NodeUnstageVolume error unmount stage target %s: %s", stageTgt, err.Error())
	}
	removeWithRetry(stageTgt) // #nosec G20

	if len(devID) > 5 {
		return &csi.NodeUnstageVolumeResponse{}, nil
	}

	// READ volume WWN from the local file copy
	var volumeWWN string
	volumeWWN, err = s.readWWNFile(id)
	if err != nil || s.useNVMeTCP {
		log.Infof("Fallback to retrieve WWN from server: %s", err)

		// Fallback - retrieve the WWN from the array. Much more expensive.
		// Probe the node if required and make sure startup called
		err = s.nodeProbe(ctx)
		if err != nil {
			log.Error("nodeProbe failed with error :" + err.Error())
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

	if err := s.disconnectVolume(reqID, symID, devID, volumeWWN); err != nil {
		return nil, err
	}

	// Remove the mount private directory if present, and the directory
	privTgt := getPrivateMountPoint(s.privDir, id)
	err = removeWithRetry(privTgt)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	s.removeWWNFile(id)

	if s.opts.IsVsphereEnabled {
		err := s.detachRDM(devID, volumeWWN)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		log.Debugf("rescanning HBAs on host done...")
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// attachRDM attaches an RDM using volumeWWN to the host
func (s *service) attachRDM(volID, volumeWWN string) error {
	host, err := s.getVMHostSystem()
	if err != nil {
		log.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	log.Debugf("found host: (%v)", host)

	// Rescan the HBA
	if err := vmHost.RescanAllHba(host); err != nil {
		log.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	log.Debugf("rescanning HBAs on host done...")

	// Attach RDM
	err = vmHost.AttachRDM(vmHost.VM, volumeWWN)
	if err != nil {
		log.Errorf("Could not attach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
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
		log.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("Could not find host system (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	log.Debugf("found host: (%v)", host)

	// Detach RDM
	err = vmHost.DetachRDM(vmHost.VM, volumeWWN)
	if err != nil {
		log.Errorf("Could not detach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("could not detach RDM (%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}
	log.Debugf("detach RDM complete ...")

	// Rescan the HBA
	if err := vmHost.RescanAllHba(host); err != nil {
		log.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
		return fmt.Errorf("rescan all HBA failed(%s) vol: %s, on host %s with error: %s", volumeWWN, volID, vmHost.VM, err.Error())
	}

	return nil
}

// disconnectVolume disconnects a volume from a node and will verify it is disconnected
// by no more /dev/disk/by-id entry, retrying if necessary.
func (s *service) disconnectVolume(reqID, symID, devID, volumeWWN string) error {
	for i := 0; i < 3; i++ {
		var deviceName, symlinkPath, devicePath string
		symlinkPath, devicePath, _ = gofsutil.WWNToDevicePathX(context.Background(), volumeWWN)
		if devicePath == "" {
			if i == 0 {
				log.Infof("NodeUnstage Note- Didn't find device path for volume %s", volumeWWN)
			}
			return nil
		}
		devicePathComponents := strings.Split(devicePath, "/")
		deviceName = devicePathComponents[len(devicePathComponents)-1]
		f := log.Fields{
			"CSIRequestID": reqID,
			"DeviceID":     devID,
			"DeviceName":   deviceName,
			"DevicePath":   devicePath,
			"Retry":        i,
			"SymmetrixID":  symID,
			"WWN":          volumeWWN,
		}
		log.WithFields(f).Info("NodeUnstageVolume disconnectVolume")

		// Disconnect the volume using device name
		nodeUnstageCtx, cancel := context.WithTimeout(context.Background(), time.Second*120)
		nodeUnstageCtx = setLogFields(nodeUnstageCtx, f)
		switch s.arrayTransportProtocolMap[symID] {
		case Vsphere:
			_ = s.fcConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		case FcTransportProtocol:
			_ = s.fcConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		case IscsiTransportProtocol:
			_ = s.iscsiConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		case NvmeTCPTransportProtocol:
			_ = s.nvmeTCPConnector.DisconnectVolumeByDeviceName(nodeUnstageCtx, deviceName)
		}
		cancel()
		time.Sleep(disconnectVolumeRetryTime)

		// Check that the /sys/block/DeviceName actually exists
		if _, err := ioutil.ReadDir(sysBlock + deviceName); err != nil {
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
	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		if req, ok := headers["csi.requestid"]; ok && len(req) > 0 {
			reqID = req[0]
		}
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()
	_, symID, devID, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}
	// Get publishContext
	publishContext := req.GetPublishContext()

	// check if it is FileSystem volume
	if accTypeIsNFS([]*csi.VolumeCapability{req.GetVolumeCapability()}) {
		return file.PublishFileSystem(ctx, req, reqID, symID, devID, pmaxClient)
	}

	// Parse the CSI VolumeId and validate against the volume
	symID, devID, vol, err := s.GetVolumeByID(ctx, id, pmaxClient)
	if err != nil {
		return nil, err
	}

	// Get volumeContext
	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		log.Infof("VolumeContext:")
		for key, value := range volumeContext {
			log.Infof("    [%s]=%s", key, value)
		}
	}
	// Get publishContext
	deviceWWN := publishContext[PublishContextDeviceWWN]
	volumeLUNAddress := publishContext[PublishContextLUNAddress]
	if deviceWWN == "" {
		log.Error("Device WWN required to be in PublishContext")
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
	log.WithField("CSIRequestID", reqID).Infof("node publishing volume: %s lun: %s", deviceWWN, volumeLUNAddress)

	var symlinkPath string
	var devicePath string
	if s.useNVMeTCP {
		symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), vol.NGUID)
		if err != nil || symlinkPath == "" {
			errmsg := fmt.Sprintf("Device path not found for WWN %s: %s", deviceWWN, err)
			log.Error(errmsg)
			return nil, status.Error(codes.NotFound, errmsg)
		}
	} else {
		symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
		if err != nil || symlinkPath == "" {
			errmsg := fmt.Sprintf("Device path not found for WWN %s: %s", deviceWWN, err)
			log.Error(errmsg)
			return nil, status.Error(codes.NotFound, errmsg)
		}
	}

	f := log.Fields{
		"CSIRequestID":      reqID,
		"DeviceID":          devID,
		"DevicePath":        devicePath,
		"ID":                req.VolumeId,
		"Name":              volumeContext["Name"],
		"WWN":               deviceWWN,
		"PrivateDir":        s.privDir,
		"SymlinkPath":       symlinkPath,
		"SymmetrixID":       symID,
		"TargetPath":        req.GetTargetPath(),
		"TargetIdentifiers": targetIdentifiers,
	}

	log.WithFields(f).Info("Calling publishVolume")
	if err := publishVolume(req, s.privDir, symlinkPath, reqID); err != nil {
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
		log.Error("target path required")
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
		log.Debug(fmt.Sprintf("No target mount found... waiting %v to re-verify no target %s mount", targetMountRecheckSleepTime, target))
		time.Sleep(targetMountRecheckSleepTime)
		if targetMount, err = s.getTargetMount(target); err != nil {
			log.Info(fmt.Sprintf("Still no mount entry for target, so assuming this is an idempotent call: %s", target))
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
	}

	log.Infof("targetMount: %#v", targetMount)
	devicePath := targetMount.Device
	if devicePath == "devtmpfs" || devicePath == "" {
		devicePath = targetMount.Source
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	f := log.Fields{
		"CSIRequestID": reqID,
		"DevicePath":   devicePath,
		"ID":           id,
		"PrivateDir":   s.privDir,
		"TargetPath":   req.GetTargetPath(),
	}

	// Unmount the target path.
	log.WithFields(f).Info("Calling Unmount of TargetPath")
	err = gofsutil.Unmount(context.Background(), req.GetTargetPath())
	if err != nil {
		log.WithFields(f).Info("TargetPath unmount: " + err.Error())
	}

	log.WithFields(f).Info("Calling unpublishVolume")
	var lastUnmounted bool
	if lastUnmounted, err = unpublishVolume(req, s.privDir, devicePath, reqID); err != nil {
		return nil, err
	}

	log.Infof("lastUnmounted %v", lastUnmounted)
	if lastUnmounted {
		removeWithRetry(target) // #nosec G20
		return &csi.NodeUnpublishVolumeResponse{}, nil
	}

	// It is unusual that we have not removed the last mount (i.e. lastUnmounted == false)
	// Recheck to make sure the target is unmounted.
	log.WithFields(f).Info("Not the last mount - rechecking target mount is gone")
	if targetMount, err = s.getTargetMount(target); err != nil {
		return nil, err
	}
	if targetMount.Device != "" {
		log.WithFields(f).Error("Target mount still present... returning failure")
		return nil, status.Error(codes.Internal, "Target Mount still present")
	}
	// Get the device mounts
	dev, err := GetDevice(devicePath)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	log.WithFields(f).Info("rechecking dev mounts")
	mnts, err := getDevMounts(dev)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if len(mnts) > 0 {
		log.WithFields(f).Infof("Device mounts still present: %#v", mnts)
	}
	removeWithRetry(target) // #nosec G20
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

func (s *service) getTargetMount(target string) (gofsutil.Info, error) {
	var targetMount gofsutil.Info
	mounts, err := gofsutil.GetMounts(context.Background())
	if err != nil {
		log.Error("could not reliably determine existing mount status")
		return targetMount, status.Error(codes.Internal, "could not reliably determine existing mount status")
	}
	for _, mount := range mounts {
		if mount.Path == target {
			targetMount = mount
			log.Infof("matching targetMount %s target %s", target, mount.Path)
		}
	}
	return targetMount, nil
}

func (s *service) nodeProbe(ctx context.Context) error {
	log.Debug("Entering nodeProbe")
	defer log.Debug("Exiting nodeProbe")
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
	log.Debugf("Entering nodeProbe for array %s", symID)
	defer log.Debugf("Exiting nodeProbe for array %s", symID)

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
	hostID, _, _ := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.opts.NodeName)
	// get the host from the array
	if !s.useNVMeTCP {
		hostID, _, _ = s.GetHostSGAndMVIDFromNodeID(s.opts.NodeName, !s.useFC)
	}

	host, err := pmaxClient.GetHostByID(ctx, symID, hostID)
	if err != nil {
		if strings.Contains(err.Error(), notFound) && s.useNFS {
			log.Debugf("Error %s, while probing %s but since it's NFS this is expected", err.Error(), symID)
			return nil
		}
		// nodeId is not right/it's not NFS and still host is not preset
		log.Infof("Error %s, while probing %s", err.Error(), symID)
		return err
	}

	log.Debugf("Successfully got Host %s on %s", symID, host.HostID)

	if s.useFC {
		log.Debugf("Checking if FC initiators are logged in or not")
		initiatorList, err := pmaxClient.GetInitiatorList(ctx, symID, "", false, true)
		if err != nil {
			log.Error("Could not get initiator list: " + err.Error())
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
				log.Errorf("error performing iscsi login %s", err.Error())
				return err
			}
		} else {
			log.Infof("skippping login on host %s as no masking view exist", host.HostID)
		}

		log.Debugf("Checking if iscsi sessions are active on node or not")
		sessions, _ := s.iscsiClient.GetSessions()
		for _, target := range s.iscsiTargets[symID] {
			for _, session := range sessions {
				log.Debugf("matching %v with %v", target, session)
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
				log.Errorf("error performing NVMETCP login %s", err.Error())
				return err
			}
		} else {
			log.Infof("skippping login on host %s as no masking view exist", host.HostID)
		}
		log.Debugf("Checking if nvme sessions are active on node or not")
		sessions, _ := s.nvmetcpClient.GetSessions()
		for _, target := range s.nvmeTargets[symID] {
			for _, session := range sessions {
				log.Debugf("matching %v with %v", target, session)
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
	_ context.Context,
	_ *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error,
) {
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

func (s *service) getIPInterfaces(ctx context.Context, symID string, portGroups []string, pmaxClient pmax.Pmax) ([]string, error) {
	ipInterfaces := make([]string, 0)
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
			ipInterfaces = append(ipInterfaces, port.SymmetrixPort.IPAddresses...)
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

func (s *service) createTopologyMap(ctx context.Context, nodeName string) (map[string]string, error) {
	topology := map[string]string{}
	iscsiArrays := make([]string, 0)
	nvmeTCPArrays := make([]string, 0)
	var protocol string
	var ok bool

	arrays, err := s.retryableGetSymmetrixIDList()
	if err != nil {
		return nil, err
	}

	for _, id := range arrays.SymmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(id)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		if s.arrayTransportProtocolMap != nil {
			if protocol, ok = s.arrayTransportProtocolMap[id]; ok && (protocol == FcTransportProtocol || protocol == Vsphere) {
				continue
			}
		}

		ipInterfaces, err := s.getIPInterfaces(ctx, id, s.opts.PortGroups, pmaxClient)
		if err != nil {
			log.Errorf("unable to fetch ip interfaces for %s: %s", id, err.Error())
			continue
		}
		if len(ipInterfaces) == 0 {
			log.Errorf("Couldn't find any ip interfaces on any of the port-groups")
		}

		if protocol == NvmeTCPTransportProtocol {

			if s.loggedInNVMeArrays != nil {
				if isLoggedIn, ok := s.loggedInNVMeArrays[id]; ok && isLoggedIn {
					nvmeTCPArrays = append(nvmeTCPArrays, id)
					continue
				}
			}

			for _, ip := range ipInterfaces {
				isArrayConnected := false
				_, err := s.nvmetcpClient.DiscoverNVMeTCPTargets(ip, true)
				if err != nil {
					log.Errorf("Failed to connect to the IP interface(%s) of array(%s)", ip, id)
				} else {
					isArrayConnected = true
				}
				if isArrayConnected {
					nvmeTCPArrays = append(nvmeTCPArrays, id)
					break
				}
			}
		} else {
			if s.loggedInArrays != nil {
				if isLoggedIn, ok := s.loggedInArrays[id]; ok && isLoggedIn {
					iscsiArrays = append(iscsiArrays, id)
					continue
				}
			}

			for _, ip := range ipInterfaces {
				isArrayConnected := false
				_, err := s.iscsiClient.DiscoverTargets(ip, false)
				if err != nil {
					if s.isISCSIConnected(err) {
						isArrayConnected = true
					} else {
						log.Errorf("Failed to connect to the IP interface(%s) of array(%s)", ip, id)
					}
				} else {
					isArrayConnected = true
				}
				if isArrayConnected {
					iscsiArrays = append(iscsiArrays, id)
					break
				}
			}
		}

	}

	for array, protocol := range s.arrayTransportProtocolMap {
		if protocol == FcTransportProtocol && s.checkIfArrayProtocolValid(nodeName, array, strings.ToLower(FcTransportProtocol)) {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(FcTransportProtocol)] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NFS)] = s.getDriverName()
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
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NFS)] = s.getDriverName()

		}
	}

	for _, array := range nvmeTCPArrays {
		if _, ok := topology[s.getDriverName()+"/"+array]; !ok &&
			s.checkIfArrayProtocolValid(nodeName, array, strings.ToLower(NvmeTCPTransportProtocol)) {
			topology[s.getDriverName()+"/"+array] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NvmeTCPTransportProtocol)] = s.getDriverName()
			topology[s.getDriverName()+"/"+array+"."+strings.ToLower(NFS)] = s.getDriverName()

		}
	}

	return topology, nil
}

// checkIfArrayProtocolValid returns true if the  pair (array and protocol) is applicable for the given node based on config
// if the pair is present in allow rules, it is applied in the topology keys map
// if the pair is present in deny rules, it is skipped in the topology keys map
func (s *service) checkIfArrayProtocolValid(nodeName string, array string, protocol string) bool {
	if !s.opts.IsTopologyControlEnabled {
		return true
	}

	key := fmt.Sprintf("%s.%s", array, protocol)
	log.Debugf("Checking topology config for allow rules for key (%s)", key)
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

	log.Debugf("Checking topology config for deny rules for key (%s)", key)
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
	log.Debugf("applied topo key for node %s : %+v", nodeName, key)
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
	// Get the Node ID
	if s.opts.NodeName == "" {
		log.Error("Unable to get Node Name from the environment")
		return nil, status.Error(codes.FailedPrecondition,
			"Unable to get Node Name from the environment")
	}

	topology, err := s.createTopologyMap(ctx, s.opts.NodeName)
	if err != nil {
		log.Errorf("Unable to get the list of symmetrix ids. (%s)", err.Error())
		return nil, status.Error(codes.FailedPrecondition,
			"Unable to get the list of symmetrix ids")
	}
	if len(topology) == 0 {
		// if s.opts.IsVsphereEnabled {
		// array:vsphere
		// }
		log.Errorf("No topology keys could be generated")
		return nil, status.Error(codes.FailedPrecondition, "no topology keys could be generate")
	}

	var maxPowerMaxVolumesPerNode int64
	labels, err := s.k8sUtils.GetNodeLabels(s.opts.NodeFullName)
	if err != nil {
		log.Infof("failed to get Node Labels with error '%s'", err.Error())
	}
	if val, ok := labels["max-powermax-volumes-per-node"]; ok {
		maxPowerMaxVolumesPerNode, err = strconv.ParseInt(val, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid value '%s' specified for 'max-powermax-volumes-per-node' node label", val)
		}
		if s.opts.IsVsphereEnabled {
			if maxPowerMaxVolumesPerNode == 0 || maxPowerMaxVolumesPerNode > 60 {
				log.Errorf("Node label max-powermax-volumes-per-node should not be greater than 60 or set to any negative value for RDM volumes, Setting to default value 60")
				maxPowerMaxVolumesPerNode = 60
			}
		}
		log.Infof("node label 'max-powermax-volumes-per-node' is available and is set to value '%v'", maxPowerMaxVolumesPerNode)
	} else {
		// As per the csi spec the plugin MUST NOT set negative values to
		// 'MaxVolumesPerNode' in the NodeGetInfoResponse response
		log.Infof("Node label 'max-powermax-volumes-per-node' is not available. Retrieving the value from yaml file")
		if s.opts.IsVsphereEnabled {
			if s.opts.MaxVolumesPerNode <= 0 || s.opts.MaxVolumesPerNode > 60 {
				log.Errorf("maxPowerMaxVolumesPerNode MUST NOT be greater than 60 or set to any negative value for RDM volumes. Setting to default value 60")
				s.opts.MaxVolumesPerNode = 60
			}
		} else {
			if s.opts.MaxVolumesPerNode < 0 {
				log.Errorf("maxPowerMaxVolumesPerNode MUST NOT be set to negative value, setting to default value 0")
				s.opts.MaxVolumesPerNode = 0
			}
		}
		maxPowerMaxVolumesPerNode = s.opts.MaxVolumesPerNode
	}
	return &csi.NodeGetInfoResponse{
		NodeId: s.opts.NodeName,
		AccessibleTopology: &csi.Topology{
			Segments: topology,
		},
		MaxVolumesPerNode: maxPowerMaxVolumesPerNode,
	}, nil
}

func (s *service) NodeGetVolumeStats(
	ctx context.Context, req *csi.NodeGetVolumeStatsRequest,
) (*csi.NodeGetVolumeStatsResponse, error) {
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
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}

	volPath := req.GetVolumePath()
	if volPath == "" {
		log.Error("Volume path required")
		return nil, status.Error(codes.InvalidArgument, "no Volume path found in request, a volume path is required")
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}

	f := log.Fields{
		"CSIRequestID":      reqID,
		"VolumePath":        volPath,
		"ID":                id,
		"VolumeName":        volName,
		"StagingTargetPath": req.GetStagingTargetPath(),
	}
	log.WithFields(f).Info("Calling NodeGetVolumeStats")

	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
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
		log.Debug("---- check 1 ----")
		replace := CSIPrefix + "-" + s.getClusterPrefix() + "-"
		volName = strings.Replace(volName, replace, "", 1)
		// remove the namespace from the volName as the mount paths will not have it
		volName = strings.Join(strings.Split(volName, "-")[:2], "-")
		isMounted, err := isVolumeMounted(ctx, volName, volPath)
		log.Debug("---- isMounted ----", isMounted)
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
		log.Debug("---- check 2 ----")
		// check if volume path is accessible
		_, err = os.ReadDir(volPath)
		if err != nil {
			abnormal = true
			msg = fmt.Sprintf("volume Path is not accessible: %s", err)
		}
		log.Debug("---- path is readable ----")
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
			"failed to get volume stats: %s", err))
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
	log.Debugf("target '%s' does not exist", target)
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
			log.Errorf("nodeStartup could not GetFCHostPortWWNs %s", err.Error())
		}

		// Get iscsi initiators.
		IQNs, err = s.iscsiClient.GetInitiators("")
		if err != nil {
			log.Errorf("nodeStartup could not GetInitiatorIQNs: %s", err.Error())
		}

		// Get nmve initiators
		hostNQN, err = s.nvmetcpClient.GetInitiators("")
		if err != nil {
			log.Errorf("nodeStartup could not GetInitiatorHostNQNs: %s", err.Error())
		}

		log.Infof("TransportProtocol %s FC portWWNs: %s ... IQNs: %s ... HostNQNs: %s ", s.opts.TransportProtocol, portWWNs, IQNs, hostNQN)
		// The driver needs at least one FC or iSCSI or NVME initiator to be defined
		if len(portWWNs) == 0 && len(IQNs) == 0 && len(hostNQN) == 0 {
			log.Errorf("No FC, iSCSI or NVMe initiators were found and at least 1 is required")
			s.useNFS = true
			return nil
		}
	} else {
		err := s.setVMHost()
		if err != nil {
			return err
		}
		log.Debug("vmHost created successfully")
	}

	arrays, err := s.retryableGetSymmetrixIDList()
	if err != nil {
		log.Error("Failed to fetch array list. Continuing without initializing node")
		return err
	}
	symmetrixIDs := arrays.SymmetrixIDs
	log.Debug(fmt.Sprintf("GetSymmetrixIDList returned: %v", symmetrixIDs))

	err = s.nodeHostSetup(ctx, portWWNs, IQNs, hostNQN, symmetrixIDs)
	if err != nil {
		return err
	} // #nosec G20
	go s.startAPIService(ctx)
	return err
}

// setVMHost create client for the vCenter
func (s *service) setVMHost() error {
	// Create a VM host
	host, err := NewVMHost(true, s.opts.VCenterHostURL, s.opts.VCenterHostUserName, s.opts.VCenterHostPassword, s.opts.IfaceExcludeFilter)
	if err != nil {
		log.Errorf("can not create VM host object: (%s)", err.Error())
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
		log.Warning("Failed to fetch initiator list for the SYM :" + symID)
		return validInitiators, err
	}
	hostUpdated := false
	for _, nodeInitiator := range nodeInitiators {
		if strings.HasPrefix(nodeInitiator, "0x") {
			nodeInitiator = strings.Replace(nodeInitiator, "0x", "", 1)
		}

		for _, initiatorID := range initList.InitiatorIDs {
			if initiatorID == nodeInitiator || strings.Contains(initiatorID, nodeInitiator) {
				log.Infof("Checking initiator %s against host %s", initiatorID, hostID)
				initiator, err := pmaxClient.GetInitiatorByID(ctx, symID, initiatorID)
				if err != nil {
					log.Warning("Failed to fetch initiator details for initiator: " + initiatorID)
					continue
				}
				if initiator.Host != "" {
					if initiator.Host != hostID &&
						s.opts.ModifyHostName {
						if !hostUpdated {
							// User has set ModifyHostName to modify host name in case of a mismatch
							log.Infof("UpdateHostName processing: %s to %s", initiator.Host, hostID)
							_, err := pmaxClient.UpdateHostName(ctx, symID, initiator.Host, hostID)
							if err != nil {
								errormsg = fmt.Sprintf("Failed to change host name from %s to %s: %s", initiator.HostID, hostID, err)
								log.Warning(errormsg)
								continue
							}
							hostUpdated = true
						} else {
							errormsg = fmt.Sprintf("Skipping Updating Host %s for initiator: %s as updated host already present on: %s", initiator.HostID,
								initiatorID, symID)
							log.Warning(errormsg)
							continue
						}
					} else if initiator.Host != hostID {
						errormsg = fmt.Sprintf("initiator: %s is already a part of a different host: %s on: %s",
							initiatorID, initiator.Host, symID)
						log.Warning(errormsg)
						continue
					}
				}
				log.Infof("valid initiator: %s\n", initiatorID)
				validInitiators = appendIfMissing(validInitiators, nodeInitiator)
				errormsg = ""
			}
		}
	}
	if 0 < len(errormsg) {
		return validInitiators, fmt.Errorf("%s", errormsg)
	}

	if len(validInitiators) == 0 && strings.Contains(hostID, NvmeTCPTransportProtocol) {
		log.Infof("No existing NVMe hosts; new hosts will be created for all discovered initiators")
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
	log.Info("**************************nodeHostSetup executing...*******************************")
	defer log.Info("**************************nodeHostSetup completed...*******************************")

	// we need to randomize a time before starting the interaction with unisphere
	// in order to reduce the concurrent workload on the system

	// determine a random delay period
	period := rand.Int() % maximumStartupDelay // #nosec G404
	// sleep ...
	log.Infof("Waiting for %d seconds", period)
	time.Sleep(time.Duration(period) * time.Second)

	// See if it's viable to use FC and/or ISCSI and/or NVMeTCP
	hostIDIscsi, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
	hostIDFC, _, _ := s.GetFCHostSGAndMVIDFromNodeID(s.opts.NodeName)
	hostIDNVMeTCP, _, _ := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.opts.NodeName)

	// Loop through the symmetrix, looking for existing initiators
	for _, symID := range symmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(symID)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		if s.arrayTransportProtocolMap == nil {
			s.arrayTransportProtocolMap = make(map[string]string)
		}
		if s.opts.IsVsphereEnabled {
			err := s.getHostForVsphere(ctx, symID, pmaxClient)
			if err != nil {
				log.Warningf("Host/HostGroup %s was not initialized on sym %s, err: %s", s.opts.VSphereHostName, symID, err.Error())
			} else {
				s.arrayTransportProtocolMap[symID] = Vsphere
			}
			continue
		}
		validFCs, err := s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, portWWNs, hostIDFC, pmaxClient)
		if err != nil {
			log.Error("Could not validate FC initiators " + err.Error())
		}
		log.Infof("valid FC initiators: %v", validFCs)
		if len(validFCs) > 0 && (s.opts.TransportProtocol == "" || s.opts.TransportProtocol == FcTransportProtocol) {
			// We do have to have pre-existing initiators that were zoned for FC
			s.useFC = true
		}

		validNVMeTCPs, err := s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, NQNs, hostIDNVMeTCP, pmaxClient)
		if err != nil {
			log.Error("Could not validate NVMeTCP initiators " + err.Error())
		} else if len(validNVMeTCPs) > 0 && s.opts.TransportProtocol == "" || s.opts.TransportProtocol == NvmeTCPTransportProtocol {
			// If pre-existing NVMeTCP initiators are not found, initiators/host should be created
			s.useNVMeTCP = true
		}
		log.Infof("valid NVMeTCP initiators: %v", validNVMeTCPs)

		validIscsis, err := s.verifyAndUpdateInitiatorsInADiffHost(ctx, symID, IQNs, hostIDIscsi, pmaxClient)
		if err != nil {
			log.Error("Could not validate iSCSI initiators" + err.Error())
		} else if s.opts.TransportProtocol == "" || s.opts.TransportProtocol == IscsiTransportProtocol {
			// We do not have to have pre-existing initiators to use Iscsi (we can create them)
			s.useIscsi = true
		}
		log.Infof("valid (existing) iSCSI initiators (must be manually created): %v", validIscsis)
		if len(validIscsis) == 0 {
			// IQNs are not yet part of any host on Unisphere
			validIscsis = IQNs
		}

		if !s.useFC && !s.useIscsi && !s.useNVMeTCP {
			log.Error("No valid initiators- could not initialize NVMeTCP or FC or iSCSI")
			return err
		}

		nodeChroot, _ := csictx.LookupEnv(context.Background(), EnvNodeChroot)
		if s.useNVMeTCP {
			// resetOtherProtocols
			s.useFC = false
			s.useIscsi = false
			// check nvme module availability on the host
			err = s.setupArrayForNVMeTCP(ctx, symID, validNVMeTCPs, pmaxClient)
			if err != nil {
				log.Errorf("Failed to do the NVMe setup for the Array(%s). Error - %s", symID, err.Error())
			}
			s.initNVMeTCPConnector(nodeChroot)
			s.arrayTransportProtocolMap[symID] = NvmeTCPTransportProtocol
		} else if s.useFC {
			// resetOtherProtocols
			s.useNVMeTCP = false
			s.useIscsi = false
			formattedFCs := make([]string, 0)
			for _, initiatorID := range validFCs {
				elems := strings.Split(initiatorID, ":")
				formattedFCs = appendIfMissing(formattedFCs, "0x"+elems[len(elems)-1])
			}
			err := s.setupArrayForFC(ctx, symID, formattedFCs, pmaxClient)
			if err != nil {
				log.Errorf("Failed to do the FC setup the Array(%s). Error - %s", symID, err.Error())
			}
			s.initFCConnector(nodeChroot)
			s.arrayTransportProtocolMap[symID] = FcTransportProtocol
			isSymConnFC[symID] = true
		} else if s.useIscsi {
			// resetOtherProtocols
			s.useNVMeTCP = false
			s.useNFS = false
			err := s.ensureISCSIDaemonStarted()
			if err != nil {
				log.Errorf("Failed to start the ISCSI Daemon. Error - %s", err.Error())
			}
			err = s.setupArrayForIscsi(ctx, symID, validIscsis, pmaxClient)
			if err != nil {
				log.Errorf("Failed to do the ISCSI setup for the Array(%s). Error - %s", symID, err.Error())
			}
			s.initISCSIConnector(nodeChroot)
			s.arrayTransportProtocolMap[symID] = IscsiTransportProtocol
		}
	}

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
	hostName, _, mvName := s.GetFCHostSGAndMVIDFromNodeID(s.opts.NodeName)
	log.Infof("setting up array %s for Fibrechannel, host name: %s masking view: %s", array, hostName, mvName)
	_, err := s.createOrUpdateFCHost(ctx, array, hostName, portWWNs, pmaxClient)
	return err
}

// setupArrayForIscsi is called to set up a node for iscsi operation.
func (s *service) setupArrayForIscsi(ctx context.Context, array string, IQNs []string, pmaxClient pmax.Pmax) error {
	hostName, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
	log.Infof("setting up array %s for Iscsi, host name: %s masking view ID: %s", array, hostName, mvName)

	// Create or update the IscsiHost and Initiators
	_, err := s.createOrUpdateIscsiHost(ctx, array, hostName, IQNs, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	_, err = s.getAndConfigureMaskingViewTargets(ctx, array, mvName, IQNs, pmaxClient)
	return err
}

// setupArrayForIscsi is called to set up a node for iscsi operation.
func (s *service) setupArrayForNVMeTCP(ctx context.Context, array string, NQNs []string, pmaxClient pmax.Pmax) error {
	hostName, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.opts.NodeName)
	log.Infof("setting up array %s for NVMeTCP, host name: %s masking view ID: %s", array, hostName, mvName)

	// Discover targets on the host
	err := s.setupNVMeTCPTargetDiscovery(ctx, array, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return err
	}
	updatesHostNQNs, err := s.updateNQNWithHostID(ctx, array, NQNs, pmaxClient)
	if err != nil || updatesHostNQNs == nil {
		return fmt.Errorf(" Error updating NQN with HostID, len of updatedNQN: %d", len(updatesHostNQNs))
	}

	// Create or update the NVMe Host and Initiators dummy
	_, err = s.createOrUpdateNVMeTCPHost(ctx, array, hostName, updatesHostNQNs, pmaxClient)
	if err != nil {
		log.Error(err.Error())
		return err
	}

	// Create or update the NVMe Host and Initiators
	_, err = s.getAndConfigureMaskingViewTargetsNVMeTCP(ctx, array, mvName, pmaxClient)
	return err
}

func (s *service) updateNQNWithHostID(ctx context.Context, symID string, NQNs []string, pmaxClient pmax.Pmax) ([]string, error) {
	updatesHostNQNs := make([]string, 0)
	// Process the NQN to append hostId
	hostInitiators, err := pmaxClient.GetInitiatorList(ctx, symID, "", false, false)
	if err != nil {
		log.Error("Failed to fetch initiator list for the SYM :" + symID)
		return nil, err
	}

	for _, hostInitiator := range hostInitiators.InitiatorIDs {
		// hostInitiator = OR-1C:001:nqn.2014-08.org.nvmexpress:uuid:csi_master:76B04D56EAB26A2E1509A7E98D3DFDB6
		for _, nqn := range NQNs {
			// nqn = [nqn.2014-08.org.nvmexpress:uuid:csi_master]
			if strings.Contains(hostInitiator, nqn) {
				initiator, err := pmaxClient.GetInitiatorByID(ctx, symID, hostInitiator)
				if err != nil {
					log.Errorf("Failed to fetch InitiatorID details for the initiator %s", initiator.InitiatorID)
				} else {
					hostID := initiator.HostID
					nqn = nqn + ":" + hostID
					log.Infof("updated host nqn is: %s", nqn)
				}
				updatesHostNQNs = append(updatesHostNQNs, nqn)
			}
		}
	}
	return updatesHostNQNs, nil
}

// getAndConfigureMaskingViewTargets - Returns a list of ISCSITargets for a given masking view
// also update the node database with CHAP authentication (if required) and perform discovery/login
func (s *service) getAndConfigureMaskingViewTargets(ctx context.Context, array, mvName string, IQNs []string, pmaxClient pmax.Pmax) ([]goiscsi.ISCSITarget, error) {
	// Check the masking view
	goISCSITargets := make([]goiscsi.ISCSITarget, 0)
	view, err := pmaxClient.GetMaskingViewByID(ctx, array, mvName)
	if err != nil {
		// masking view does not exist, not an error but no need to login
		log.Debugf("Masking View %s does not exist for array %s, skipping login", mvName, array)
		return goISCSITargets, err
	}
	log.Infof("Masking View: %s exists for array id: %s", mvName, array)
	// masking view exists, we need to log into some targets
	// this will also update the cache
	targets, err := s.getIscsiTargetsForMaskingView(ctx, array, view, pmaxClient)
	if err != nil {
		log.Debugf("%s", err.Error())
		return goISCSITargets, err
	}
	for _, tgt := range targets {
		goISCSITargets = append(goISCSITargets, tgt.target)
	}
	log.Debugf("Masking View Targets: %v", goISCSITargets)
	// First set the CHAP credentials
	if len(targets) > 0 {
		err = s.setCHAPCredentials(array, targets, IQNs)
		if err != nil {
			errorMsg := "Unable to set ISCSI CHAP credentials for some targets in the node database"
			log.Errorf("%s Error - %s", errorMsg, err.Error())
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
		log.Debugf("Masking View %s does not exist for array %s, skipping login", mvName, array)
		return goNVMeTargets, err
	}
	log.Infof("Masking View: %s exists for array id: %s", mvName, array)
	// masking view exists, we need to log into some targets
	// this will also update the cache
	targets, err := s.getNVMeTCPTargetsForMaskingView(ctx, array, view, pmaxClient)
	if err != nil {
		log.Debugf("%s", err.Error())
		return goNVMeTargets, err
	}
	for _, tgt := range targets {
		goNVMeTargets = append(goNVMeTargets, tgt.target)
	}
	log.Debugf("Masking View Targets: %v", goNVMeTargets)
	// Login to targets
	if len(targets) > 0 {
		err = s.loginIntoNVMeTCPTargets(array, targets)
	}
	return goNVMeTargets, err
}

// setupNVMeTCPTargetDiscovery is called to discover NVMe targets from the host/node
func (s *service) setupNVMeTCPTargetDiscovery(ctx context.Context, array string, pmaxClient pmax.Pmax) error {
	loggedInAll := true
	ipInterfaces, err := s.getIPInterfaces(ctx, array, s.opts.PortGroups, pmaxClient)
	if err != nil {
		log.Errorf("unable to fetch ip interfaces for %s: %s", array, err.Error())
		return err
	}

	if len(ipInterfaces) == 0 {
		log.Errorf("Couldn't find any ip interfaces on any of the port-groups")
		return err
	}

	for _, ip := range ipInterfaces {
		// Attempt target discovery from host
		log.Debugf("Discovering NVMe targets on %s", ip)
		_, discoveryError := s.nvmetcpClient.DiscoverNVMeTCPTargets(ip, true)
		if discoveryError != nil {
			log.Errorf("Failed to discover the NVMe target: %s. Error: %s",
				ip, discoveryError.Error())
			err = discoveryError
			loggedInAll = false
		} else {
			log.Infof("Successfully logged into target IP: %s ", ip)
		}
	}

	if loggedInAll {
		return nil
	}
	return err
}

// loginIntoISCSITargets - for a given array id and list of masking view targets
// attempt login. The login method is different if CHAP is enabled
// also update the logged in arrays cache
func (s *service) loginIntoISCSITargets(array string, targets []maskingViewTargetInfo) error {
	var err error
	loggedInAll := true
	if s.opts.EnableCHAP {
		// CHAP is already enabled on the array, discovery will not work,
		// so we need to do a login (as we have already setup the database(s) successfully
		for _, tgt := range targets {
			loginError := s.iscsiClient.PerformLogin(tgt.target)
			if loginError != nil {
				log.Errorf("Failed to perform ISCSI login for target: %s. Error: %s",
					tgt.target.Target, loginError.Error())
				err = loginError
				loggedInAll = false
			} else {
				s.iscsiTargets[array] = append(s.iscsiTargets[array], tgt.target.Target)
				log.Infof("Successfully logged into target: %s", tgt.target.Target)
			}
		}
	} else {
		for _, tgt := range targets {
			// If CHAP is not enabled, attempt a discovery login
			log.Debugf("Discovering iSCSI targets on %s", tgt.target.Portal)
			_, discoveryError := s.iscsiClient.DiscoverTargets(tgt.target.Portal, true)
			if discoveryError != nil {
				log.Errorf("Failed to discover the ISCSI target: %s. Error: %s",
					tgt.target.Target, discoveryError.Error())
				err = discoveryError
				loggedInAll = false
			} else {
				s.iscsiTargets[array] = append(s.iscsiTargets[array], tgt.target.Target)
				log.Infof("Successfully logged into target: %s on portal :%s",
					tgt.target.Target, tgt.target.Portal)
			}
		}
	}
	// If we successfully logged into all targets, then marked the array as logged in
	if loggedInAll {
		s.cacheMutex.Lock()
		s.loggedInArrays[array] = true
		s.cacheMutex.Unlock()
	}
	return err
}

// loginIntoNVMeTargets - for a given array id and list of masking view targets
// also update the logged in arrays cache
func (s *service) loginIntoNVMeTCPTargets(array string, targets []maskingViewNVMeTargetInfo) error {
	var err error
	loggedInAll := true
	for _, tgt := range targets {
		// Attempt target discovery from host
		log.Debugf("Discovering NVMe targets on %s", tgt.target.Portal)
		_, discoveryError := s.nvmetcpClient.DiscoverNVMeTCPTargets(tgt.target.Portal, true)
		if discoveryError != nil {
			log.Errorf("Failed to discover the NVMe target: %s. Error: %s",
				tgt.target.PortID, discoveryError.Error())
			err = discoveryError
			loggedInAll = false
		} else {
			s.nvmeTargets[array] = append(s.nvmeTargets[array], tgt.target.TargetNqn)
			log.Infof("Successfully logged into target: %s on portal :%s",
				tgt.target.PortID, tgt.target.Portal)
		}
	}

	// If we successfully logged into all targets, then marked the array as logged in
	if loggedInAll {
		s.cacheMutex.Lock()
		s.loggedInNVMeArrays[array] = true
		s.cacheMutex.Unlock()
	}
	return err
}

// setCHAPCredentials - Sets the CHAP credentials for a list of masking view targets
// in the node database. Also if any credentials were updated, it updates the target cache
func (s *service) setCHAPCredentials(array string, targets []maskingViewTargetInfo, IQNs []string) error {
	if s.opts.EnableCHAP {
		if len(IQNs) > 0 {
			if s.opts.CHAPUserName != "" {
				errorMsg := "multiple IQNs found on host and CHAP username is not set. invalid configuration"
				log.Debugf("%s", errorMsg)
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
				log.Debugf("Setting CHAP credentials for targets: %v", targets)
				err := s.iscsiClient.SetCHAPCredentials(targets[i].target, chapUserName, s.opts.CHAPPassword)
				if err != nil {
					log.Error(err)
					// If we were able to set credentials for some targets successfully
					// even then we won't be updating the cache
					return err
				}
				log.Debugf("Successfully set CHAP credentials for targets: %v", targets)
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
		log.Errorf("Failed to list systemd units. Error - %s", err.Error())
		return err
	}
	unit := getUnitStatus(units, target)
	if unit == nil {
		// Failed to get the status of ISCSI Daemon
		errMsg := fmt.Sprintf("failed to find %s. Going to panic", target)
		log.Error(errMsg)
		panic(fmt.Errorf("%s", errMsg))
	} else if unit.ActiveState != "active" {
		log.Infof("%s is not active. Current state - %s", target, unit.ActiveState)
	} else {
		log.Info("ISCSI Daemon is active")
		return nil
	}
	log.Infof("Attempting to start %s", target)
	responsechan := make(chan string)
	// We try to replace the unit
	_, err = s.dBusConn.StartUnit(target, "replace", responsechan)
	if err != nil {
		// Failed to start the unit
		log.Errorf("Failed to start %s. Error - %s", target, err.Error())
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
		log.Error(errMsg)
		return fmt.Errorf("%s", errMsg)
	}
	log.Info("Successfully started ISCSI daemon")
	return nil
}

func (s *service) ensureLoggedIntoEveryArray(ctx context.Context, _ bool) error {
	arrays := &types.SymmetrixIDList{}
	var err error

	// Get the list of arrays
	arrays, err = s.retryableGetSymmetrixIDList()
	if err != nil {
		return err
	}
	// for each array known to unisphere, ensure we have performed ISCSI login for our masking views
	for _, array := range arrays.SymmetrixIDs {
		pmaxClient, err := s.GetPowerMaxClient(array)
		if err != nil {
			log.Error(err.Error())
			continue
		}
		if isSymConnFC[array] {
			// Check if we have marked this array as FC earlier
			continue
		}
		s.cacheMutex.Lock()
		if s.loggedInArrays[array] {
			// we have already logged into this array
			log.Debugf("(ISCSI) Already logged into the array: %s", array)
			s.cacheMutex.Unlock()
			break
		}
		if s.loggedInNVMeArrays[array] {
			// we have already logged into this array
			log.Debugf("(NVME) Already logged into the array: %s", array)
			s.cacheMutex.Unlock()
			break
		}
		if s.useIscsi {
			log.Debugf("(ISCSI) No logins were done earlier for %s", array)
			s.cacheMutex.Unlock()
			_, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
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
			log.Debugf("(ISCSI) No logins were done earlier for %s", array)
			s.cacheMutex.Unlock()
			_, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.opts.NodeName)
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
				log.Debugf("Couldn't configure NVME targets as masking view: %s doesn't exist for array: %s",
					mvName, array)
				tempErr = nil
			} else {
				err = tempErr
				log.Errorf("Failed to configure NVME targets for masking view: %s, array: %s. Error: %s",
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
		log.Debugf("Cache hit for %s", array)
		maskingViewTargets := mvTargets.([]maskingViewTargetInfo)
		// First set the CHAP credentials if required
		err = s.setCHAPCredentials(array, maskingViewTargets, IQNs)
		if err != nil {
			// log the error and continue
			log.Errorf("Failed to set CHAP credentials for %v", maskingViewTargets)
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
				log.Debugf("Couldn't configure ISCSI targets as masking view: %s doesn't exist for array: %s",
					mvName, array)
				tempErr = nil
			} else {
				err = tempErr
				log.Errorf("Failed to configure ISCSI targets for masking view: %s, array: %s. Error: %s",
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
	// get the PortGroup in the masking view
	portGroup, err := pmaxClient.GetPortGroupByID(ctx, array, view.PortGroupID)
	if err != nil {
		return []maskingViewNVMeTargetInfo{}, err
	}
	targets := make([]maskingViewNVMeTargetInfo, 0)
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
			t := gonvme.NVMeTarget{
				Portal:    ip,
				TargetNqn: port.SymmetrixPort.Identifier,
			}
			targets = append(targets, maskingViewNVMeTargetInfo{
				target: t,
			})
		}
	}
	symToMaskingViewTargets.Store(array, targets)
	return targets, nil
}

func (s *service) createOrUpdateFCHost(ctx context.Context, array string, nodeName string, portWWNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	log.Info(fmt.Sprintf("Processing FC Host array: %s, nodeName: %s, initiators: %v", array, nodeName, portWWNs))
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
		log.Error("Could not get initiator list: " + err.Error())
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
	log.Infof("hostInitiators: %s", hostInitiators)

	// See if the host is present
	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	log.Debug(fmt.Sprintf("GetHostById returned: %v, %v", host, err))
	if err != nil {
		// host does not exist, create it
		log.Infof("Array %s FC Host %s does not exist. Creating it.", array, nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, hostInitiators, nil, pmaxClient)
		if err != nil {
			return host, err
		}
	} else {
		// make sure we don't update an iscsi host
		arrayInitiators := stringSliceRegexMatcher(host.Initiators, "(0x)*[0-9a-fA-F]")
		stringSliceRegexReplace(arrayInitiators, "^.*:.*:", "")
		// host does exist, update it if necessary
		if len(arrayInitiators) != 0 && !stringSlicesEqual(arrayInitiators, hostInitiators) {
			log.Infof("updating host: %s initiators to: %s", nodeName, hostInitiators)
			_, err := s.retryableUpdateHostInitiators(ctx, array, host, portWWNs, pmaxClient)
			if err != nil {
				return host, err
			}
		}
	}
	return host, nil
}

func (s *service) createOrUpdateIscsiHost(ctx context.Context, array string, nodeName string, IQNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	log.Debug(fmt.Sprintf("Processing Iscsi Host array: %s, nodeName: %s, initiators: %v", array, nodeName, IQNs))
	if array == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(IQNs) == 0 {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No IQNs specified")
	}

	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	log.Infof("GetHostById returned: %v, %v", host, err)

	if err != nil {
		// host does not exist, create it
		log.Infof("ISCSI Host %s does not exist. Creating it.", nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, IQNs, nil, pmaxClient)
		if err != nil {
			return &types.Host{}, fmt.Errorf("Unable to create Host: %v", err)
		}
	} else {
		// Make sure we don't update an FC host
		hostInitiators := stringSliceRegexMatcher(host.Initiators, "^iqn\\.")
		// host does exist, update it if necessary
		if len(hostInitiators) != 0 && !stringSlicesEqual(hostInitiators, IQNs) {
			log.Infof("updating host: %s initiators to: %s", nodeName, IQNs)
			if _, err := s.retryableUpdateHostInitiators(ctx, array, host, IQNs, pmaxClient); err != nil {
				return host, err
			}
		}
	}
	return host, nil
}

func (s *service) createOrUpdateNVMeTCPHost(ctx context.Context, array string, nodeName string, NQNs []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	log.Debug(fmt.Sprintf("Processing NVMeTCP Host array: %s, nodeName: %s, initiators: %v", array, nodeName, NQNs))
	if array == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(NQNs) == 0 {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No NQNs specified")
	}

	// process the NQNs
	host, err := pmaxClient.GetHostByID(ctx, array, nodeName)
	log.Infof("GetHostById returned: %v, %v", host, err)

	if err != nil {
		// host does not exist, create it
		log.Infof("NVMe Host %s does not exist. Creating it.", nodeName)
		host, err = s.retryableCreateHost(ctx, array, nodeName, NQNs, nil, pmaxClient)
		if err != nil {
			return &types.Host{}, fmt.Errorf("Unable to create Host: %v", err)
		}
	} else {
		// Make sure we fetch only the NVMe hosts
		hostInitiators := stringSliceRegexMatcher(host.Initiators, "^nqn\\.")
		// host does exist, update it if necessary
		if len(hostInitiators) != 0 && !stringSlicesEqual(hostInitiators, NQNs) {
			log.Infof("updating host: %s initiators to: %s", nodeName, NQNs)
			if _, err := s.retryableUpdateHostInitiators(ctx, array, host, NQNs, pmaxClient); err != nil {
				return host, err
			}
		}
	}
	return host, nil
}

// retryableCreateHost
func (s *service) retryableCreateHost(ctx context.Context, array string, nodeName string, hostInitiators []string, _ *types.HostFlags, pmaxClient pmax.Pmax) (*types.Host, error) {
	var err error
	var host *types.Host
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		host, err = pmaxClient.CreateHost(ctx, array, nodeName, hostInitiators, nil)
		if err != nil {
			// Retry on this error
			if strings.Contains(err.Error(), "is not in the format of a valid NQN:HostID") {
				hostInitiators, err = s.updateNQNWithHostID(ctx, array, hostInitiators, pmaxClient)
				if err != nil {
					log.Debug(fmt.Sprintf("failed to update host nqn; retrying..."))
				}
			}
			log.Debug(fmt.Sprintf("failed to create Host; retrying..."))
			// #nosec G115
			time.Sleep(time.Second << uint(tries)) // incremental back-off
			continue
		}
		break
	}
	if err != nil {
		return &types.Host{}, fmt.Errorf("Unable to create Host: %s", err)
	}
	return host, nil
}

// retryableUpdateHostInitiators wraps UpdateHostInitiators in a retry loop
func (s *service) retryableUpdateHostInitiators(ctx context.Context, array string, host *types.Host, initiators []string, pmaxClient pmax.Pmax) (*types.Host, error) {
	var err error
	var updatedHost *types.Host
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		updatedHost, err = pmaxClient.UpdateHostInitiators(ctx, array, host, initiators)
		if err != nil {
			// Retry on this error
			log.Debug(fmt.Sprintf("failed to update Host; retrying..."))
			// #nosec G115
			time.Sleep(time.Second << uint(tries)) // incremental back-off
			continue
		}
		host = updatedHost
		break
	}
	if err != nil {
		return &types.Host{}, fmt.Errorf("Unable to update Host: %v", err)
	}
	return updatedHost, nil
}

// retryableGetSymmetrixIDList returns the list of arrays
func (s *service) retryableGetSymmetrixIDList() (*types.SymmetrixIDList, error) {
	/*var arrays *types.SymmetrixIDList
	var err error
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		arrays, err = pmaxClient.GetSymmetrixIDList()
		if err != nil {
			// Retry on this error
			log.Error("failed to retrieve list of arrays; retrying...")
			time.Sleep(time.Second << uint(tries)) // incremental back-off
			continue
		}
		break
	}
	if err != nil {
		return &types.SymmetrixIDList{}, fmt.Errorf("Unable to retrieve Array List, timed out")
	}*/
	return &types.SymmetrixIDList{
		SymmetrixIDs: s.opts.ManagedArrays,
	}, nil
}

// NodeExpandVolume helps extending a volume size on a node
func (s *service) NodeExpandVolume(
	ctx context.Context,
	req *csi.NodeExpandVolumeRequest) (
	*csi.NodeExpandVolumeResponse, error,
) {
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
		log.Error("Volume path required")
		return nil, status.Error(codes.InvalidArgument,
			"Volume path required")
	}

	id := req.GetVolumeId()
	_, symID, _, _, _, err := s.parseCsiID(id)
	if err != nil {
		log.Errorf("Invalid volumeid: %s", id)
		return nil, status.Errorf(codes.InvalidArgument, "Invalid volume id: %s", id)
	}
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}
	// Check if it s a file System
	if accTypeIsNFS([]*csi.VolumeCapability{req.GetVolumeCapability()}) {
		log.Debug("file system is expanded, nothing to do on node...")
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
	replace := CSIPrefix + "-" + s.getClusterPrefix() + "-"
	volName := strings.Replace(vol.VolumeIdentifier, replace, "", 1)
	// remove the namespace from the volName as the mount paths will not have it
	volName = strings.Join(strings.Split(volName, "-")[:2], "-")

	// Locate and fetch all (multipath/regular) mounted paths using this volume
	devMnt, err := gofsutil.GetMountInfoFromDevice(ctx, volName)
	if err != nil {
		var devName string
		// No mounts were found. Perhaps it is a raw block device, which would not be mounted.
		deviceNames, _ := gofsutil.GetSysBlockDevicesForVolumeWWN(context.Background(), volumeWWN)
		if len(deviceNames) > 0 {
			for _, deviceName := range deviceNames {
				devicePath := sysBlock + "/" + deviceName
				log.Infof("Rescanning unmounted (raw block) device %s to expand size", deviceName)
				err = gofsutil.DeviceRescan(context.Background(), devicePath)
				if err != nil {
					log.Errorf("Failed to rescan device (%s) with error (%s)", devicePath, err.Error())
					return nil, status.Error(codes.Internal, err.Error())
				}
				devName = deviceName
			}
			mpathDev, err := gofsutil.GetMpathNameFromDevice(ctx, devName)
			if err != nil {
				log.Errorf("Failed to fetch mpath name for device (%s) with error (%s)", devName, err.Error())
				return nil, status.Error(codes.Internal, err.Error())
			}
			if mpathDev != "" {
				err = gofsutil.ResizeMultipath(context.Background(), mpathDev)
				if err != nil {
					log.Errorf("Failed to resize filesystem: device  (%s) with error (%s)", mpathDev, err.Error())
					return nil, status.Error(codes.Internal, err.Error())
				}
			}
			return &csi.NodeExpandVolumeResponse{}, nil
		}
		log.Errorf("Failed to find mount info for (%s) with error (%s)", volName, err.Error())
		return nil, status.Error(codes.Internal,
			fmt.Sprintf("Failed to find mount info for (%s) with error (%s)", volName, err.Error()))
	}
	log.Infof("Mount info for volume %s: %+v", volName, devMnt)

	size := req.GetCapacityRange().GetRequiredBytes()

	f := log.Fields{
		"CSIRequestID": reqID,
		"VolumeName":   volName,
		"VolumePath":   volumePath,
		"Size":         size,
		"VolumeWWN":    volumeWWN,
	}
	log.WithFields(f).Info("Calling resize the file system")

	// Rescan the scsi devices for the volume expanded on the array
	if !s.useNVMeTCP {
		for _, device := range devMnt.DeviceNames {
			devicePath := sysBlock + "/" + device
			err = gofsutil.DeviceRescan(context.Background(), devicePath)
			if err != nil {
				log.Errorf("Failed to rescan device (%s) with error (%s)", devicePath, err.Error())
				return nil, status.Error(codes.Internal, err.Error())
			}
		}
	}

	// Expand the filesystem with the actual expanded volume size.
	if devMnt.MPathName != "" {
		err = gofsutil.ResizeMultipath(context.Background(), devMnt.MPathName)
		if err != nil {
			log.Errorf("Failed to resize filesystem: device  (%s) with error (%s)", devMnt.MountPoint, err.Error())
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
		log.Errorf("Failed to fetch filesystem for volume  (%s) with error (%s)", devMnt.MountPoint, err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	log.Infof("Found %s filesystem mounted on volume %s", fsType, devMnt.MountPoint)

	// Resize the filesystem
	err = gofsutil.ResizeFS(context.Background(), devMnt.MountPoint, devicePath, devMnt.PPathName, devMnt.MPathName, fsType)
	if err != nil {
		log.Errorf("Failed to resize filesystem: mountpoint (%s) device (%s) with error (%s)",
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
		log.Infof("Found targets %v in cache", targets)
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
		log.Infof("Updated targets %v in cache", targets)
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
		log.Infof("Found targets %v in cache", targets)
	} else {
		// TODO NVME
		pmaxTargets, err := pmaxClient.GetNVMeTCPTargets(ctx, symID)
		if err != nil {
			return targets, status.Error(codes.Internal, fmt.Sprintf("Could not get iscsi target information: %s", err.Error()))
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
		log.Infof("Updated targets %v in cache", targets)
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
	var isFC bool
	var isNVMeTCP bool
	arrayTargets := strings.Split(targetIdentifiers, ",")
	// Remove the last empty element from the slice as there is a trailing ","
	if len(arrayTargets) == 1 && (arrayTargets[0] == targetIdentifiers) {
		log.Error("Failed to parse the target identifier string: " + targetIdentifiers)
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
	log.Infof("Array targets: %s", arrayTargets)
	return iscsiTargets, fcTargets, nvmeTargets, isFC, isNVMeTCP
}

func (s *service) getAndConfigureArrayNVMeTCPTargets(ctx context.Context, arrayTargets []string, symID string, pmaxClient pmax.Pmax) []NVMeTCPTargetInfo {
	nvmetcpTargets := make([]NVMeTCPTargetInfo, 0)
	allTargets, _ := s.getNVMeTCPTargets(ctx, symID, pmaxClient)
	cachedTargets, ok := symToMaskingViewTargets.Load(symID)
	if ok {
		targets := cachedTargets.([]maskingViewNVMeTargetInfo)
		// Check if the array targets are all present in the cache
		for _, arrayTarget := range arrayTargets {
			found := false
			for _, tgt := range targets {
				if arrayTarget == tgt.target.TargetNqn {
					nvmetcpTarget := NVMeTCPTargetInfo{
						Target: arrayTarget,
						Portal: tgt.target.Portal,
					}
					nvmetcpTargets = append(nvmetcpTargets, nvmetcpTarget)
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
						nvmetcpTarget := NVMeTCPTargetInfo{
							Target: arrayTarget,
							Portal: tgt.Portal,
						}
						nvmetcpTargets = append(nvmetcpTargets, nvmetcpTarget)
						isFound = true
					}
				}
				if !isFound {
					// This will be an extremely rare case
					// A new ISCSI target/portal IP has been configured
					// on the array and added to the Port Group
					// after the node driver cache information
					// Invalidate the cache. Return whatever targets we have found until now
					symToAllNVMeTCPTargets.Delete(symID)
					break
				}
			}
		}
		return nvmetcpTargets
	}
	// There is no cached information
	_, _, mvName := s.GetNVMETCPHostSGAndMVIDFromNodeID(s.opts.NodeName)
	// Get the Masking View Targets and configure CHAP if required
	// This call updates the cache as well
	goNVMETCPTargets, err := s.getAndConfigureMaskingViewTargetsNVMeTCP(ctx, symID, mvName, pmaxClient)
	if err != nil {
		log.Errorf("Failed to get and configure masking view targets. Error: %s", err.Error())
	}
	for _, arrayTarget := range arrayTargets {
		found := false
		for _, gonvmetcpTarget := range goNVMETCPTargets {
			if arrayTarget == gonvmetcpTarget.TargetNqn {
				nvmetcpTarget := NVMeTCPTargetInfo{
					Target: arrayTarget,
					Portal: gonvmetcpTarget.Portal,
				}
				nvmetcpTargets = append(nvmetcpTargets, nvmetcpTarget)
				found = true
			}
		}
		if !found {
			log.Errorf("Internal Error - Target: %s not found on array: %s", arrayTarget, symID)
		}
	}
	return nvmetcpTargets
}

func (s *service) getAndConfigureArrayISCSITargets(ctx context.Context, arrayTargets []string, symID string, pmaxClient pmax.Pmax) []ISCSITargetInfo {
	iscsiTargets := make([]ISCSITargetInfo, 0)
	allTargets, _ := s.getISCSITargets(ctx, symID, pmaxClient)
	IQNs, err := s.iscsiClient.GetInitiators("")
	if err != nil {
		log.Errorf("Failed to fetch initiators for the host. Error: %s", err.Error())
		return iscsiTargets
	}
	cachedTargets, ok := symToMaskingViewTargets.Load(symID)
	if ok {
		targets := cachedTargets.([]maskingViewTargetInfo)
		// Enable CHAP if required
		err = s.setCHAPCredentials(symID, targets, IQNs)
		if err != nil {
			// Log the error and continue
			log.Errorf("Failed to set CHAP credentials for targets: %v. Error: %s", targets, err.Error())
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
			found := false
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
		return iscsiTargets
	}
	// There is no cached information
	_, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
	// Get the Masking View Targets and configure CHAP if required
	// This call updates the cache as well
	goISCSITargets, err := s.getAndConfigureMaskingViewTargets(ctx, symID, mvName, IQNs, pmaxClient)
	if err != nil {
		log.Errorf("Failed to get and configure masking view targets. Error: %s", err.Error())
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
			log.Errorf("Internal Error - Target: %s not found on array: %s", arrayTarget, symID)
		}
	}
	return iscsiTargets
}

// writeWWNFile writes a volume's WWN to a file copy on the node
func (s *service) writeWWNFile(id, volumeWWN string) error {
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	err := ioutil.WriteFile(wwnFileName, []byte(volumeWWN), 0o644) // #nosec G306
	if err != nil {
		return status.Errorf(codes.Internal, "Could not read WWN file: %s", wwnFileName)
	}
	return nil
}

// readWWNFile reads the WWN from a file copy on the node
func (s *service) readWWNFile(id string) (string, error) {
	// READ volume WWN
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	wwnBytes, err := ioutil.ReadFile(wwnFileName) // #nosec G304
	if err != nil {
		return "", status.Errorf(codes.Internal, "Could not read WWN file: %s", wwnFileName)
	}
	volumeWWN := string(wwnBytes)
	return volumeWWN, nil
}

// removeWWNFile removes the WWN file from the node local disk
func (s *service) removeWWNFile(id string) {
	wwnFileName := fmt.Sprintf("%s/%s.wwn", s.privDir, id)
	os.Remove(wwnFileName) // #nosec G20
}
