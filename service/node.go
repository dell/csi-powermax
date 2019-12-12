package service

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"os"
	"strings"
	"sync"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	csictx "github.com/rexray/gocsi/context"
	log "github.com/sirupsen/logrus"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	types "github.com/dell/csi-powermax/pmax/types/v90"
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
)

func (s *service) NodeStageVolume(
	ctx context.Context,
	req *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse, error) {

	// Probe the node if required and make sure startup called
	err := s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}

	privTgt := req.GetStagingTargetPath()
	if privTgt == "" {
		return nil, status.Error(codes.InvalidArgument, "Target Path is required")
	}

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	// Parse the CSI VolumeId and validate against the volume
	symID, devID, vol, err := s.GetVolumeByID(id)
	if err != nil {
		// If the volume isn't found, we cannot stage it
		return nil, err
	}
	volumeWWN := vol.WWN

	// Get publishContext
	publishContext := req.GetPublishContext()
	volumeLUNAddress := publishContext[PublishContextLUNAddress]
	targetIdentifiers := publishContext[PortIdentifiers]

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

	// scan for the symlink and device path
	symlinkPath, devPath, err := s.nodeScanForDeviceSymlinkPath(volumeWWN, volumeLUNAddress, targetIdentifiers, reqID, symID, f)
	if err != nil {
		return nil, err
	}
	// Take some time to reverify in case other paths discovered (e.g. multipath)
	time.Sleep(nodePublishSleepTime)
	_, newDevPath, err := gofsutil.WWNToDevicePathX(context.Background(), volumeWWN)
	if err == nil && newDevPath != "" && newDevPath != devPath {
		log.Printf("devPath updated from %s to %s", devPath, newDevPath)
		devPath = newDevPath
	}
	log.WithFields(f).WithField("symlinkPath", symlinkPath).WithField("devPath", devPath).Info("NodeStageVolume completed")
	return &csi.NodeStageVolumeResponse{}, nil
}

func (s *service) NodeUnstageVolume(
	ctx context.Context,
	req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse, error) {

	// Probe the node if required and make sure startup called
	err := s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}

	// Remove the staging directory.
	stageTgt := req.GetStagingTargetPath()
	if stageTgt == "" {
		return nil, status.Error(codes.InvalidArgument, "A Staging Target argument is required")
	}
	err = gofsutil.Unmount(context.Background(), stageTgt)
	if err != nil {
		log.Info("NodeUnstageVolume error unmount stageTgt: " + err.Error())
	}
	removeWithRetry(stageTgt)

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	// Parse the CSI VolumeId and validate against the volume
	symID, devID, vol, err := s.GetVolumeByID(id)
	if err != nil {
		// If the volume isn't found, or we fail to validate the name/id, k8s will retry NodeUnstage forever so...
		// Make it stop...
		if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
			return &csi.NodeUnstageVolumeResponse{}, nil
		}
		return nil, err
	}
	volumeWWN := vol.WWN

	f := log.Fields{
		"CSIRequestID": reqID,
		"DeviceID":     devID,
		"ID":           req.VolumeId,
		"PrivTgt":      stageTgt,
		"SymmetrixID":  symID,
		"WWN":          volumeWWN,
	}
	log.WithFields(f).Info("NodeUnstageVolume")

	err = s.nodeDeleteBlockDevices(volumeWWN, reqID)
	if err != nil {
		return nil, err
	}

	// Remove the mount private directory if present, and the directory
	privTgt := getPrivateMountPoint(s.privDir, id)
	err = removeWithRetry(privTgt)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}

	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublish volume handles the CSI request to publish a volume to a target directory.
