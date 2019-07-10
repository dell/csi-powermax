package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/DATA-DOG/godog"
	csi "github.com/container-storage-interface/spec/lib/go/csi"
	pmax "github.com/dell/csi-powermax/pmax"
	service "github.com/dell/csi-powermax/service"
	ptypes "github.com/golang/protobuf/ptypes"
)

const (
	MaxRetries      = 10
	RetrySleepTime  = 10 * time.Second
	ShortSleepTime  = 3 * time.Second
	SleepTime       = 100 * time.Millisecond
	ApplicationName = "CSI Driver Integration Tests"
)

type feature struct {
	errs                     []error
	createVolumeRequest      *csi.CreateVolumeRequest
	createVolumeResponse     *csi.CreateVolumeResponse
	publishVolumeRequest     *csi.ControllerPublishVolumeRequest
	publishVolumeResponse    *csi.ControllerPublishVolumeResponse
	nodePublishVolumeRequest *csi.NodePublishVolumeRequest
	listVolumesResponse      *csi.ListVolumesResponse
	listSnapshotsResponse    *csi.ListSnapshotsResponse
	capability               *csi.VolumeCapability
	capabilities             []*csi.VolumeCapability
	getCapacityRequest       *csi.GetCapacityRequest
	getCapacityResponse      *csi.GetCapacityResponse
	volID                    string
	snapshotID               string
	volIDList                []string
	maxRetryCount            int
	symID                    string
	srpID                    string
	serviceLevel             string
	pmaxClient               pmax.Pmax
	publishVolumeContextMap  map[string]map[string]string
	idempotentTest           bool
}

func (f *feature) addError(err error) {
	f.errs = append(f.errs, err)
}

func (f *feature) aPowermaxService() error {
	f.errs = make([]error, 0)
	f.createVolumeRequest = nil
	f.createVolumeResponse = nil
	f.publishVolumeRequest = nil
	f.publishVolumeResponse = nil
	f.listVolumesResponse = nil
	f.listSnapshotsResponse = nil
	f.getCapacityRequest = nil
	f.getCapacityResponse = nil
	f.capability = nil
	f.volID = ""
	f.snapshotID = ""
	f.volIDList = f.volIDList[:0]
	f.maxRetryCount = MaxRetries
	f.publishVolumeContextMap = make(map[string]map[string]string)
	f.idempotentTest = false
	if f.pmaxClient == nil {
		var err error
		endpoint := os.Getenv("X_CSI_POWERMAX_ENDPOINT")
		if endpoint == "" {
			return fmt.Errorf("Cannot read X_CSI_POWERMAX_ENDPOINT")
		}
		f.pmaxClient, err = pmax.NewClientWithArgs(endpoint, "", ApplicationName, true, false)
		if err != nil {
			return fmt.Errorf("Cannot attach to pmax library: %s", err)
		}
		configConnect := &pmax.ConfigConnect{
			Endpoint: endpoint,
			Username: os.Getenv("X_CSI_POWERMAX_USER"),
			Password: os.Getenv("X_CSI_POWERMAX_PASSWORD"),
		}
		err = f.pmaxClient.Authenticate(configConnect)
		if err != nil {
			return err
		}
	}
	f.symID = os.Getenv("SYMID")
	f.srpID = os.Getenv("SRPID")
	f.serviceLevel = os.Getenv("SERVICELEVEL")
	return nil
}

func (f *feature) aBasicBlockVolumeRequest(name string, size int64) error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[service.SymmetrixIDParam] = f.symID
	params[service.ServiceLevelParam] = f.serviceLevel
	params[service.StoragePoolParam] = f.srpID
	params["thickprovisioning"] = "false"
	params[service.ApplicationPrefixParam] = "INT"
	req.Parameters = params
	now := time.Now()
	req.Name = fmt.Sprintf("Int%d", now.Nanosecond())
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = size * 1024 * 1024
	req.CapacityRange = capacityRange
	capability := new(csi.VolumeCapability)
	block := new(csi.VolumeCapability_BlockVolume)
	blockType := new(csi.VolumeCapability_Block)
	blockType.Block = block
	capability.AccessType = blockType
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	capability.AccessMode = accessMode
	f.capability = capability
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	f.createVolumeRequest = req
	return nil
}

func (f *feature) iUseThickProvisioning() error {
	f.createVolumeRequest.Parameters[service.ThickVolumesParam] = "true"
	f.createVolumeRequest.Parameters[service.StorageGroupParam] = "CSI-Integration-Thick"
	return nil
}

func (f *feature) anAlternateServiceLevel(serviceLevel string) error {
	f.createVolumeRequest.Parameters[service.ServiceLevelParam] = serviceLevel
	return nil
}

func (f *feature) accessTypeIs(arg1 string) error {
	switch arg1 {
	case "multi-writer":
		f.createVolumeRequest.VolumeCapabilities[0].AccessMode.Mode =
			csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	}
	return nil
}

