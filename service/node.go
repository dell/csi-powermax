package service

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"
	log "github.com/sirupsen/logrus"

	// log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	types "github.com/dell/csi-powermax/pmax/types/v90"
)

var (
	maximumStartupDelay   = 30
	getMappedVolMaxRetry  = 20
	devDiskByIDPrefix     = "/dev/disk/by-id/wwn-0x"
	nodePublishSleepTime  = 5 * time.Second
	removeDeviceSleepTime = 1 * time.Second
	maxBlockDevicesPerWWN = 16
)

func (s *service) NodeStageVolume(
	ctx context.Context,
	req *csi.NodeStageVolumeRequest) (
	*csi.NodeStageVolumeResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")
}

func (s *service) NodeUnstageVolume(
	ctx context.Context,
	req *csi.NodeUnstageVolumeRequest) (
	*csi.NodeUnstageVolumeResponse, error) {

	return nil, status.Error(codes.Unimplemented, "")
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

	portalIPs, err := s.adminClient.GetListOfTargetAddresses(symID)
	if err != nil {
		return nil, status.Error(codes.Internal, fmt.Sprintf("Could not get iscsi portalIPs: %s", err.Error()))
	}
	noTargets := make([]string, 0)

	log.Printf("node publishing volume: %s lun: %s", deviceWWN, volumeLUNAddress)

	var devicePath string
	f := log.Fields{
		"CSIRequestID": reqID,
		"DeviceID":     devID,
		"DevicePath":   devicePath,
		"ID":           req.VolumeId,
		"Name":         volumeContext["Name"],
		"PortalIPs":    portalIPs,
		"SymmetrixID":  symID,
		"WWN":          deviceWWN,
	}

	// Determine if the device is visible, if not perform a simple rescan.
	err = fmt.Errorf("start the loop")
	for retry := 0; err != nil && retry < 2; retry++ {
		//err = s.PerformIscsiRescan()
		err = gofsutil.RescanSCSIHost(context.Background(), noTargets, volumeLUNAddress)
		//err = s.iscsiClient.PerformRescan()
		if err == nil {
			time.Sleep(nodePublishSleepTime)
			devicePath, err = gofsutil.WWNToDevicePath(context.Background(), deviceWWN)
		}
	}
	if err != nil {
		log.WithFields(f).Error("No DevicePath found after multiple rescans")
		maxRetries := 1

		for retry := 0; err != nil && retry < maxRetries; retry++ {
			// Try running the iscsi discovery process again.
			for _, portalIP := range portalIPs {
				log.Info(fmt.Sprintf("Performing IscsiDiscovery and login on portalIP: %s", portalIP))
				_, err = s.iscsiClient.DiscoverTargets(portalIP, true)
				if err != nil {
					log.Error("DiscoverTargets: " + err.Error())
				}
			}
			time.Sleep(nodePublishSleepTime)
			// Rescan again.
			//err := s.PerformIscsiRescan()
			//err = gofsutil.RescanSCSIHost(context.Background(), noTargets, volumeLUNAddress)
			err = s.iscsiClient.PerformRescan()
			if err != nil {
				log.Error("RescanSCSIHost error: " + err.Error())
			}
			// Look for the device again
			time.Sleep(nodePublishSleepTime)
			devicePath, err = gofsutil.WWNToDevicePath(context.Background(), deviceWWN)
		}
	}
	if err != nil {
		log.Error("Unable to find device after multiple discovery attempts: " + err.Error())
		return nil, status.Error(codes.NotFound, "Unable to find device after multiple discovery attempts: "+err.Error())
	}

	f = log.Fields{
		"CSIRequestID": reqID,
		"DevicePath":   devicePath,
		"ID":           req.VolumeId,
		"Name":         volumeContext["Name"],
		"WWN":          deviceWWN,
		"PrivateDir":   s.privDir,
		"TargetPath":   req.GetTargetPath(),
	}
	log.WithFields(f).Info("Calling publishVolume")
	if err := publishVolume(req, s.privDir, devDiskByIDPrefix+deviceWWN); err != nil {
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

	// Look through the mount table for the target path.
	var targetMount gofsutil.Info
	if targetMount, err = s.getTargetMount(target); err != nil {
		return nil, err
	}

	if targetMount.Device == "" {
		// Didn't find mount, so idempotently done.
		log.Debug(fmt.Sprintf("No mount entry for target, so assuming this is an idempotent call: %s", target))
		return &csi.NodeUnpublishVolumeResponse{}, nil
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
		return nil, err
	}

	volumeWWN := vol.WWN
	if volumeWWN == "" {
		log.Error(fmt.Sprintf("Volume has no WWN sym %s dev %s", symID, devID))
		return nil, status.Error(codes.Internal, fmt.Sprintf("Volume has no WWN sym %s dev %s", symID, devID))
	}

	f := log.Fields{
		"DeviceID":     devID,
		"DevicePath":   devicePath,
		"ID":           id,
		"PrivateDir":   s.privDir,
		"SymmetrixID":  symID,
		"TargetPath":   req.GetTargetPath(),
		"WWN":          volumeWWN,
		"CSIRequestID": reqID,
	}
	log.WithFields(f).Info("Calling unpublishVolume")

	var lastUnmounted bool
	if lastUnmounted, err = unpublishVolume(req, s.privDir, devicePath); err != nil {
		return nil, err
	}

	if devicePath != "" && lastUnmounted {
		// If this is the last unmount for this WWN, we want to remove
		// all the block devices which will remove also the corresponding
		// /dev/disk/by-id symlinks for that WWN. This is necessary so as
		// to not leave garbage that will interere in the future with rescans.
		// We remove /sys/block/{deviceName} by writing a 1 to
		// /sys/block{deviceName}/device/delete in RemoveBlockDevice().

		log.WithField("WWN", volumeWWN).WithField("DevicePath", devicePath).Info("Removing block device")
		gofsutil.RemoveBlockDevice(context.Background(), devicePath)

		// There may be other devices for the same WWN. These must also be removed
		// so they will not interfere in the future with other iscsi rescans/discoveries.
		for i := 0; i < maxBlockDevicesPerWWN; i++ {
			// Wait for the next device to show up
			time.Sleep(removeDeviceSleepTime)
			devicePath, _ := gofsutil.WWNToDevicePath(context.Background(), volumeWWN)
			if devicePath == "" {
				// All done, no more paths for WWN
				return &csi.NodeUnpublishVolumeResponse{}, nil
			}
			log.WithField("WWN", volumeWWN).WithField("DevicePath", devicePath).Info("Removing secondary block device")
			err = gofsutil.RemoveBlockDevice(context.Background(), devicePath)
			if err != nil {
				log.WithField("DevicePath", devicePath).Error(err)
			}
		}
		return nil, status.Error(codes.Internal,
			fmt.Sprintf("WWN %s had more than %d block devices or they weren't successfully deleted", volumeWWN, maxBlockDevicesPerWWN))
	}

	// Recheck to make sure the target is unmounted.
	log.WithFields(f).Info("Rechecking target mount is gone")
	if targetMount, err = s.getTargetMount(target); err != nil {
		return nil, err
	}
	if targetMount.Device != "" {
		log.WithFields(f).Error("Target mount still present... returning failure")
		return nil, status.Error(codes.Internal, "Target Mount still present")
	}

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
		_ = s.ensureLoggedIntoEveryArray(false)
	}

	return nil
}