func (s *service) NodePublishVolume(
	ctx context.Context,
	req *csi.NodePublishVolumeRequest) (
	*csi.NodePublishVolumeResponse, error) {

	var reqID string
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	// Probe the node if required and make sure startup called
	err := s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	// Parse the CSI VolumeId and validate against the volume
	symID, devID, _, err := s.GetVolumeByID(id)
	if err != nil {
		return nil, err
	}

	// Get volumeContext
	volumeContext := req.GetVolumeContext()
	if volumeContext != nil {
		log.Printf("VolumeContext:")
		for key, value := range volumeContext {
			log.Printf("    [%s]=%s", key, value)
		}
	}

	// Get publishContext
	publishContext := req.GetPublishContext()
	deviceWWN := publishContext[PublishContextDeviceWWN]
	volumeLUNAddress := publishContext[PublishContextLUNAddress]
	if deviceWWN == "" {
		log.Error("Device WWN required to be in PublishContext")
		return nil, status.Error(codes.InvalidArgument, "Device WWN required to be in PublishContext")
	}
	targetIdentifiers := publishContext[PortIdentifiers]

	log.WithField("CSIRequestID", reqID).Printf("node publishing volume: %s lun: %s", deviceWWN, volumeLUNAddress)

	var symlinkPath string
	var devicePath string
	f := log.Fields{
		"CSIRequestID":      reqID,
		"DeviceID":          devID,
		"DevicePath":        devicePath,
		"ID":                req.VolumeId,
		"Name":              volumeContext["Name"],
		"SymlinkPath":       symlinkPath,
		"SymmetrixID":       symID,
		"TargetIdentifiers": targetIdentifiers,
		"WWN":               deviceWWN,
	}

	symlinkPath, devicePath, err = s.nodeScanForDeviceSymlinkPath(deviceWWN, volumeLUNAddress, targetIdentifiers, reqID, symID, f)
	if err != nil {
		return nil, err
	}

	f = log.Fields{
		"CSIRequestID":      reqID,
		"DevicePath":        devicePath,
		"ID":                req.VolumeId,
		"Name":              volumeContext["Name"],
		"WWN":               deviceWWN,
		"PrivateDir":        s.privDir,
		"SymlinkPath":       symlinkPath,
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
	*csi.NodeUnpublishVolumeResponse, error) {

	var reqID string
	var err error
	headers, ok := metadata.FromIncomingContext(ctx)
	if ok {
		reqID = headers["csi.requestid"][0]
	}

	// Get the target path
	target := req.GetTargetPath()
	if target == "" {
		log.Error("target path required")
		return nil, status.Error(codes.InvalidArgument,
			"target path required")
	}

	// Probe the node if required and make sure startup called
	err = s.nodeProbe(ctx)
	if err != nil {
		log.Error("nodeProbe failed with error :" + err.Error())
		return nil, err
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

	log.Printf("targetMount: %#v", targetMount)
	devicePath := targetMount.Device
	if devicePath == "devtmpfs" || devicePath == "" {
		devicePath = targetMount.Source
	}

	// Get the VolumeID and parse it
	id := req.GetVolumeId()

	// Parse the CSI VolumeId and validate against the volume
	symID, devID, vol, err := s.GetVolumeByID(id)
	if err != nil {
		// The spec says we should return NotFound, but kubelet will
		// call this repeatedly if we return it NotFound.
		// Return good response.
		if strings.Contains(err.Error(), notFound) || strings.Contains(err.Error(), failedToValidateVolumeNameAndID) {
			return &csi.NodeUnpublishVolumeResponse{}, nil
		}
		return nil, err
	}

	volumeWWN := vol.WWN
	if volumeWWN == "" {
		log.Error(fmt.Sprintf("Volume has no WWN sym %s dev %s", symID, devID))
		return nil, status.Error(codes.Internal, fmt.Sprintf("Volume has no WWN sym %s dev %s", symID, devID))
	}

	f := log.Fields{
		"CSIRequestID": reqID,
		"DeviceID":     devID,
		"DevicePath":   devicePath,
		"ID":           id,
		"PrivateDir":   s.privDir,
		"SymmetrixID":  symID,
		"TargetPath":   req.GetTargetPath(),
		"WWN":          volumeWWN,
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

	log.Printf("lastUmounted %v\n", lastUnmounted)
	if lastUnmounted {
		removeWithRetry(target)
		err = s.nodeDeleteBlockDevices(volumeWWN, reqID)
		if err != nil {
			return nil, err
		}
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
		log.WithFields(f).Printf("Device mounts still present: %#v", mnts)
	} else {
		if err := s.nodeDeleteBlockDevices(volumeWWN, reqID); err != nil {
			return nil, err
		}
	}
	removeWithRetry(target)
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
			log.Printf("matching targetMount %s target %s", target, mount.Path)
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

	err := s.createPowerMaxClient()
	if err != nil {
		return err
	}

	// make sure we are logged into all arrays
	if s.nodeIsInitialized {
		// nothing to do for FC
		if s.opts.TransportProtocol != FcTransportProtocol {
			_ = s.ensureLoggedIntoEveryArray(false)
		}
	}
	return nil
}

func (s *service) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error) {

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: []*csi.NodeServiceCapability{
			{Type: &csi.NodeServiceCapability_Rpc{
				Rpc: &csi.NodeServiceCapability_RPC{
					Type: csi.NodeServiceCapability_RPC_STAGE_UNSTAGE_VOLUME,
				},
			},
			},
		},
	}, nil
}

// Minimal version of NodeGetInfo. Returns the NodeId
// MaxVolumesPerNode (optional) is left as 0 which means unlimited, and AccessibleTopology is left nil.
func (s *service) NodeGetInfo(
	ctx context.Context,
	req *csi.NodeGetInfoRequest) (
	*csi.NodeGetInfoResponse, error) {

	// Get the Node ID
	if s.opts.NodeName == "" {
		log.Error("Unable to get Node Name from the environment")
		return nil, status.Error(codes.FailedPrecondition,
			"Unable to get Node Name from the environment")
	}
	return &csi.NodeGetInfoResponse{NodeId: s.opts.NodeName}, nil
}

func (s *service) NodeGetVolumeStats(
	ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// nodeStartup performs a few necessary functions for the nodes to function properly
// - validates that at least one iSCSI initiator is defined
// - validates that a connection to Unisphere exists
// - invokes nodeHostSetup in a thread
//
// returns an error if unable to perform node startup tasks without error
func (s *service) nodeStartup() error {

	if s.nodeIsInitialized {
		return nil
	}

	// Copy the multipath.conf file from /noderoot/etc/multipath.conf (EnvISCSIChroot)to /etc/multipath.conf if present
	//iscsiChroot, _ := csictx.LookupEnv(context.Background(), EnvISCSIChroot)
	//copyMultipathConfigFile(iscsiChroot, "")

	// make sure we have a connection to Unisphere
	if s.adminClient == nil {
		return fmt.Errorf("There is no Unisphere connection")
	}

	portWWNs := make([]string, 0)
	IQNs := make([]string, 0)
	var err error

	// Get fibrechannel initiators
	portWWNs, err = gofsutil.GetFCHostPortWWNs(context.Background())
	if err != nil {
		log.Error("nodeStartup could not GetFCHostPortWWNs")
	}

	// Get iscsi initiators.
	IQNs, err = s.iscsiClient.GetInitiators("")
	if err != nil {
		log.Error("nodeStartup could not GetInitiatorIQNs")
	}

	log.Printf("TransportProtocol %s FC portWWNs: %s ... IQNs: %s\n", s.opts.TransportProtocol, portWWNs, IQNs)
	// The driver needs at least one FC or iSCSI initiator to be defined
	if len(portWWNs) == 0 && len(IQNs) == 0 {
		return fmt.Errorf("No FC or iSCSI initiators were found and at least 1 is required")
	}

	arrays, err := s.retryableGetSymmetrixIDList()
	if err != nil {
		log.Error("Failed to fetch array list. Continuing without initializing node")
		return err
	}
	symmetrixIDs := arrays.SymmetrixIDs
	log.Debug(fmt.Sprintf("GetSymmetrixIDList returned: %v", symmetrixIDs))

	go s.nodeHostSetup(portWWNs, IQNs, symmetrixIDs)

	return err
}

// verifyInitiatorsNotInADiffernetHost verifies that a set of node initiators are not in a different host than expected.
// These can be either FC initiators (hex numbers) or iSCSI initiators (starting with iqn.)
// It returns the number of initiators for the host that were found.
// Do not mix both FC and iSCSI initiators in a single call.
func (s *service) verifyInitiatorsNotInADifferentHost(symID string, nodeInitiators []string, hostID string) (int, error) {
	initList, err := s.adminClient.GetInitiatorList(symID, "", false, false)
	if err != nil {
		log.Warning("Failed to fetch initiator list for the SYM :" + symID)
		return 0, err
	}
	var nValidInitiators int
	for _, nodeInitiator := range nodeInitiators {
		if strings.HasPrefix(nodeInitiator, "0x") {
			nodeInitiator = strings.Replace(nodeInitiator, "0x", "", 1)
		}
		for _, initiatorID := range initList.InitiatorIDs {
			if initiatorID == nodeInitiator || strings.HasSuffix(initiatorID, nodeInitiator) {
				fmt.Printf("Checking initiator %s against host %s\n", initiatorID, hostID)
				initiator, err := s.adminClient.GetInitiatorByID(symID, initiatorID)
				if err != nil {
					log.Warning("Failed to fetch initiator details for initiator: " + initiatorID)
					continue
				}
				if (initiator.HostID != "") && (initiator.HostID != hostID) {
					errormsg := fmt.Sprintf("initiator: %s is already a part of a different host: %s on: %s",
						initiatorID, initiator.HostID, symID)
					log.Error(errormsg)
					return 0, fmt.Errorf(errormsg)
				}
				fmt.Printf("valid initiator: %s\n", initiatorID)
				nValidInitiators++
			}
		}
	}
	return nValidInitiators, nil
}

// nodeHostSeup performs a few necessary functions for the nodes to function properly
// For FC:
// - A Host exists
// For ISCSI:
// - a Host exists within PowerMax, to identify this node
// - The Host contains the discovered iSCSI initiators
// - performs an iSCSI login
func (s *service) nodeHostSetup(portWWNs []string, IQNs []string, symmetrixIDs []string) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	log.Info("**************************\nnodeHostSetup executing...\n*******************************")
	defer log.Info("**************************\nnodeHostSetup completed...\n*******************************")

	// we need to randomize a time before starting the interaction with unisphere
	// in order to reduce the concurrent workload on the system

	// determine a random delay period (in
	period := rand.Int() % maximumStartupDelay
	// sleep ...
	log.Printf("Waiting for %d seconds", period)
	time.Sleep(time.Duration(period) * time.Second)

	// See if it's viable to use FC and/or ISCSI
	hostIDIscsi, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
	hostIDFC, _, _ := s.GetFCHostSGAndMVIDFromNodeID(s.opts.NodeName)
	var useFC bool
	var useIscsi bool

	// Loop through the symmetrix, looking for existing initiators
	for _, symID := range symmetrixIDs {
		validFC, err := s.verifyInitiatorsNotInADifferentHost(symID, portWWNs, hostIDFC)
		if err != nil {
			log.Error("Could not validate FC initiators" + err.Error())
		}
		fmt.Printf("valid FC initiators: %d\n", validFC)
		if validFC > 0 && (s.opts.TransportProtocol == "" || s.opts.TransportProtocol == FcTransportProtocol) {
			// We do have to have pre-existing initiators that were zoned for FC
			useFC = true
		}
		validIscsi, err := s.verifyInitiatorsNotInADifferentHost(symID, IQNs, hostIDIscsi)
		if err != nil {
			log.Error("Could not validate iSCSI initiators" + err.Error())
		} else if s.opts.TransportProtocol == "" || s.opts.TransportProtocol == IscsiTransportProtocol {
			// We do not have to have pre-existing initiators to use Iscsi (we can create them)
			useIscsi = true
		}
		fmt.Printf("valid (existing) iSCSI initiators (must be manually created): %d\n", validIscsi)

		if !useFC && !useIscsi {
			log.Error("No valid initiators- could not initialize FC or iSCSI")
			return err
		}

		if useFC {
			s.setupArrayForFC(symID, portWWNs)
		} else if useIscsi {
			s.setupArrayForIscsi(symID, IQNs)
		}
	}

	s.nodeIsInitialized = true
	return nil
}

func (s *service) setupArrayForFC(array string, portWWNs []string) error {
	hostName, _, mvName := s.GetFCHostSGAndMVIDFromNodeID(s.opts.NodeName)
	log.Printf("setting up array %s for Fibrechannel, host name: %s masking view: %s", array, hostName, mvName)

	_, err := s.createOrUpdateFCHost(array, hostName, portWWNs)
	return err
}

// setupArrayForIscsi is called to set up a node for iscsi operation.
func (s *service) setupArrayForIscsi(array string, IQNs []string) error {
	hostName, _, mvName := s.GetISCSIHostSGAndMVIDFromNodeID(s.opts.NodeName)
	log.Printf("setting up array %s for Iscsi, host name: %s masking view ID: %s", array, hostName, mvName)

	// Create or update the IscsiHost and Initiators
	_, err := s.createOrUpdateIscsiHost(array, hostName, IQNs)
	if err != nil {
		log.Error(err.Error())
		return err
	}

	// Check the masking view
	view, err := s.adminClient.GetMaskingViewByID(array, mvName)
	if err != nil {
		// masking view does not exist, not an error but no need to login
		log.Debugf("Masking View %s does not exist for array %s, skipping login", mvName, array)
	} else {
		// masking view exists, we need to log into some targets
		targets, err := s.getIscsiTargetsForMaskingView(array, view)
		if err != nil {
			log.Debugf(err.Error())
			return err
		}
		for _, tgt := range targets {
			// discover the targets but do not login
			log.Debugf("Discovering iSCSI targets on %s", tgt.Portal)
			_, err = s.iscsiClient.DiscoverTargets(tgt.Portal, true)
			s.iscsiClient.DiscoverTargets(tgt.Portal, false)
		}
	}
	return nil
}

func (s *service) getIscsiTargetsForMaskingView(array string, view *types.MaskingView) ([]goiscsi.ISCSITarget, error) {
	if array == "" {
		return []goiscsi.ISCSITarget{}, fmt.Errorf("No array specified")
	}
	if view.PortGroupID == "" {
		return []goiscsi.ISCSITarget{}, fmt.Errorf("Masking view contains no PortGroupID")
	}
	// get the PortGroup in the masking view
	portGroup, err := s.adminClient.GetPortGroupByID(array, view.PortGroupID)
	if err != nil {
		return []goiscsi.ISCSITarget{}, err
	}
	targets := make([]goiscsi.ISCSITarget, 0)
	// for each Port
	for _, portKey := range portGroup.SymmetrixPortKey {
		pID := strings.Split(portKey.PortID, ":")[1]
		port, err := s.adminClient.GetPort(array, portKey.DirectorID, pID)
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
			targets = append(targets, t)
		}
	}
	return targets, nil
}