func (f *feature) iCallCreateVolume() error {
	var err error
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	f.createVolumeResponse, err = client.CreateVolume(ctx, f.createVolumeRequest)
	if err != nil {
		fmt.Printf("CreateVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("CreateVolume %s (%s) %s\n", f.createVolumeResponse.GetVolume().VolumeContext["Name"],
			f.createVolumeResponse.GetVolume().VolumeId, f.createVolumeResponse.GetVolume().VolumeContext["CreationTime"])
		f.volID = f.createVolumeResponse.GetVolume().VolumeId
		f.volIDList = append(f.volIDList, f.createVolumeResponse.GetVolume().VolumeId)
	}
	return nil
}

func (f *feature) createVolume(req *csi.CreateVolumeRequest) (*csi.CreateVolumeResponse, error) {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	var volResp *csi.CreateVolumeResponse
	var err error
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		volResp, err = client.CreateVolume(ctx, req)
		if err == nil || !strings.Contains(err.Error(), "Insufficient resources") {
			// no need for retry
			break
		}
		fmt.Printf("retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return volResp, err
}

func (f *feature) whenICallDeleteVolume() error {
	err := f.deleteVolume(f.volID)
	if err != nil {
		fmt.Printf("DeleteVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("DeleteVolume %s completed successfully\n", f.volID)
	}
	return nil
}

func (f *feature) iReceiveAValidVolume() error {
	if f.createVolumeResponse == nil {
		return fmt.Errorf("Expected a Volume Resonse but there was none")
	}
	attributes := f.createVolumeResponse.Volume.VolumeContext
	if attributes == nil {
		return fmt.Errorf("Expected volume attributes but there were none")
	}
	serviceLevel := attributes["ServiceLevel"]
	expectedServiceLevel := f.createVolumeRequest.Parameters[service.ServiceLevelParam]
	if expectedServiceLevel != serviceLevel {
		return fmt.Errorf("Expected ServiceLevel %s but ServiceLevel %s was returned", expectedServiceLevel, serviceLevel)
	}
	return nil
}

func (f *feature) deleteVolume(id string) error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	delVolReq := new(csi.DeleteVolumeRequest)
	delVolReq.VolumeId = id
	var err error
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.DeleteVolume(ctx, delVolReq)
		if err == nil || !strings.Contains(err.Error(), "Insufficient resources") {
			// no need for retry
			break
		}
		fmt.Printf("retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) thereAreNoErrors() error {
	if len(f.errs) == 0 {
		return nil
	}
	return f.errs[0]
}

func (f *feature) theErrorMessageShouldContain(expected string) error {
	// If arg1 is none, we expect no error, any error received is unexpected
	if expected == "none" {
		if len(f.errs) == 0 {
			return nil
		} else {
			return fmt.Errorf("Unexpected error(s): %s", f.errs[0])
		}
	}
	// We expect an error...
	if len(f.errs) == 0 {
		return errors.New("there were no errors but we expected: " + expected)
	}
	err0 := f.errs[0]
	if !strings.Contains(err0.Error(), expected) {
		return errors.New(fmt.Sprintf("Error %s does not contain the expected message: %s", err0.Error(), expected))
	}
	return nil
}

func (f *feature) aMountVolumeRequest(name string) error {
	req := f.getMountVolumeRequest(name)
	f.createVolumeRequest = req
	return nil
}

func (f *feature) getMountVolumeRequest(name string) *csi.CreateVolumeRequest {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[service.SymmetrixIDParam] = f.symID
	params[service.ServiceLevelParam] = f.serviceLevel
	params[service.StoragePoolParam] = f.srpID
	req.Parameters = params
	now := time.Now()
	req.Name = fmt.Sprintf("Int%d", now.Nanosecond())
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = 100 * 1024 * 1024
	req.CapacityRange = capacityRange
	capability := new(csi.VolumeCapability)
	mountVolume := new(csi.VolumeCapability_MountVolume)
	mountVolume.FsType = "xfs"
	mountVolume.MountFlags = make([]string, 0)
	mount := new(csi.VolumeCapability_Mount)
	mount.Mount = mountVolume
	capability.AccessType = mount
	accessMode := new(csi.VolumeCapability_AccessMode)
	accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	capability.AccessMode = accessMode
	f.capability = capability
	capabilities := make([]*csi.VolumeCapability, 0)
	capabilities = append(capabilities, capability)
	req.VolumeCapabilities = capabilities
	f.createVolumeRequest = req
	return req
}
func (f *feature) aVolumeRequestFileSystem(name,fstype,access,voltype string) error {
        req := f.getVolumeRequestFileSystem(name,fstype,access,voltype)
        f.createVolumeRequest = req
        return nil
}
func (f *feature) getVolumeRequestFileSystem(name,fstype,access,voltype string) *csi.CreateVolumeRequest {
        req := new(csi.CreateVolumeRequest)
        params := make(map[string]string)
        params[service.SymmetrixIDParam] = f.symID
        params[service.ServiceLevelParam] = f.serviceLevel
        params[service.StoragePoolParam] = f.srpID
        switch voltype {
        case "block":
              params["thickprovisioning"] = "false"
              params[service.ApplicationPrefixParam] = "INT"
        }
        req.Parameters = params
        now := time.Now()
        req.Name = fmt.Sprintf("Int%d", now.Nanosecond())
        capacityRange := new(csi.CapacityRange)
        capacityRange.RequiredBytes = 100 * 1024 * 1024
        req.CapacityRange = capacityRange
        capability := new(csi.VolumeCapability)
        switch voltype {
        case "mount":
              mountVolume := new(csi.VolumeCapability_MountVolume)
              switch fstype {
              case "xfs": 
                   mountVolume.FsType = "xfs"
              case "ext4":
                   mountVolume.FsType = "ext4"
              }
              mountVolume.MountFlags = make([]string, 0)
              mount := new(csi.VolumeCapability_Mount)
              mount.Mount = mountVolume
              capability.AccessType = mount
        case "block":
              block := new(csi.VolumeCapability_BlockVolume)
              blockType := new(csi.VolumeCapability_Block)
              blockType.Block = block
              capability.AccessType = blockType
        }
        accessMode := new(csi.VolumeCapability_AccessMode)
        switch access {
        case "single-writer":
                accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
        case "multi-writer":
                accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
        case "multi-reader":
                accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
        case "multi-node-single-writer":
                accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER
        }
        capability.AccessMode = accessMode
        f.capability = capability
        capabilities := make([]*csi.VolumeCapability, 0)
        capabilities = append(capabilities, capability)
        req.VolumeCapabilities = capabilities
        f.createVolumeRequest = req
        return req
}


func (f *feature) getControllerPublishVolumeRequest() *csi.ControllerPublishVolumeRequest {
	req := new(csi.ControllerPublishVolumeRequest)
	req.VolumeId = f.volID
	req.NodeId = os.Getenv("X_CSI_POWERMAX_NODENAME")
	req.Readonly = false
	req.VolumeCapability = f.capability
	f.publishVolumeRequest = req
	return req
}

func (f *feature) whenICallPublishVolume(nodeIDEnvVar string) error {
	err := f.controllerPublishVolume(f.volID, nodeIDEnvVar)
	if err != nil {
		fmt.Printf("ControllerPublishVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("ControllerPublishVolume completed successfully\n")
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) controllerPublishVolume(id string, nodeIDEnvVar string) error {
	var err error
	req := f.getControllerPublishVolumeRequest()
	req.VolumeId = id
	req.NodeId = os.Getenv("X_CSI_POWERMAX_NODENAME")
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	resp, err := client.ControllerPublishVolume(ctx, req)
	f.publishVolumeContextMap[id] = resp.GetPublishContext()
	f.publishVolumeResponse = resp
	return err
}

func (f *feature) whenICallUnpublishVolume(nodeIDEnvVar string) error {
	err := f.controllerUnpublishVolume(f.publishVolumeRequest.VolumeId, nodeIDEnvVar)
	if err != nil {
		fmt.Printf("ControllerUnpublishVolume failed: %s\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("ControllerUnpublishVolume completed successfully\n")
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) controllerUnpublishVolume(id string, nodeIDEnvVar string) error {
	req := new(csi.ControllerUnpublishVolumeRequest)
	req.VolumeId = id
	req.NodeId = os.Getenv("X_CSI_POWERMAX_NODENAME")
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	_, err := client.ControllerUnpublishVolume(ctx, req)
	return err
}

func (f *feature) maxRetries(arg1 int) error {
	f.maxRetryCount = arg1
	return nil
}

func (f *feature) aCapabilityWithVoltypeAccessFstype(voltype, access, fstype string) error {
	// Construct the volume capabilities
	capability := new(csi.VolumeCapability)
	switch voltype {
	case "block":
		blockVolume := new(csi.VolumeCapability_BlockVolume)
		block := new(csi.VolumeCapability_Block)
		block.Block = blockVolume
		capability.AccessType = block
	case "mount":
		mountVolume := new(csi.VolumeCapability_MountVolume)
		mountVolume.FsType = fstype
		mountVolume.MountFlags = make([]string, 0)
		mount := new(csi.VolumeCapability_Mount)
		mount.Mount = mountVolume
		capability.AccessType = mount
	}
	accessMode := new(csi.VolumeCapability_AccessMode)
	switch access {
	case "single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER
	case "multi-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	case "multi-reader":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY
	case "multi-node-single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_SINGLE_WRITER
	}
	capability.AccessMode = accessMode
	f.capabilities = make([]*csi.VolumeCapability, 0)
	f.capabilities = append(f.capabilities, capability)
	f.capability = capability
	return nil
}

func (f *feature) aVolumeRequest(name string, size int64) error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params["storagepool"] = os.Getenv("STORAGE_POOL")
	params["thickprovisioning"] = "true"
	req.Parameters = params
	req.Name = name
	capacityRange := new(csi.CapacityRange)
	capacityRange.RequiredBytes = size * 1024
	req.CapacityRange = capacityRange
	req.VolumeCapabilities = f.capabilities
	f.createVolumeRequest = req
	return nil
}

func (f *feature) getNodePublishVolumeRequest() *csi.NodePublishVolumeRequest {
	req := new(csi.NodePublishVolumeRequest)
	req.VolumeId = f.volID
	req.Readonly = false
	req.VolumeCapability = f.capability
	req.PublishContext = f.publishVolumeResponse.PublishContext
	block := f.capability.GetBlock()
	if block != nil {
		req.TargetPath = datafile
	}
	mount := f.capability.GetMount()
	if mount != nil {
		now := time.Now()
		targetPath := fmt.Sprintf("datadir-%d", now.Nanosecond())
		var fileMode os.FileMode
		fileMode = 0777
		err := os.Mkdir(targetPath, fileMode)
		if err != nil && !os.IsExist(err) {
			fmt.Printf("%s: %s\n", req.TargetPath, err)
		}
		var cwd string
		if cwd, err = os.Getwd(); err != nil {
			panic("cannot get working directory")
		}
		req.TargetPath = cwd + "/" + targetPath
	}
	f.nodePublishVolumeRequest = req
	return req
}

func (f *feature) whenICallNodePublishVolume(arg1 string) error {
	err := f.nodePublishVolume(f.volID, "")
	if err != nil {
		fmt.Printf("NodePublishVolume failed: %s\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("NodePublishVolume completed successfully\n")
	}
	time.Sleep(ShortSleepTime)
	return nil
}

func (f *feature) nodePublishVolume(id string, path string) error {
	req := f.nodePublishVolumeRequest
	if req == nil || !f.idempotentTest {
		req = f.getNodePublishVolumeRequest()
	}
	if path != "" {
		block := f.capability.GetBlock()
		if block != nil {
			req.TargetPath = path
		}
		mount := f.capability.GetMount()
		if mount != nil {
			req.TargetPath = path
		}
	}
	req.VolumeId = id
	req.PublishContext = f.publishVolumeContextMap[id]
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	_, err := client.NodePublishVolume(ctx, req)
	return err
}

func (f *feature) whenICallNodeUnpublishVolume(arg1 string) error {
	fmt.Printf("calling nodeUnpublish vol %s targetPath %s\n", f.volID, f.nodePublishVolumeRequest.TargetPath)
	err := f.nodeUnpublishVolume(f.volID, f.nodePublishVolumeRequest.TargetPath)
	if err != nil {
		fmt.Printf("NodeUnpublishVolume failed: %s\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("NodeUnpublishVolume completed successfully\n")
	}
	time.Sleep(ShortSleepTime)
	return nil
}

func (f *feature) nodeUnpublishVolume(id string, path string) error {
	req := &csi.NodeUnpublishVolumeRequest{VolumeId: id, TargetPath: path}
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	_, err := client.NodeUnpublishVolume(ctx, req)
	return err
}

func (f *feature) verifyPublishedVolumeWithVoltypeAccessFstype(voltype, access, fstype string) error {
	if len(f.errs) > 0 {
		fmt.Printf("Not verifying published volume because of previous error")
		return nil
	}
	var cmd *exec.Cmd
	if voltype == "mount" {
		cmd = exec.Command("/bin/sh", "-c", "mount | grep /tmp/datadir")
	} else if voltype == "block" {
		cmd = exec.Command("/bin/sh", "-c", "mount | grep /tmp/datafile")
	} else {
		return errors.New("unepected volume type")
	}
	stdout, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", stdout)
	if voltype == "mount" {
		// output: /dev/scinia on /tmp/datadir type xfs (rw,relatime,seclabel,attr2,inode64,noquota)
		if !strings.Contains(string(stdout), "/dev/scini") {
			return errors.New("Mount did not contain /dev/scini for scale-io")
		}
		if !strings.Contains(string(stdout), "/tmp/datadir") {
			return errors.New("Mount did not contain /tmp/datadir for type mount")
		}
		if !strings.Contains(string(stdout), fmt.Sprintf("type %s", fstype)) {
			return errors.New(fmt.Sprintf("Did not find expected fstype %s", fstype))
		}

	} else if voltype == "block" {
		// devtmpfs on /tmp/datafile type devtmpfs (rw,relatime,seclabel,size=8118448k,nr_inodes=2029612,mode=755)
		if !strings.Contains(string(stdout), "devtmpfs on /tmp/datafile") {
			return errors.New("Expected devtmpfs on /tmp/datafile for mounted block device")
		}
	}
	return nil
}

func (f *feature) iCallCreateSnapshot() error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: f.volID,
		Name:           "snapshot-0eb5347a-0000-11e9-ab1c-005056a64ad3",
	}
	resp, err := client.CreateSnapshot(ctx, req)
	if err != nil {
		fmt.Printf("CreateSnapshot returned error: %s\n", err.Error())
		f.addError(err)
	} else {
		f.snapshotID = resp.Snapshot.SnapshotId
		fmt.Printf("createSnapshot: SnapshotId %s SourceVolumeId %s CreationTime %s\n",
			resp.Snapshot.SnapshotId, resp.Snapshot.SourceVolumeId, ptypes.TimestampString(resp.Snapshot.CreationTime))
	}
	time.Sleep(RetrySleepTime)
	return nil
}

func (f *feature) iCallDeleteSnapshot() error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	req := &csi.DeleteSnapshotRequest{
		SnapshotId: f.snapshotID,
	}
	_, err := client.DeleteSnapshot(ctx, req)
	if err != nil {
		fmt.Printf("DeleteSnapshot returned error: %s\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("DeleteSnapshot: SnapshotId %s\n", req.SnapshotId)
	}
	time.Sleep(RetrySleepTime)
	return nil
}

func (f *feature) iCallCreateSnapshotConsistencyGroup() error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	var volumeIDList string
	for i, v := range f.volIDList {
		switch i {
		case 0:
			continue
		case 1:
			volumeIDList = v
		default:
			volumeIDList = volumeIDList + "," + v
		}
	}
	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: f.volIDList[0],
		Name:           "",
	}
	req.Parameters = make(map[string]string)
	req.Parameters["VolumeIDList"] = volumeIDList
	resp, err := client.CreateSnapshot(ctx, req)
	if err != nil {
		fmt.Printf("CreateSnapshot returned error: %s\n", err.Error())
		f.addError(err)
	} else {
		f.snapshotID = resp.Snapshot.SnapshotId
		fmt.Printf("createSnapshot: SnapshotId %s SourceVolumeId %s CreationTime %s\n",
			resp.Snapshot.SnapshotId, resp.Snapshot.SourceVolumeId, ptypes.TimestampString(resp.Snapshot.CreationTime))
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) whenICallDeleteAllVolumes() error {
	for _, v := range f.volIDList {
		f.volID = v
		f.whenICallDeleteVolume()
	}
	return nil
}

func (f *feature) iCallCreateVolumeFromSnapshot() error {
	req := f.createVolumeRequest
	req.Name = "volFromSnap-" + req.Name
	source := &csi.VolumeContentSource_SnapshotSource{SnapshotId: f.snapshotID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Snapshot{Snapshot: source}
	fmt.Printf("Calling CreateVolume with snapshot source")

	_ = f.createAVolume(req, "single CreateVolume from Snap")
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) createAVolume(req *csi.CreateVolumeRequest, voltype string) error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	volResp, err := client.CreateVolume(ctx, req)
	if err != nil {
		fmt.Printf("CreateVolume %s request returned error: %s\n", voltype, err.Error())
		f.addError(err)
	} else {
		fmt.Printf("CreateVolume from snap %s (%s) %s\n", volResp.GetVolume().VolumeContext["Name"],
			volResp.GetVolume().VolumeId, volResp.GetVolume().VolumeContext["CreationTime"])
		f.volIDList = append(f.volIDList, volResp.GetVolume().VolumeId)
	}
	return err
}

func (f *feature) iCallCreateManyVolumesFromSnapshot() error {
	for i := 1; i <= 130; i++ {
		req := f.createVolumeRequest
		req.Name = fmt.Sprintf("volFromSnap%d", i)
		source := &csi.VolumeContentSource_SnapshotSource{SnapshotId: f.snapshotID}
		req.VolumeContentSource = new(csi.VolumeContentSource)
		req.VolumeContentSource.Type = &csi.VolumeContentSource_Snapshot{Snapshot: source}
		fmt.Printf("Calling CreateVolume with snapshot source")
		err := f.createAVolume(req, "single CreateVolume from Snap")
		if err != nil {
			fmt.Printf("Error on the %d th volume: %s\n", i, err.Error())
			break
		}
	}
	return nil
}

func (f *feature) iCallListVolume() error {
	var err error
	ctx := context.Background()
	req := &csi.ListVolumesRequest{}
	client := csi.NewControllerClient(grpcClient)
	f.listVolumesResponse, err = client.ListVolumes(ctx, req)
	if err != nil {
		return err
	}
	return nil
}

func (f *feature) aValidListVolumeResponseIsReturned() error {
	resp := f.listVolumesResponse
	entries := resp.GetEntries()
	if entries == nil {
		return errors.New("expected ListVolumeResponse.Entries but none")
	}
	for _, entry := range entries {
		vol := entry.GetVolume()
		if vol != nil {
			id := vol.VolumeId
			capacity := vol.CapacityBytes
			name := vol.VolumeContext["Name"]
			creation := vol.VolumeContext["CreationTime"]
			fmt.Printf("Volume ID: %s Name: %s Capacity: %d CreationTime: %s\n", id, name, capacity, creation)
		}
	}
	return nil
}

func (f *feature) iCallListSnapshot() error {
	var err error
	ctx := context.Background()
	req := &csi.ListSnapshotsRequest{}
	client := csi.NewControllerClient(grpcClient)
	f.listSnapshotsResponse, err = client.ListSnapshots(ctx, req)
	if err != nil {
		return err
	}
	return nil
}

func (f *feature) aValidListSnapshotResponseIsReturned() error {
	nextToken := f.listSnapshotsResponse.GetNextToken()
	if nextToken != "" {
		return errors.New("received NextToken on ListSnapshots but didn't expect one")
	}
	entries := f.listSnapshotsResponse.GetEntries()
	for j := 0; j < len(entries); j++ {
		entry := entries[j]
		id := entry.GetSnapshot().SnapshotId
		ts := ptypes.TimestampString(entry.GetSnapshot().CreationTime)
		fmt.Printf("snapshot ID %s source ID %s timestamp %s\n", id, entry.GetSnapshot().SourceVolumeId, ts)
	}
	return nil
}

func (f *feature) iCreateVolumesInParallel(nVols int) error {
	idchan := make(chan string, nVols)
	errchan := make(chan error, nVols)
	t0 := time.Now()
	// Send requests
	for i := 0; i < nVols; i++ {
		name := fmt.Sprintf("scale%d", i)
		go func(name string, idchan chan string, errchan chan error) {
			var resp *csi.CreateVolumeResponse
			var err error
			req := f.getMountVolumeRequest(name)
			if req != nil {
				resp, err = f.createVolume(req)
				if resp != nil {
					idchan <- resp.GetVolume().VolumeId
				} else {
					idchan <- ""
				}
			}
			errchan <- err
		}(name, idchan, errchan)
	}
	// Wait on complete, collecting ids and errors
	nerrors := 0
	for i := 0; i < nVols; i++ {
		var id string
		var err error
		id = <-idchan
		if id != "" {
			f.volIDList = append(f.volIDList, id)
		}
		err = <-errchan
		if err != nil {
			fmt.Printf("create volume received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	if len(f.volIDList) > nVols {
		f.volIDList = f.volIDList[0:nVols]
	}
	fmt.Printf("Create volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iPublishVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	done := make(chan bool, nvols)
	errchan := make(chan error, nvols)

	// Send requests
	t0 := time.Now()
	for i := 0; i < nVols; i++ {
		id := f.volIDList[i]
		if id == "" {
			continue
		}
		go func(id string, done chan bool, errchan chan error) {
			err := f.controllerPublishVolume(id, "nodeID")
			done <- true
			errchan <- err
		}(id, done, errchan)
	}

	// Wait for responses
	nerrors := 0
	for i := 0; i < nVols; i++ {
		if f.volIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("controller publish received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Controller publish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(4 * SleepTime)
	return nil
}

func (f *feature) iNodePublishVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	// make a data directory for each
	for i := 0; i < nVols; i++ {
		dataDirName := fmt.Sprintf("/tmp/datadir%d", i)
		fmt.Printf("Checking %s\n", dataDirName)
		var fileMode os.FileMode
		fileMode = 0777
		err := os.Mkdir(dataDirName, fileMode)
		if err != nil && !os.IsExist(err) {
			fmt.Printf("%s: %s\n", dataDirName, err)
		}
	}
	done := make(chan bool, nvols)
	errchan := make(chan error, nvols)

	// Send requests
	t0 := time.Now()
	for i := 0; i < nVols; i++ {
		id := f.volIDList[i]
		if id == "" {
			continue
		}
		dataDirName := fmt.Sprintf("/tmp/datadir%d", i)
		go func(id string, dataDirName string, done chan bool, errchan chan error) {
			err := f.nodePublishVolume(id, dataDirName)
			done <- true
			errchan <- err
		}(id, dataDirName, done, errchan)
	}

	// Wait for responses
	nerrors := 0
	for i := 0; i < nVols; i++ {
		if f.volIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("node publish received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Node publish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(2 * SleepTime)
	return nil
}

func (f *feature) iNodeUnpublishVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	done := make(chan bool, nvols)
	errchan := make(chan error, nvols)

	// Send requests
	t0 := time.Now()
	for i := 0; i < nVols; i++ {
		id := f.volIDList[i]
		if id == "" {
			continue
		}
		dataDirName := fmt.Sprintf("/tmp/datadir%d", i)
		go func(id string, dataDirName string, done chan bool, errchan chan error) {
			err := f.nodeUnpublishVolume(id, dataDirName)
			done <- true
			errchan <- err
		}(id, dataDirName, done, errchan)
	}

	// Wait for responses
	nerrors := 0
	for i := 0; i < nVols; i++ {
		if f.volIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("node unpublish received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Node unpublish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iUnpublishVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	done := make(chan bool, nvols)
	errchan := make(chan error, nvols)

	// Send request
	t0 := time.Now()
	for i := 0; i < nVols; i++ {
		id := f.volIDList[i]
		if id == "" {
			continue
		}
		go func(id string, done chan bool, errchan chan error) {
			err := f.controllerUnpublishVolume(id, "nodeID")
			done <- true
			errchan <- err
		}(id, done, errchan)
	}

	// Wait for resonse
	nerrors := 0
	for i := 0; i < nVols; i++ {
		if f.volIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("controller unpublish received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Controller unpublish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) whenIDeleteVolumesInParallel(nVols int) error {
	nVols = len(f.volIDList)
	done := make(chan bool, nVols)
	errchan := make(chan error, nVols)

	// Send requests
	t0 := time.Now()
	for i := 0; i < nVols; i++ {
		id := f.volIDList[i]
		if id == "" {
			continue
		}
		go func(id string, done chan bool, errchan chan error) {
			err := f.deleteVolume(id)
			done <- true
			errchan <- err
		}(f.volIDList[i], done, errchan)
	}

	// Wait on complete
	nerrors := 0
	for i := 0; i < nVols; i++ {
		var finished bool
		var err error
		name := fmt.Sprintf("scale%d", i)
		finished = <-done
		if !finished {
			return errors.New("premature completion")
		}
		err = <-errchan
		if err != nil {
			fmt.Printf("delete volume received error %s: %s\n", name, err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Delete volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(RetrySleepTime)
	return nil
}

func (f *feature) allVolumesAreDeletedSuccessfully() error {
	t0 := time.Now()
	nVols := len(f.volIDList)
	for _, id := range f.volIDList {
		deleted := false
		idComponents := strings.Split(id, "-")
		symVolumeID := idComponents[len(idComponents)-1]
		fmt.Printf("Waiting for volume %s %s to be deleted\n", id, symVolumeID)
		max := 40 * nVols
		if 300 > max {
			max = 300
		}
		for i := 0; i < max; i++ {
			vol, err := f.pmaxClient.GetVolumeByID(f.symID, symVolumeID)
			if vol == nil {
				deleted = strings.Contains(err.Error(), "cannot be found")
				fmt.Printf("volume deleted?: %s\n", err)
				break
			}
			fmt.Printf("Waiting for volume %s %s to be deleted\n", id, symVolumeID)
			time.Sleep(5 * time.Second)
		}
		if !deleted {
			return fmt.Errorf("Volume was never deleted: %s", id)
		}
	}
	t1 := time.Now()
	fmt.Printf("Background deletion time for %d volumes approximately: %v %v\n", nVols, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(5 * time.Second)
	return nil
}

func (f *feature) iMungeTheCSIVolumeId() error {
	str := []byte(f.volID)
	strlen := len(str)
	str[strlen-20] = 'z'
	f.volID = string(str)
	return nil
}

func (f *feature) iUnmungeTheCSIVolumeId() error {
	f.volID = f.volIDList[0]
	f.errs = make([]error, 0)
	return nil
}

func (f *feature) theVolumeIsNotDeleted() error {
	id := f.volIDList[0]
	splitid := strings.Split(id, "-")
	deviceID := splitid[len(splitid)-1]
	vol, err := f.pmaxClient.GetVolumeByID(f.symID, deviceID)
	if err != nil {
		return err
	}
	volumePrefix := os.Getenv(service.EnvClusterPrefix)
	expectedName := service.CsiVolumePrefix + volumePrefix + "-" + f.createVolumeRequest.Name
	if vol.VolumeIdentifier != expectedName {
		return fmt.Errorf("Expected VolumeIdentifier %s to match original requested name %s indicating volume was not deleted, but they differ",
			vol.VolumeIdentifier, expectedName)
	}
	return nil
}

func (f *feature) aGetCapacityRequest(srpID string) error {
	req := new(csi.GetCapacityRequest)
	params := make(map[string]string)
	params[service.StoragePoolParam] = srpID
	params[service.SymmetrixIDParam] = f.symID
	params[service.ServiceLevelParam] = f.serviceLevel
	req.Parameters = params
	req.VolumeCapabilities = f.capabilities
	f.getCapacityRequest = req
	return nil
}

func (f *feature) iCallGetCapacity() error {
	var err error
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	f.getCapacityResponse, err = client.GetCapacity(ctx, f.getCapacityRequest)
	if err != nil {
		fmt.Printf("GetCapacity %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("GetCapacity %d\n", f.getCapacityResponse.AvailableCapacity)
	}
	return nil
}

func (f *feature) aValidGetCapacityResponseIsReturned() error {
	resp := f.getCapacityResponse
	capacity := resp.AvailableCapacity
	if capacity <= 0 {
		return errors.New("Storage capacity is zero or less")
	}
	return nil
}

func (f *feature) anIdempotentTest() error {
	f.idempotentTest = true
	return nil
}

func FeatureContext(s *godog.Suite) {
	f := &feature{}
	s.Step(`^a Powermax service$`, f.aPowermaxService)
	s.Step(`^a basic block volume request "([^"]*)" "(\d+)"$`, f.aBasicBlockVolumeRequest)
	s.Step(`^I call CreateVolume$`, f.iCallCreateVolume)
	s.Step(`^when I call DeleteVolume$`, f.whenICallDeleteVolume)
	s.Step(`^there are no errors$`, f.thereAreNoErrors)
	s.Step(`^the error message should contain "([^"]*)"$`, f.theErrorMessageShouldContain)
	s.Step(`^a mount volume request "([^"]*)"$`, f.aMountVolumeRequest)
	s.Step(`^when I call PublishVolume$`, f.whenICallPublishVolume)
	s.Step(`^when I call UnpublishVolume$`, f.whenICallUnpublishVolume)
	s.Step(`^when I call PublishVolume "([^"]*)"$`, f.whenICallPublishVolume)
	s.Step(`^when I call UnpublishVolume "([^"]*)"$`, f.whenICallUnpublishVolume)
	s.Step(`^access type is "([^"]*)"$`, f.accessTypeIs)
	s.Step(`^max retries (\d+)$`, f.maxRetries)
	s.Step(`^a capability with voltype "([^"]*)" access "([^"]*)" fstype "([^"]*)"$`, f.aCapabilityWithVoltypeAccessFstype)
	s.Step(`^a volume request "([^"]*)" "(\d+)"$`, f.aVolumeRequest)
	s.Step(`^when I call NodePublishVolume "([^"]*)"$`, f.whenICallNodePublishVolume)
	s.Step(`^when I call NodeUnpublishVolume "([^"]*)"$`, f.whenICallNodeUnpublishVolume)
	s.Step(`^verify published volume with voltype "([^"]*)" access "([^"]*)" fstype "([^"]*)"$`, f.verifyPublishedVolumeWithVoltypeAccessFstype)
	s.Step(`^I call CreateSnapshot$`, f.iCallCreateSnapshot)
	s.Step(`^I call CreateSnapshotConsistencyGroup$`, f.iCallCreateSnapshotConsistencyGroup)
	s.Step(`^when I call DeleteAllVolumes$`, f.whenICallDeleteAllVolumes)
	s.Step(`^I call DeleteSnapshot$`, f.iCallDeleteSnapshot)
	s.Step(`^I call CreateVolumeFromSnapshot$`, f.iCallCreateVolumeFromSnapshot)
	s.Step(`^I call CreateManyVolumesFromSnapshot$`, f.iCallCreateManyVolumesFromSnapshot)
	s.Step(`^I call ListVolume$`, f.iCallListVolume)
	s.Step(`^a valid ListVolumeResponse is returned$`, f.aValidListVolumeResponseIsReturned)
	s.Step(`^I call ListSnapshot$`, f.iCallListSnapshot)
	s.Step(`^a valid ListSnapshotResponse is returned$`, f.aValidListSnapshotResponseIsReturned)
	s.Step(`^I create (\d+) volumes in parallel$`, f.iCreateVolumesInParallel)
	s.Step(`^I publish (\d+) volumes in parallel$`, f.iPublishVolumesInParallel)
	s.Step(`^I node publish (\d+) volumes in parallel$`, f.iNodePublishVolumesInParallel)
	s.Step(`^I node unpublish (\d+) volumes in parallel$`, f.iNodeUnpublishVolumesInParallel)
	s.Step(`^I unpublish (\d+) volumes in parallel$`, f.iUnpublishVolumesInParallel)
	s.Step(`^when I delete (\d+) volumes in parallel$`, f.whenIDeleteVolumesInParallel)
	s.Step(`^an alternate ServiceLevel "([^"]*)"$`, f.anAlternateServiceLevel)
	s.Step(`^I receive a valid volume$`, f.iReceiveAValidVolume)
	s.Step(`^all volumes are deleted successfully$`, f.allVolumesAreDeletedSuccessfully)
	s.Step(`^I use thick provisioning$`, f.iUseThickProvisioning)
	s.Step(`^I munge the CSI VolumeId$`, f.iMungeTheCSIVolumeId)
	s.Step(`^I unmunge the CSI VolumeId$`, f.iUnmungeTheCSIVolumeId)
	s.Step(`^the volume is not deleted$`, f.theVolumeIsNotDeleted)
	s.Step(`^a get capacity request "([^"]*)"$`, f.aGetCapacityRequest)
	s.Step(`^I call GetCapacity$`, f.iCallGetCapacity)
	s.Step(`^a valid GetCapacityResponse is returned$`, f.aValidGetCapacityResponseIsReturned)
	s.Step(`^an idempotent test$`, f.anIdempotentTest)
        s.Step(`^a volume request with file system "([^"]*)" fstype "([^"]*)" access "([^"]*)" voltype "([^"]*)"$`, f.aVolumeRequestFileSystem)
}
