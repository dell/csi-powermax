package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gofsutil"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Variables set only for unit testing.
var unitTestEmulateBlockDevice bool

// Device is a struct for holding details about a block device
type Device struct {
	FullPath string
	Name     string
	RealDev  string
}

// GetDevice returns a Device struct with info about the given device, or
// an error if it doesn't exist or is not a block device
func GetDevice(path string) (*Device, error) {

	fi, err := os.Lstat(path)
	if err != nil {
		log.Error("Could not lstat path: " + path)
		return nil, err
	}

	// eval any symlinks and make sure it points to a device
	d, err := filepath.EvalSymlinks(path)
	if err != nil {
		log.Error("Could not evaluate symlinks path: " + path)
		return nil, err
	}

	// TODO does EvalSymlinks throw error if link is to non-
	// existent file? assuming so by masking error below
	ds, _ := os.Stat(d)
	dm := ds.Mode()
	if unitTestEmulateBlockDevice {
		// For unit testing only, emulate a block device on windows
		dm = dm | os.ModeDevice
	}
	if dm&os.ModeDevice == 0 {
		return nil, fmt.Errorf(
			"%s is not a block device", path)
	}

	return &Device{
		Name:     fi.Name(),
		FullPath: path,
		RealDev:  strings.Replace(d, "\\", "/", -1),
	}, nil
}