func (s *service) ensureLoggedIntoEveryArray(skipLogin bool) error {
	arrays := &types.SymmetrixIDList{}
	var err error

	// Get the list of arrays
	arrays, err = s.retryableGetSymmetrixIDList()
	if err != nil {
		return err
	}

	// for each array known to unisphere, ensure we have performed an iSCSI login at least once
	for _, array := range arrays.SymmetrixIDs {
		deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
		for tries := 0; time.Now().Before(deadline); tries++ {
			var addresses []string
			addresses, err = s.adminClient.GetListOfTargetAddresses(array)
			if err != nil {
				return err
			}
			if s.loggedInArrays[array] {
				// we have already logged into this array
				break
			}
			for _, addr := range addresses {
				if skipLogin == false {
					_, err = s.iscsiClient.DiscoverTargets(addr, true)
				} else {
					log.Debug("Skipping iSCSI login due to user request")
					err = nil
				}
				if err != nil {
					// Retry on this error
					log.Error(fmt.Sprintf("Failed to execute iSCSI login to target: %s; retrying...", addr))
					time.Sleep(time.Second << uint(tries)) // incremental back-off
					continue
				} else {
					// set the flag saying that we have logged into the array
					log.Debug(fmt.Sprintf("loggedInArrays: %v", s.loggedInArrays))
					s.loggedInArrays[array] = true
				}
			}
			break
		}
		if err != nil {
			return fmt.Errorf("Unable to perform iSCSI discovery and login, timed out")
		}
	}
	return nil
}