func (s *service) NodeGetCapabilities(
	ctx context.Context,
	req *csi.NodeGetCapabilitiesRequest) (
	*csi.NodeGetCapabilitiesResponse, error) {

	return &csi.NodeGetCapabilitiesResponse{}, nil
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

	// make sure we have a connection to Unisphere
	if s.adminClient == nil {
		return fmt.Errorf("There is no Unisphere connection")
	}

	IQNs, err := s.iscsiClient.GetInitiators("")
	if err != nil {
		log.Error("nodeStartup could not GetInitiatorIQNs")
		return err
	}
	// The driver needs at least one iSCSI initiator to be defined
	if len(IQNs) == 0 {
		return fmt.Errorf("No iSCSI IQNs were found and, at least, 1 is required")
	}
	hostID, _, _ := s.GetHostSGAndMVIDFromNodeID(s.opts.NodeName)
	skipVerify := false
	symmetrixIDs := make([]string, 0)
	arrays, err := s.adminClient.GetSymmetrixIDList()
	if err != nil {
		log.Warning("Failed to fetch array list. Continuing")
		skipVerify = true
	} else {
		symmetrixIDs = arrays.SymmetrixIDs
	}
	if !skipVerify {
		for _, iqn := range IQNs {
			// Fetch the IQN detail and make sure it is not part of any other host
			for _, symID := range symmetrixIDs {
				initList, err := s.adminClient.GetInitiatorList(symID, iqn, true, true)
				if err != nil {
					log.Warning("Failed to fetch initiators for the IQN :" + iqn)
					continue
				}
				if len(initList.InitiatorIDs) > 0 {
					// Fetch the initiator details
					for _, initID := range initList.InitiatorIDs {
						initiator, err := s.adminClient.GetInitiatorByID(symID, initID)
						if err != nil {
							log.Warning("Failed to fetch initiator details for initiator: " + initID)
							continue
						}
						if (initiator.HostID != "") && (initiator.HostID != hostID) {
							errormsg := fmt.Sprintf("IQN: %s is already a part of a different host: %s on: %s", iqn, initiator.HostID, symID)
							log.Error(errormsg)
							return fmt.Errorf(errormsg)
						}
					}
				} else {
					log.Debug(fmt.Sprintf("Failed to find any initiators on: %s for IQN: %s", symID, iqn))
				}
			}
		}
	}

	go s.nodeHostSetup(IQNs, false)

	return err
}