// publishVolume uses the parameters in req to bindmount the underlying block
// device to the requested target path. A private mount is performed first
// within the given privDir directory.
// The req.TargetPath should be a path starting with "/" (except for unit testing).
//
// publishVolume handles both Mount and Block access types
func publishVolume(
	req *csi.NodePublishVolumeRequest,
	privDir, device string) error {

	id := req.GetVolumeId()

	target := req.GetTargetPath()
	if target == "" {
		return status.Error(codes.InvalidArgument,
			"Target Path is required")
	}

	ro := req.GetReadonly()

	volCap := req.GetVolumeCapability()
	if volCap == nil {
		return status.Error(codes.InvalidArgument,
			"Volume Capability is required")
	}

	accMode := volCap.GetAccessMode()
	if accMode == nil {
		return status.Error(codes.InvalidArgument,
			"Volume Access Mode is required")
	}

	// make sure device is valid
	sysDevice, err := GetDevice(device)
	if err != nil {
		return status.Errorf(codes.Internal,
			"error getting block device for volume: %s, err: %s",
			id, err.Error())
	}

	isBlock := false
	typeSet := false
	multiAccessFlag := ""
	if blockVol := volCap.GetBlock(); blockVol != nil {
		// Read-only is not supported for BlockVolume. Doing a read-only
		// bind mount of the device to the target path does not prevent
		// the underlying block device from being modified, so don't
		// advertise a false sense of security
		if accMode.GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			multiAccessFlag = "rw"
		}
		if ro {
			return status.Error(codes.InvalidArgument,
				"read only not supported for Block Volume")
		}
		isBlock = true
		typeSet = true
	}
	mntVol := volCap.GetMount()
	if mntVol != nil {
		if accMode.GetMode() == csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER {
			// MULTI_WRITER not supported for mount volumes
			return status.Error(codes.AlreadyExists, "Mount volumes do not support AccessMode MULTI_NODE_MULTI_WRITER")
		}
		typeSet = true
	}
	if !typeSet {
		return status.Error(codes.InvalidArgument,
			"Volume Access Type is required")
	}

	// Make sure target is created. The spec says the driver is responsible
	// for creating the target, but Kubernetes generallly creates the target.
	if isBlock {
		_, err = mkfile(target)
		if err != nil {
			return status.Error(codes.FailedPrecondition, fmt.Sprintf("Could not create %s: %s", target, err.Error()))
		}
	} else {
		_, err = mkdir(target)
		if err != nil {
			return status.Error(codes.FailedPrecondition, fmt.Sprintf("Could not create %s: %s", target, err.Error()))
		}
	}

	// Make sure privDir exists and is a directory
	if _, err := mkdir(privDir); err != nil {
		return err
	}

	// Path to mount device to
	privTgt := getPrivateMountPoint(privDir, id)

	f := log.Fields{
		"id":           id,
		"volumePath":   sysDevice.FullPath,
		"device":       sysDevice.RealDev,
		"target":       target,
		"privateMount": privTgt,
	}
	ctx := context.Background()

	// Check if device is already mounted
	devMnts, err := getDevMounts(sysDevice)
	if err != nil {
		return status.Errorf(codes.Internal,
			"could not reliably determine existing mount status: %s",
			err.Error())
	}

	if len(devMnts) == 0 {
		// Device isn't mounted anywhere, do the private mount
		log.WithFields(f).Debug("attempting mount to private area")

		// Make sure private mount point exists
		var created bool
		if isBlock {
			created, err = mkfile(privTgt)
		} else {
			created, err = mkdir(privTgt)
		}
		if err != nil {
			return status.Errorf(codes.Internal,
				"Unable to create private mount point: %s",
				err.Error())
		}
		if !created {
			log.WithFields(f).Debug("private mount target already exists")

			// The place where our device is supposed to be mounted
			// already exists, but we also know that our device is not mounted anywhere
			// Either something didn't clean up correctly, or something else is mounted
			// If the private mount is not in use, it's okay to re-use it. But make sure
			// it's not in use first

			mnts, err := gofsutil.GetMounts(ctx)
			if err != nil {
				return status.Errorf(codes.Internal,
					"could not reliably determine existing mount status: %s",
					err.Error())
			}
			if len(mnts) == 0 {
				return status.Errorf(codes.Unavailable, "volume %s not published to node", id)
			}
			for _, m := range mnts {
				// This used to fail if m.Path==privTgt, but this will not work for
				// idempotent retries and must be avoided.
				if m.Path == target {
					log.WithFields(f).WithField("mountedDevice", m.Device).Error(
						"mount point already in use by device... continuing")
				}
			}
		}

		if !isBlock {
			fs := mntVol.GetFsType()
			mntFlags := mntVol.GetMountFlags()

			if err := handlePrivFSMount(
				ctx, accMode, sysDevice, mntFlags, fs, privTgt); err != nil {
				return err
			}
		} else {
			if err := gofsutil.BindMount(ctx, sysDevice.FullPath, privTgt, multiAccessFlag); err != nil {
				return status.Errorf(codes.Internal,
					"failure bind-mounting block device to private mount: %s", err.Error())
			}
		}

	} else {
		// Device is already mounted. Need to ensure that it is already
		// mounted to the expected private mount, with correct rw/ro perms
		mounted := false
		for _, m := range devMnts {
			log.Printf("devMnt: %#v", m)
			if m.Path == target {
				log.Printf("mount %#v already mounted to requested target %s", m, target)
				mounted = true
			}
			if m.Path == privTgt {
				mounted = true
				rwo := "rw"
				if ro {
					rwo = "ro"
				}
				if contains(m.Opts, rwo) {
					log.WithFields(f).Debug(
						"private mount already in place")
					break
				} else {
					log.Printf("mount %#v rwo %s", m, rwo)
					return status.Error(codes.InvalidArgument,
						"access mode conflicts with existing mounts")
				}
			}
		}
		if !mounted {
			return status.Error(codes.Internal,
				"device already in use and mounted elsewhere")
		}
	}

	// Private mount in place, now bind mount to target path
	devMnts, err = getDevMounts(sysDevice)
	if err != nil {
		return status.Errorf(codes.Internal,
			"could not reliably determine existing mount status: %s",
			err.Error())
	}

	// If mounts already existed for this device, check if mount to
	// target path was already there
	if len(devMnts) > 0 {
		for _, m := range devMnts {
			if m.Path == target {
				// volume already published to target
				// if mount options look good, do nothing
				rwo := multiAccessFlag
				if accMode.GetMode() == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY {
					rwo = "ro"
				}
				if rwo != "" && !contains(m.Opts, rwo) {
					log.Printf("mount %#v rwo %s\n", m, rwo)
					return status.Error(codes.Internal,
						"volume previously published with different options")

				}
				// Existing mount satisfies request
				log.WithFields(f).Debug("volume already published to target")
				return nil
			}
		}

	}

	var mntFlags []string
	if isBlock {
		mntFlags = make([]string, 0)
		if multiAccessFlag != "" {
			mntFlags = append(mntFlags, multiAccessFlag)
		}
	} else {
		mntFlags = mntVol.GetMountFlags()
		if accMode.GetMode() == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY {
			mntFlags = append(mntFlags, "ro")
		}
	}
	if err := gofsutil.BindMount(ctx, privTgt, target, mntFlags...); err != nil {
		return status.Errorf(codes.Internal,
			"error publish volume to target path: %s",
			err.Error())
	}

	return nil
}

func handlePrivFSMount(
	ctx context.Context,
	accMode *csi.VolumeCapability_AccessMode,
	sysDevice *Device,
	mntFlags []string,
	fs, privTgt string) error {

	// If read-only access mode, we don't allow formatting
	if accMode.GetMode() == csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY {
		mntFlags = append(mntFlags, "ro")
		if err := gofsutil.Mount(ctx, sysDevice.FullPath, privTgt, fs, mntFlags...); err != nil {
			return status.Errorf(codes.Internal,
				"error performing private mount: %s",
				err.Error())
		}
		return nil
	} else if accMode.GetMode() == csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER {
		if err := gofsutil.FormatAndMount(ctx, sysDevice.FullPath, privTgt, fs, mntFlags...); err != nil {
			return status.Errorf(codes.Internal,
				"error performing private mount: %s",
				err.Error())
		}
		return nil
	}
	return status.Error(codes.Internal, "Invalid access mode")
}