func (s *service) createOrUpdateFCHost(array string, nodeName string, portWWNs []string) (*types.Host, error) {
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
	initList, err := s.adminClient.GetInitiatorList(array, "", false, false)
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
	log.Printf("hostInitiators: %s\n", hostInitiators)

	// See if the host is present
	host, err := s.adminClient.GetHostByID(array, nodeName)
	log.Debug(fmt.Sprintf("GetHostById returned: %v, %v", host, err))
	if err != nil {
		// host does not exist, create it
		log.Printf(fmt.Sprintf("Array %s FC Host %s does not exist. Creating it.", array, nodeName))
		host, err = s.retryableCreateHost(array, nodeName, hostInitiators, nil)
		if err != nil {
			return host, err
		}
	} else {
		// make sure we don't update an iscsi host
		arrayInitiators := stringSliceRegexMatcher(host.Initiators, "(0x)*[0-9a-fA-F]")
		stringSliceRegexReplace(arrayInitiators, "^.*:.*:", "")
		// host does exist, update it if necessary
		if len(arrayInitiators) != 0 && !stringSlicesEqual(arrayInitiators, hostInitiators) {
			log.Printf("updating host: %s initiators to: %s", nodeName, hostInitiators)
			_, err := s.retryableUpdateHostInitiators(array, host, portWWNs)
			if err != nil {
				return host, err
			}
		}
	}
	return host, nil
}