// nodeHostSeup performs a few necessary functions for the nodes to function properly
// - a Host exists within PowerMax, to identify this node
// - The Host contains the discovered iSCSI initiators
// - performs an iSCSI login
func (s *service) nodeHostSetup(IQNs []string, skipLogin bool) error {
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

	// Get the masking view ID using node id
	if s.opts.NodeName == "" {
		return fmt.Errorf("Node name not set")
	}
	nodeName, _, mvid := s.GetHostSGAndMVIDFromNodeID(s.opts.NodeName)

	arrays := &types.SymmetrixIDList{}

	var err error

	// Get the list of arrays
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
		return fmt.Errorf("Unable to retrieve Array List, timed out")
	}
	log.Debug(fmt.Sprintf("GetSymmetrixIDList returned: %v", arrays))

	// for each array, create or update the Host and Initiators
	for _, array := range arrays.SymmetrixIDs {
		_, err := s.createOrUpdateHost(array, nodeName, IQNs)
		if err != nil {
			log.Error(err.Error())
			return err
		}
	}

	// for each array, check the masking view
	for _, array := range arrays.SymmetrixIDs {
		view, err := s.adminClient.GetMaskingViewByID(array, mvid)
		if err != nil {
			// masking view does not exist, not an error but no need to login
			log.Debugf("Masking View %s does not exist for array %s, skipping login", mvid, array)
		} else {
			// masking view exists, we need to log into some targets
			targets, err := s.getTargetsForMaskingView(array, view)
			if err != nil {
				log.Debugf(err.Error())
				continue
			}
			for _, tgt := range targets {
				// discover the targets but do not login
				log.Debugf("Discovering iSCSI targets on %s", tgt.Portal)
				_, err = s.iscsiClient.DiscoverTargets(tgt.Portal, true)
				s.iscsiClient.DiscoverTargets(tgt.Portal, false)
			}
		}
	}

	s.nodeIsInitialized = true

	return nil
}

func (s *service) getTargetsForMaskingView(array string, view *types.MaskingView) ([]goiscsi.ISCSITarget, error) {
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
		return fmt.Errorf("Unable to retrieve Array List, timed out")
	}

	// for each array known to unisphere, ensure we have performed an iSCSI login at least once
	for _, array := range arrays.SymmetrixIDs {
		deadline = time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
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

func (s *service) createOrUpdateHost(array string, nodeName string, IQNs []string) (*types.Host, error) {
	log.Debug(fmt.Sprintf("Processing array: %s, nodeName: %s, initiators: %v", array, nodeName, IQNs))
	if array == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No array specified")
	}
	if nodeName == "" {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No nodeName specified")
	}
	if len(IQNs) == 0 {
		return &types.Host{}, fmt.Errorf("createOrUpdateHost: No initiators specified")
	}

	host, err := s.adminClient.GetHostByID(array, nodeName)
	log.Debug(fmt.Sprintf("GetHostById returned: %v, %v", host, err))

	if err != nil {
		// host does not exist, create it
		log.Debug(fmt.Sprintf("Host %s does not exist. Creating it.", nodeName))

		deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
		for tries := 0; time.Now().Before(deadline); tries++ {
			host, err = s.adminClient.CreateHost(array, nodeName, IQNs, nil)

			if err != nil {
				// Retry on this error
				log.Debug(fmt.Sprintf("failed to create Host; retrying..."))
				time.Sleep(time.Second << uint(tries)) // incremental back-off
				continue
			}
			break
		}
		if err != nil {
			return &types.Host{}, fmt.Errorf("Unable to create Host: %v", err)
		}
	} else {
		// host does exist, update it if necessary
		if !s.stringSlicesEqual(host.Initiators, IQNs) {
			log.Debug(fmt.Sprintf("Host %s exists, ensuring all initiators are associated.", nodeName))
			deadline := time.Now().Add(time.Duration(s.GetPmaxTimeoutSeconds()) * time.Second)
			for tries := 0; time.Now().Before(deadline); tries++ {
				var updatedHost *types.Host
				updatedHost, err = s.adminClient.UpdateHostInitiators(array, host, IQNs)
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
		} else {
			log.Debug(fmt.Sprintf("Host %s exists and initiators are set.", nodeName))
		}
	}

	return host, nil
}

func (s *service) stringSlicesEqual(a, b []string) bool {
	sort.Strings(a)
	sort.Strings(b)

	if len(a) != len(b) {
		return false
	}
	return reflect.DeepEqual(a, b)
}