func getPrivateMountPoint(privDir string, name string) string {
	return filepath.Join(privDir, name)
}

func contains(list []string, item string) bool {
	for _, x := range list {
		if x == item {
			return true
		}
	}
	return false
}

// mkfile creates a file specified by the path if needed.
// return pair is a bool flag of whether file was created, and an error
func mkfile(path string) (bool, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		file, err := os.OpenFile(path, os.O_CREATE, 0600)
		if err != nil {
			log.WithField("path", path).WithError(
				err).Error("Unable to create file")
			return false, err
		}
		file.Close()
		log.WithField("path", path).Debug("created file")
		return true, nil
	}
	if st.IsDir() {
		return false, fmt.Errorf("existing path is a directory")
	}
	return false, nil
}

// mkdir creates the directory specified by path if needed.
// return pair is a bool flag of whether dir was created, and an error
func mkdir(path string) (bool, error) {
	st, err := os.Stat(path)
	if os.IsNotExist(err) {
		if err := os.Mkdir(path, 0750); err != nil {
			log.WithField("dir", path).WithError(
				err).Error("Unable to create dir")
			return false, err
		}
		log.WithField("path", path).Debug("created directory")
		return true, nil
	}
	if !st.IsDir() {
		return false, fmt.Errorf("existing path is not a directory")
	}
	return false, nil
}

// unpublishVolume removes the bind mount to the target path, and also removes
// the mount to the private mount directory if the volume is no longer in use.
// It determines this by checking to see if the volume is mounted anywhere else
// other than the private mount.
// The req.TargetPath should be a path starting with "/" (except for unit testing).
func unpublishVolume(
	req *csi.NodeUnpublishVolumeRequest,
	privDir, device string) (bool, error) {
	lastUnmounted := false

	ctx := context.Background()
	id := req.GetVolumeId()

	target := req.GetTargetPath()
	if target == "" {
		return lastUnmounted, status.Error(codes.InvalidArgument,
			"target path required")
	}

	// make sure device is valid
	sysDevice, err := GetDevice(device)
	if err != nil {
		// This error needs to be idempotent since device was not found
		return true, nil
	}

	// Path to mount device to
	privTgt := getPrivateMountPoint(privDir, id)

	mnts, err := gofsutil.GetMounts(ctx)
	if err != nil {
		return lastUnmounted, status.Errorf(codes.Internal,
			"could not reliably determine existing mount status: %s",
			err.Error())
	}

	tgtMnt := false
	privMnt := false
	for _, m := range mnts {
		if m.Source == sysDevice.RealDev || m.Device == sysDevice.RealDev {
			if m.Path == privTgt {
				privMnt = true
			} else if m.Path == target {
				tgtMnt = true
			}
		}
	}

	if tgtMnt {
		if err := gofsutil.Unmount(ctx, target); err != nil {
			return lastUnmounted, status.Errorf(codes.Internal,
				"Error unmounting target: %s", err.Error())
		}
	}

	if privMnt {
		if lastUnmounted, err = unmountPrivMount(ctx, sysDevice, privTgt); err != nil {
			return lastUnmounted, status.Errorf(codes.Internal,
				"Error unmounting private mount: %s", err.Error())
		}
	} else {
		mnts, err := getDevMounts(sysDevice)
		if err == nil && len(mnts) == 0 {
			log.Info("No private mount or remaining mounts device: " + sysDevice.Name)
			lastUnmounted = true
		}
	}

	return lastUnmounted, nil
}

func unmountPrivMount(
	ctx context.Context,
	dev *Device,
	target string) (bool, error) {
	lastUnmounted := false

	mnts, err := getDevMounts(dev)
	if err != nil {
		return lastUnmounted, err
	}

	// remove private mount if we can (if there are no other mounts
	if len(mnts) == 1 && mnts[0].Path == target {
		if err := gofsutil.Unmount(ctx, target); err != nil {
			return lastUnmounted, err
		}
		lastUnmounted = true
		log.WithField("directory", target).Debug(
			"removing directory")
		os.Remove(target)
	}
	return lastUnmounted, nil
}

func getDevMounts(
	sysDevice *Device) ([]gofsutil.Info, error) {

	ctx := context.Background()
	devMnts := make([]gofsutil.Info, 0)

	mnts, err := gofsutil.GetMounts(ctx)
	if err != nil {
		return devMnts, err
	}
	for _, m := range mnts {
		if m.Device == sysDevice.RealDev || (m.Device == "devtmpfs" && m.Source == sysDevice.RealDev) {
			devMnts = append(devMnts, m)
		}
	}
	return devMnts, nil
}