func (s *service) createOrUpdateIscsiHost(array string, nodeName string, IQNs []string) (*types.Host, error) {
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

	host, err := s.adminClient.GetHostByID(array, nodeName)
	log.Printf(fmt.Sprintf("GetHostById returned: %v, %v", host, err))

	if err != nil {
		// host does not exist, create it
		log.Printf(fmt.Sprintf("ISCSI Host %s does not exist. Creating it.", nodeName))
		host, err = s.retryableCreateHost(array, nodeName, IQNs, nil)
		if err != nil {
			return &types.Host{}, fmt.Errorf("Unable to create Host: %v", err)
		}
	} else {
		// Make sure we don't update an FC host
		hostInitiators := stringSliceRegexMatcher(host.Initiators, "^iqn\\.")
		// host does exist, update it if necessary
		if len(hostInitiators) != 0 && !stringSlicesEqual(hostInitiators, IQNs) {
			log.Printf("updating host: %s initiators to: %s", nodeName, IQNs)
			if _, err := s.retryableUpdateHostInitiators(array, host, IQNs); err != nil {
				return host, err
			}
		}
	}

	return host, nil
}

// retryableCreateHost
func (s *service) retryableCreateHost(array string, nodeName string, hostInitiators []string, flags *types.HostFlags) (*types.Host, error) {
	var err error
	var host *types.Host
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		host, err = s.adminClient.CreateHost(array, nodeName, hostInitiators, nil)
		if err != nil {
			// Retry on this error
			log.Debug(fmt.Sprintf("failed to create Host; retrying..."))
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
func (s *service) retryableUpdateHostInitiators(array string, host *types.Host, initiators []string) (*types.Host, error) {
	var err error
	var updatedHost *types.Host
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		updatedHost, err = s.adminClient.UpdateHostInitiators(array, host, initiators)
		if err != nil {
			// Retry on this error
			log.Debug(fmt.Sprintf("failed to update Host; retrying..."))
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
	var arrays *types.SymmetrixIDList
	var err error
	deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
	for tries := 0; time.Now().Before(deadline); tries++ {
		arrays, err = s.adminClient.GetSymmetrixIDList()
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
	}
	return arrays, nil
}

func (s *service) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	return nil, status.Error(codes.Unimplemented, "")
}

// Given a volume WWN, delete all the associated block devices (including multipath) on that node.
// Caches for nodeScanForDeviceSymlinkPath
var symToPortalIPsMap sync.Map

// Gets the iscsi target iqn values that can be used for rescanning.
func (s *service) getPortalIPs(symID string) ([]string, error) {
	var portalIPs []string
	var ips interface{}
	var ok bool
	var err error

	ips, ok = symToPortalIPsMap.Load(symID)
	if ok {
		portalIPs = ips.([]string)
		log.Printf("hit portalIPs %s", portalIPs)
	} else {
		portalIPs, err = s.adminClient.GetListOfTargetAddresses(symID)
		if err != nil {
			return portalIPs, status.Error(codes.Internal, fmt.Sprintf("Could not get iscsi portalIPs: %s", err.Error()))
		}
		symToPortalIPsMap.Store(symID, portalIPs)
		log.Printf("miss portalIPs %s", portalIPs)
	}

	return portalIPs, nil
}

// nodeScanForDeviceSymlinkPath scans SCSI devices to get the device symlink and device paths.
// devinceWWN is the volume's WWN field
// volumeLUNAddress is the LUN Address in the masking view connections list (optional; expect only one per volume)
// reqID is the CSI request ID
// symID is the symmetrix ID
// f is the logrus fields
func (s *service) nodeScanForDeviceSymlinkPath(deviceWWN, volumeLUNAddress, targetIdentifiers string, reqID string, symID string, f log.Fields) (string, string, error) {
	arrayTargets := strings.Split(targetIdentifiers, ",")
	retryForISCSI := true
	// Remove the last empty element from the slice as there is a trailing ","
	if len(arrayTargets) == 1 && (arrayTargets[0] == targetIdentifiers) {
		log.Error("Failed to parse the target identifier string: " + targetIdentifiers)
	} else if len(arrayTargets) > 1 {
		arrayTargets = arrayTargets[:len(arrayTargets)-1]
		if strings.Contains(arrayTargets[0], "0x") {
			retryForISCSI = false
		}
	}
	log.Printf("Array targets: %s", arrayTargets)

	// Determine if the device is visible, if not perform a simple rescan.
	symlinkPath, devicePath, err := gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
	for retry := 0; devicePath == "" && retry < 3; retry++ {
		err = gofsutil.RescanSCSIHost(context.Background(), arrayTargets, volumeLUNAddress)
		time.Sleep(nodePublishSleepTime)
		symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
	}
	// If not iSCSI, it is Fibrechannel
	if err != nil && !retryForISCSI {
		log.WithFields(f).Error("No Fibrechannel DevicePath found after multiple rescans... last resort is issue_lip and retry")
		maxRetries := 3
		for retry := 0; err != nil && retry < maxRetries; retry++ {
			err := gofsutil.IssueLIPToAllFCHosts(context.Background())
			if err != nil {
				log.Error(err.Error())
			}
			// Look for the device again
			time.Sleep(lipSleepTime)
			symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
		}
	}
	// If is iSCSI
	if err != nil && retryForISCSI {
		log.WithFields(f).Error("No iSCSI DevicePath found after multiple rescans... last resort is iscsi DiscoverTargets and PerformRescan")
		maxRetries := 2
		for retry := 0; err != nil && retry < maxRetries; retry++ {
			// Try running the iscsi discovery process again.
			var portalIPs []string
			portalIPs, err = s.getPortalIPs(symID)
			if err == nil {
				for _, portalIP := range portalIPs {
					log.Info(fmt.Sprintf("Performing IscsiDiscovery and login on portalIP: %s", portalIP))
					_, err = s.iscsiClient.DiscoverTargets(portalIP, true)
					if err != nil {
						log.Error("DiscoverTargets: " + err.Error())
					}
				}
				time.Sleep(nodePublishSleepTime)
				err = s.iscsiClient.PerformRescan()
				if err != nil {
					log.Error("RescanSCSIHost error: " + err.Error())
				}
				// Look for the device again
				time.Sleep(nodePublishSleepTime)
				symlinkPath, devicePath, err = gofsutil.WWNToDevicePathX(context.Background(), deviceWWN)
			} else {
				log.Printf("getPortalIPs returned %s %s", portalIPs, err)
			}
		}
	}
	if err != nil {
		if retryForISCSI {
			// delete the caches so will refresh since had an absolute failure
			symToPortalIPsMap.Delete(symID)
		}
		log.WithFields(f).Error("Unable to find device after multiple discovery attempts: " + err.Error())
		return "", "", status.Error(codes.NotFound, "Unable to find device after multiple discovery attempts: "+err.Error())
	}
	return symlinkPath, devicePath, nil
}

// nodeDeleteBlockDevices deletes the block devices associated with a volume WWN (including multipath) on that node.
// This should be done when the volume will no longer be used on the node.
func (s *service) nodeDeleteBlockDevices(volumeWWN, reqID string) error {
	var err error
	for i := 0; i < maxBlockDevicesPerWWN; i++ {
		// Wait for the next device to show up
		symlinkPath, devicePath, _ := gofsutil.WWNToDevicePathX(context.Background(), volumeWWN)
		if devicePath == "" {
			// All done, no more paths for WWN
			return nil
		}
		log.WithField("CSIRequestID", reqID).WithField("WWN", volumeWWN).WithField("SymlinkPath", symlinkPath).WithField("DevicePath", devicePath).
			Info("Removing block device")
		isMultipath := strings.HasPrefix(symlinkPath, gofsutil.MultipathDevDiskByIDPrefix)
		if isMultipath {
			var textBytes []byte
			iscsiChroot, _ := csictx.LookupEnv(context.Background(), EnvISCSIChroot)
			// List the current multipath status
			ctx := context.Background()
			// Attempt to flush the devicePath
			multipathMutex.Lock()
			textBytes, err = gofsutil.MultipathCommand(ctx, 10, iscsiChroot, "-f", devicePath)
			multipathMutex.Unlock()
			if textBytes != nil && len(textBytes) > 0 {
				log.WithField("CSIRequestID", reqID).Info(fmt.Sprintf("multipath -f %s: %s", devicePath, string(textBytes)))
			}
			if err != nil {
				log.WithField("CSIRequestID", reqID).Info(fmt.Sprintf("multipath flush error: %s: %s", devicePath, err.Error()))
			}
			time.Sleep(multipathSleepTime)
			continue
		}

		// RemoveBlockDevice is called in a goroutine in case it takes an extended period. It will return error status in channel errorChan.
		// The mutex protecting concurrent device deletions is held for a maximum of deviceDeletionTimeout.
		// After that time we check to see if we have received a response from the goroutine.
		// If there was no response, we return an error that we timed out.
		// Otherwise, we wait any additional necessary to have slept for the removeDeviceSleepTime.
		deviceDeleteMutex.Lock()
		startTime := time.Now()
		endTime := startTime.Add(removeDeviceSleepTime)
		errorChan := make(chan error, 1)
		go func(devicePath string, errorChan chan error) {
			err := gofsutil.RemoveBlockDevice(context.Background(), devicePath)
			errorChan <- err
		}(devicePath, errorChan)
		done := false
		for curTime := time.Now(); !done && curTime.Sub(startTime) < deviceDeletionTimeout; curTime = time.Now() {
			select {
			case err = <-errorChan:
				done = true
				log.Printf("device delete took %v", curTime.Sub(startTime))
				if err != nil {
					log.WithField("CSIRequestID", reqID).WithField("DevicePath", devicePath).Error(err)
				}
			default:
				time.Sleep(deviceDeletionPoll)
			}
		}
		deviceDeleteMutex.Unlock()
		if !done {
			return fmt.Errorf("Removing block device timed out after %v", deviceDeletionTimeout)
		}
		remainingTime := endTime.Sub(time.Now())
		if remainingTime > (100 * time.Millisecond) {
			time.Sleep(remainingTime)
		}
	}
	if err != nil {
		return err
	}
	// Do a linear scan.
	return linearScanToRemoveDevices(reqID, volumeWWN)
}

// linearScanToRemoveDevices does a linear scan through /sys/block for devices matching a volumeWWN, and attempts to remove them if any.
func linearScanToRemoveDevices(reqID, volumeWWN string) error {
	time.Sleep(removeDeviceSleepTime)
	devs, _ := gofsutil.GetSysBlockDevicesForVolumeWWN(context.Background(), volumeWWN)
	if len(devs) == 0 {
		return nil
	}
	log.Printf("volume devices wwn %s still to be deleted: %s", volumeWWN, devs)
	for _, dev := range devs {
		devicePath := "/dev/" + dev
		err := gofsutil.RemoveBlockDevice(context.Background(), devicePath)
		if err != nil {
			log.WithField("CSIRequestID", reqID).WithField("DevicePath", devicePath).Error(err)
		}
	}
	devs, _ = gofsutil.GetSysBlockDevicesForVolumeWWN(context.Background(), volumeWWN)
	if len(devs) > 0 {
		return status.Error(codes.Internal, fmt.Sprintf("volume WWN %s had %d block devices that weren't successfully deleted: %s", volumeWWN, len(devs), devs))
	}
	return nil
}

// copyMultipathConfig file copies the /etc/multipath.conf file from the nodeRoot chdir path to
// /etc/multipath.conf if testRoot is "". testRoot can be set for testing to copy somehwere else,
// but it should be empty ( "" ) for normal operation. nodeRoot is normally iscsiChroot env. variable.
func copyMultipathConfigFile(nodeRoot, testRoot string) error {
	var srcFile *os.File
	var dstFile *os.File
	var err error
	// Copy the multipath.conf file from /noderoot/etc/multipath.conf (EnvISCSIChroot)to /etc/multipath.conf if present
	srcFile, err = os.Open(nodeRoot + "/etc/multipath.conf")
	if err == nil {
		dstFile, err = os.Create(testRoot + "/etc/multipath.conf")
		if err != nil {
			log.Error("Could not open /etc/multipath.conf for writing")
		} else {
			written, _ := io.Copy(dstFile, srcFile)
			log.Printf("copied %d bytes to /etc/multipath.conf", written)
			dstFile.Close()
		}
		srcFile.Close()
	}
	return err
}
