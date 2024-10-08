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
package integration_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/cucumber/godog"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/service"
	csiext "github.com/dell/dell-csi-extensions/replication"
	pmax "github.com/dell/gopowermax/v2"
)

const (
	MaxRetries          = 10
	RetrySleepTime      = 10 * time.Second
	ShortSleepTime      = 3 * time.Second
	SleepTime           = 100 * time.Millisecond
	ApplicationName     = "CSI Driver Integration Tests"
	HostLimitName       = "HostLimitName"
	HostIOLimitMBSec    = "HostIOLimitMBSec"
	HostIOLimitIOSec    = "HostIOLimitIOSec"
	DynamicDistribution = "DynamicDistribution"
)

type feature struct {
	errs                        []error
	createVolumeRequest         *csi.CreateVolumeRequest
	createVolumeResponse        *csi.CreateVolumeResponse
	createRemoteVolumeResponse  *csiext.CreateRemoteVolumeResponse
	publishVolumeRequest        *csi.ControllerPublishVolumeRequest
	expandVolumeRequest         *csi.ControllerExpandVolumeRequest
	expandVolumeResponse        *csi.ControllerExpandVolumeResponse
	nodeExpandVolumeRequest     *csi.NodeExpandVolumeRequest
	nodeExpandVolumeResponse    *csi.NodeExpandVolumeResponse
	publishVolumeResponse       *csi.ControllerPublishVolumeResponse
	nodePublishVolumeRequest    *csi.NodePublishVolumeRequest
	listVolumesResponse         *csi.ListVolumesResponse
	listSnapshotsResponse       *csi.ListSnapshotsResponse
	capability                  *csi.VolumeCapability
	capabilities                []*csi.VolumeCapability
	getCapacityRequest          *csi.GetCapacityRequest
	getCapacityResponse         *csi.GetCapacityResponse
	volID                       string
	remotevolID                 string
	snapshotID                  string
	volIDList                   []string
	maxRetryCount               int
	symID                       string
	remotesymID                 string
	srpID                       string
	serviceLevel                string
	remoteServiceLevel          string
	localRdfGrpNo               string
	remoteRdfGrpNo              string
	localProtectedStorageGroup  string
	remoteProtectedStorageGroup string
	pmaxClient                  pmax.Pmax
	publishVolumeContextMap     map[string]map[string]string
	lockMutex                   sync.Mutex
	idempotentTest              bool
	snapIDList                  []string
	replicationPrefix           string
	replicationContextPrefix    string
	symmetrixIDParam            string
	remoteSRPID                 string
	setIOLimits                 bool
	autoSRDFEnabled             bool
}

func (f *feature) addError(err error) {
	f.errs = append(f.errs, err)
}

func (f *feature) aPowermaxService() error {
	f.errs = make([]error, 0)
	f.createVolumeRequest = nil
	f.createVolumeResponse = nil
	f.createRemoteVolumeResponse = nil
	f.publishVolumeRequest = nil
	f.publishVolumeResponse = nil
	f.expandVolumeRequest = nil
	f.expandVolumeResponse = nil
	f.nodeExpandVolumeRequest = nil
	f.nodeExpandVolumeResponse = nil
	f.listVolumesResponse = nil
	f.listSnapshotsResponse = nil
	f.getCapacityRequest = nil
	f.getCapacityResponse = nil
	f.capability = nil
	f.volID = ""
	f.snapshotID = ""
	f.localProtectedStorageGroup = ""
	f.remoteProtectedStorageGroup = ""
	f.symmetrixIDParam = "SYMID"
	f.volIDList = f.volIDList[:0]
	f.snapIDList = f.snapIDList[:0]
	f.maxRetryCount = MaxRetries
	f.publishVolumeContextMap = make(map[string]map[string]string)
	f.idempotentTest = false
	if f.pmaxClient == nil {
		var err error
		endpoint := os.Getenv("X_CSI_POWERMAX_ENDPOINT")
		if endpoint == "" {
			return fmt.Errorf("Cannot read X_CSI_POWERMAX_ENDPOINT")
		}
		f.pmaxClient, err = pmax.NewClientWithArgs(endpoint, ApplicationName, true, false)
		if err != nil {
			return fmt.Errorf("Cannot attach to pmax library: %s", err)
		}
		configConnect := &pmax.ConfigConnect{
			Endpoint: endpoint,
			Username: os.Getenv("X_CSI_POWERMAX_USER"),
			Password: os.Getenv("X_CSI_POWERMAX_PASSWORD"),
		}
		err = f.pmaxClient.Authenticate(context.Background(), configConnect)
		if err != nil {
			return err
		}
	}
	f.symID = os.Getenv("SYMID")
	f.srpID = os.Getenv("SRPID")
	f.remoteSRPID = os.Getenv("REMOTESRPID")
	f.remotesymID = os.Getenv("REMOTESYMID")
	f.serviceLevel = os.Getenv("SERVICELEVEL")
	f.remoteServiceLevel = os.Getenv("REMOTESERVICELEVEL")
	f.localRdfGrpNo = os.Getenv("LOCALRDFGROUP")
	f.remoteRdfGrpNo = os.Getenv("REMOTERDFGROUP")
	f.replicationPrefix = os.Getenv("X_CSI_REPLICATION_PREFIX")
	f.replicationContextPrefix = os.Getenv("X_CSI_REPLICATION_CONTEXT_PREFIX")
	f.setIOLimits = false
	f.autoSRDFEnabled = false
	symIDList := []string{f.remotesymID}
	// Add remote array to managed sym by csi driver
	if _, err := symmetrix.GetPowerMax(f.remotesymID); err != nil {
		err := symmetrix.Initialize(symIDList, f.pmaxClient)
		if err != nil {
			fmt.Printf("initialize remote array error: %s", err.Error())
			return err
		}
	}
	return nil
}

// A size of zero causes no capacity range to be specified.
func (f *feature) aBasicBlockVolumeRequest(_ string, size int64) error {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[service.SymmetrixIDParam] = f.symID
	params[service.ServiceLevelParam] = f.serviceLevel
	params[service.StoragePoolParam] = f.srpID
	params["thickprovisioning"] = "false"
	params[service.ApplicationPrefixParam] = "INT"
	params[service.CSIPVCNamespace] = "INT"
	req.Parameters = params
	now := time.Now()
	req.Name = fmt.Sprintf("pmax-Int%d", now.Nanosecond())
	if size > 0 {
		capacityRange := new(csi.CapacityRange)
		capacityRange.RequiredBytes = size * 1024 * 1024
		req.CapacityRange = capacityRange
	}
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
	if f.setIOLimits {
		req.Parameters[HostLimitName] = "HL1"
		req.Parameters[HostIOLimitMBSec] = "1000"
		req.Parameters[HostIOLimitIOSec] = "500"
		req.Parameters[DynamicDistribution] = "Always"
	}
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

func (f *feature) addsReplicationCapability(replicationMode string, namespace string) error {
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.RepEnabledParam)] = "true"
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.LocalRDFGroupParam)] = f.localRdfGrpNo
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.RemoteRDFGroupParam)] = f.remoteRdfGrpNo
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.RemoteSymIDParam)] = f.remotesymID
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.ReplicationModeParam)] = replicationMode
	f.createVolumeRequest.Parameters[service.CSIPVCNamespace] = namespace
	return nil
}

func (f *feature) addsAutoSRDFReplicationCapability(replicationMode string, namespace string) error {
	f.autoSRDFEnabled = true
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.RepEnabledParam)] = "true"
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.RemoteSymIDParam)] = f.remotesymID
	f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.ReplicationModeParam)] = replicationMode
	f.createVolumeRequest.Parameters[service.CSIPVCNamespace] = namespace
	return nil
}

func (f *feature) iCallCreateStorageProtectionGroup(replicationMode string) error {
	req := new(csiext.CreateStorageProtectionGroupRequest)
	params := make(map[string]string)
	if !f.autoSRDFEnabled {
		params[path.Join(f.replicationPrefix, service.LocalRDFGroupParam)] = f.localRdfGrpNo
		params[path.Join(f.replicationPrefix, service.RemoteRDFGroupParam)] = f.remoteRdfGrpNo
	}
	params[path.Join(f.replicationPrefix, service.RemoteSymIDParam)] = f.remotesymID
	params[path.Join(f.replicationPrefix, service.ReplicationModeParam)] = replicationMode
	req.Parameters = params
	req.VolumeHandle = f.volID
	var err error
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	resp, err := client.CreateStorageProtectionGroup(ctx, req)
	if err != nil {
		fmt.Printf("CreateStorageProtectionGroup error %s:\n", err.Error())
		f.addError(err)
	}
	fmt.Printf("CreateStorageProtectionGroup succeeded \n")
	if resp.LocalProtectionGroupAttributes[path.Join(f.replicationContextPrefix, f.symmetrixIDParam)] != f.symID {
		fmt.Printf("CreateStorageProtectionGroup validation failed")
		err := errors.New("validation failed for CreateStorageProtectionGroup, context prefix is missing")
		f.addError(err)
	}
	fmt.Printf("CreateStorageProtectionGroup validation succeeded \n")
	f.localProtectedStorageGroup = resp.LocalProtectionGroupId
	f.remoteProtectedStorageGroup = resp.RemoteProtectionGroupId
	return nil
}

func (f *feature) iCallCreateRemoteVolume(replicationMode string) error {
	req := new(csiext.CreateRemoteVolumeRequest)
	params := make(map[string]string)
	if !f.autoSRDFEnabled {
		params[path.Join(f.replicationPrefix, service.LocalRDFGroupParam)] = f.localRdfGrpNo
		params[path.Join(f.replicationPrefix, service.RemoteRDFGroupParam)] = f.remoteRdfGrpNo
	}
	params[path.Join(f.replicationPrefix, service.RemoteSymIDParam)] = f.remotesymID
	params[path.Join(f.replicationPrefix, service.ReplicationModeParam)] = replicationMode
	params[path.Join(f.replicationPrefix, service.RemoteServiceLevelParam)] = f.remoteServiceLevel
	params[path.Join(f.replicationPrefix, service.RemoteSRPParam)] = f.remoteSRPID
	req.Parameters = params
	req.VolumeHandle = f.volID
	var err error
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	f.createRemoteVolumeResponse, err = client.CreateRemoteVolume(ctx, req)
	if err != nil {
		fmt.Printf("CreateRemoteVolume error %s:\n", err.Error())
		f.addError(err)
	}
	remoteVolume := f.createRemoteVolumeResponse.RemoteVolume.GetVolumeId()
	fmt.Printf("Remote Volume retrieved %s:\n", remoteVolume)
	if f.createRemoteVolumeResponse.RemoteVolume.VolumeContext[path.Join(f.replicationContextPrefix, f.symmetrixIDParam)] != f.remotesymID {
		fmt.Printf("CreateRemoteVolume validation failed \n")
		err := errors.New("validation failed for CreateRemoteVolume, context prefix is missing")
		f.addError(err)
	}
	fmt.Printf("CreateRemoteVolume validation succeeded\n")
	f.volIDList = append(f.volIDList, remoteVolume)
	return nil
}

func (f *feature) iCallDeleteLocalStorageProtectionGroup() error {
	req := new(csiext.DeleteStorageProtectionGroupRequest)
	params := make(map[string]string)
	params[path.Join(f.replicationContextPrefix, service.SymmetrixIDParam)] = f.symID
	req.ProtectionGroupAttributes = params
	req.ProtectionGroupId = f.localProtectedStorageGroup
	var err error
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	_, err = client.DeleteStorageProtectionGroup(ctx, req)
	if err != nil {
		fmt.Printf("DeleteStorageProtectionGroup error %s:\n", err.Error())
		f.addError(err)
	}
	return nil
}

func (f *feature) iCallDeleteRemoteStorageProtectionGroup() error {
	req := new(csiext.DeleteStorageProtectionGroupRequest)
	params := make(map[string]string)
	params[path.Join(f.replicationContextPrefix, service.SymmetrixIDParam)] = f.remotesymID
	req.ProtectionGroupAttributes = params
	req.ProtectionGroupId = f.remoteProtectedStorageGroup
	var err error
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	_, err = client.DeleteStorageProtectionGroup(ctx, req)
	if err != nil {
		fmt.Printf("DeleteStorageProtectionGroup error %s:\n", err.Error())
		f.addError(err)
	}
	return nil
}

func (f *feature) iCallExecuteAction(action string) error {
	req := new(csiext.ExecuteActionRequest)
	params := make(map[string]string)
	repMode := f.createVolumeRequest.Parameters[path.Join(f.replicationPrefix, service.ReplicationModeParam)]
	params[path.Join(f.replicationContextPrefix, service.SymmetrixIDParam)] = f.symID
	params[path.Join(f.replicationContextPrefix, service.LocalRDFGroupParam)] = f.localRdfGrpNo
	params[path.Join(f.replicationContextPrefix, service.ReplicationModeParam)] = repMode
	req.ProtectionGroupId = f.localProtectedStorageGroup
	req.ProtectionGroupAttributes = params
	actionType := &csiext.ExecuteActionRequest_Action{
		Action: &csiext.Action{},
	}
	switch action {
	case "Failover":
		actionType.Action.ActionTypes = csiext.ActionTypes_FAILOVER_REMOTE
	case "Failback":
		actionType.Action.ActionTypes = csiext.ActionTypes_FAILBACK_LOCAL
	case "Establish":
		actionType.Action.ActionTypes = csiext.ActionTypes_ESTABLISH
	case "Resume":
		actionType.Action.ActionTypes = csiext.ActionTypes_RESUME
	case "Suspend":
		actionType.Action.ActionTypes = csiext.ActionTypes_SUSPEND
	}
	req.ActionTypes = actionType
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	_, err := client.ExecuteAction(ctx, req)
	if err != nil {
		fmt.Printf("ExecuteAction error %s:\n", err.Error())
		f.addError(err)
	}
	return nil
}

func (f *feature) iCallGetStorageProtectionGroupStatus(expectedStatus string) error {
	req := new(csiext.GetStorageProtectionGroupStatusRequest)
	params := make(map[string]string)
	params[path.Join(f.replicationContextPrefix, service.SymmetrixIDParam)] = f.symID
	req.ProtectionGroupId = f.localProtectedStorageGroup
	req.ProtectionGroupAttributes = params
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	resp, err := client.GetStorageProtectionGroupStatus(ctx, req)
	if err != nil {
		fmt.Printf("GetStorageProtectionGroupStatus error %s:\n", err.Error())
		f.addError(err)
	}
	currentStatus := resp.GetStatus().State.String()
	if currentStatus != expectedStatus {
		errmsg := fmt.Sprintf("GetStorageProtectionGroupStatus error, Expected %s != %s Current", expectedStatus, currentStatus)
		fmt.Printf("%s", errmsg)
		f.addError(errors.New(errmsg))
	}
	return nil
}

func (f *feature) accessTypeIs(arg1 string) error {
	switch arg1 {
	case "multi-writer":
		f.createVolumeRequest.VolumeCapabilities[0].AccessMode.Mode = csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER
	}
	return nil
}

func (f *feature) iCallCreateVolume() error {
	var err error
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	f.createVolumeResponse, err = client.CreateVolume(ctx, f.createVolumeRequest)
	if err != nil {
		fmt.Printf("CreateVolume error %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("CreateVolume %s (%s) %s %s\n", f.createVolumeResponse.GetVolume().VolumeContext["Name"],
			f.createVolumeResponse.GetVolume().VolumeId, f.createVolumeResponse.GetVolume().VolumeContext["CreationTime"], f.createVolumeResponse.GetVolume().VolumeContext["CapacityBytes"])
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
		fmt.Printf("CreateVolume retry: %s\n", err.Error())
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

func (f *feature) whenICallDeleteLocalVolume() error {
	err := f.deleteLocalVolume(f.createRemoteVolumeResponse.RemoteVolume.VolumeId)
	if err != nil {
		fmt.Printf("DeleteLocalVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("DeleteLocalVolume %s completed successfully\n", f.volID)
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
		if err == nil {
			// no need for retry
			break
		}
		fmt.Printf("DeleteVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) deleteLocalVolume(id string) error {
	ctx := context.Background()
	client := csiext.NewReplicationClient(grpcClient)
	delVolReq := new(csiext.DeleteLocalVolumeRequest)
	delVolReq.VolumeHandle = id
	var err error
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.DeleteLocalVolume(ctx, delVolReq)
		if err == nil {
			// no need for retry
			break
		}
		fmt.Printf("DeleteLocalVolume retry: %s\n", err.Error())
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
		}
		return fmt.Errorf("Unexpected error(s): %s", f.errs[0])
	}
	// We expect an error...
	if len(f.errs) == 0 {
		return errors.New("there were no errors but we expected: " + expected)
	}
	err0 := f.errs[0]
	if !strings.Contains(err0.Error(), expected) {
		return fmt.Errorf("Error %s does not contain the expected message: %s", err0.Error(), expected)
	}
	return nil
}

func (f *feature) aMountVolumeRequest(name string) error {
	req := f.getMountVolumeRequest(name)
	f.createVolumeRequest = req
	return nil
}

func (f *feature) getMountVolumeRequest(_ string) *csi.CreateVolumeRequest {
	req := new(csi.CreateVolumeRequest)
	params := make(map[string]string)
	params[service.SymmetrixIDParam] = f.symID
	params[service.ServiceLevelParam] = f.serviceLevel
	params[service.CSIPVCNamespace] = "INT"
	params[service.StoragePoolParam] = f.srpID
	req.Parameters = params
	now := time.Now()
	req.Name = fmt.Sprintf("pmax-Int%d", now.Nanosecond())
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

func (f *feature) aVolumeRequestFileSystem(name, fstype, access, voltype string) error {
	req := f.getVolumeRequestFileSystem(name, fstype, access, voltype)
	f.createVolumeRequest = req
	return nil
}

func (f *feature) getVolumeRequestFileSystem(_, fstype, access, voltype string) *csi.CreateVolumeRequest {
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
	req.Name = fmt.Sprintf("pmax-Int%d", now.Nanosecond())
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

func (f *feature) controllerPublishVolume(id string, _ string) error {
	var resp *csi.ControllerPublishVolumeResponse
	var err error
	req := f.getControllerPublishVolumeRequest()
	req.VolumeId = id
	req.NodeId = os.Getenv("X_CSI_POWERMAX_NODENAME")
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		resp, err = client.ControllerPublishVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("Controller PublishVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	f.lockMutex.Lock()
	defer f.lockMutex.Unlock()
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

func (f *feature) controllerUnpublishVolume(id string, _ string) error {
	var err error
	req := new(csi.ControllerUnpublishVolumeRequest)
	req.VolumeId = id
	req.NodeId = os.Getenv("X_CSI_POWERMAX_NODENAME")
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.ControllerUnpublishVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("ControllerUnpublishVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
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
	case "single-node-single-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_SINGLE_WRITER
	case "single-node-multi-writer":
		accessMode.Mode = csi.VolumeCapability_AccessMode_SINGLE_NODE_MULTI_WRITER
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
		targetPath := fmt.Sprintf("%s-datadir-%d", f.volID, now.Nanosecond())
		var fileMode os.FileMode
		fileMode = 0o777
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

func (f *feature) whenICallNodeStageVolume(_ string) error {
	pub := f.nodePublishVolumeRequest
	if pub == nil {
		pub = f.getNodePublishVolumeRequest()
	}
	req := &csi.NodeStageVolumeRequest{}
	req.VolumeId = f.volID
	req.PublishContext = pub.PublishContext
	req.StagingTargetPath = pub.TargetPath
	req.VolumeCapability = f.capability
	fmt.Printf("calling NodeStageVolume vol %s staging path %s\n", f.volID, f.nodePublishVolumeRequest.TargetPath)
	client := csi.NewNodeClient(grpcClient)
	ctx := context.Background()
	_, err := client.NodeStageVolume(ctx, req)
	return err
}

func (f *feature) nodeStageVolume(id string, path string) error {
	var err error
	pub := f.nodePublishVolumeRequest
	if pub == nil {
		pub = f.getNodePublishVolumeRequest()
	}
	req := &csi.NodeStageVolumeRequest{}
	req.VolumeId = id
	f.lockMutex.Lock()
	defer f.lockMutex.Unlock()
	req.PublishContext = f.publishVolumeContextMap[id]
	req.StagingTargetPath = path
	req.VolumeCapability = f.capability
	fmt.Printf("calling NodeStageVolume vol %s staging path %s\n", req.VolumeId, req.StagingTargetPath)
	client := csi.NewNodeClient(grpcClient)
	ctx := context.Background()
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.NodeStageVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("NodeStageVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) whenICallNodePublishVolume(_ string) error {
	err := f.nodePublishVolume(f.volID, "")
	if err != nil {
		fmt.Printf("NodePublishVolume failed: %s\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("NodePublishVolume completed successfully\n")
	}
	time.Sleep(ShortSleepTime)
	time.Sleep(30 * time.Second)
	return nil
}

func (f *feature) nodePublishVolume(id string, path string) error {
	var err error
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
	f.lockMutex.Lock()
	defer f.lockMutex.Unlock()
	req.PublishContext = f.publishVolumeContextMap[id]
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.NodePublishVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("NodePublishVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) whenICallNodeUnstageVolume(_ string) error {
	req := &csi.NodeUnstageVolumeRequest{}
	req.VolumeId = f.volID
	req.StagingTargetPath = f.nodePublishVolumeRequest.TargetPath
	fmt.Printf("calling NodeUnstageVolume vol %s targetpath %s\n", f.volID, f.nodePublishVolumeRequest.TargetPath)
	client := csi.NewNodeClient(grpcClient)
	ctx := context.Background()
	_, err := client.NodeUnstageVolume(ctx, req)
	return err
}

func (f *feature) nodeUnstageVolume(id string, path string) error {
	var err error
	req := &csi.NodeUnstageVolumeRequest{}
	req.VolumeId = id
	req.StagingTargetPath = path
	fmt.Printf("calling NodeUnstageVolume vol %s targetpath %s\n", id, path)
	client := csi.NewNodeClient(grpcClient)
	ctx := context.Background()
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.NodeUnstageVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("NodeUnstageVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) whenICallNodeUnpublishVolume(_ string) error {
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
	var err error
	req := &csi.NodeUnpublishVolumeRequest{VolumeId: id, TargetPath: path}
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.NodeUnpublishVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("NodeUnpublishVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) verifyPublishedVolumeWithVoltypeAccessFstype(voltype, _, fstype string) error {
	if len(f.errs) > 0 {
		fmt.Printf("Not verifying published volume because of previous error")
		return nil
	}
	var cmd *exec.Cmd
	if voltype == "mount" {
		cmd = exec.Command("/bin/bash", "-c", "mount | grep /tmp/datadir")
	} else if voltype == "block" {
		cmd = exec.Command("/bin/bash", "-c", "mount | grep /tmp/datafile")
	} else {
		return fmt.Errorf("unexpected volume type")
	}
	stdout, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}
	fmt.Printf("%s\n", stdout)
	if voltype == "mount" {
		// output: /dev/scinia on /tmp/datadir type xfs (rw,relatime,seclabel,attr2,inode64,noquota)
		if !strings.Contains(string(stdout), "/dev/scini") {
			return fmt.Errorf("Mount did not contain /dev/scini for scale-io")
		}
		if !strings.Contains(string(stdout), "/tmp/datadir") {
			return fmt.Errorf("Mount did not contain /tmp/datadir for type mount")
		}
		if !strings.Contains(string(stdout), fmt.Sprintf("type %s", fstype)) {
			return fmt.Errorf("Did not find expected fstype %s", fstype)
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
		f.snapIDList = append(f.snapIDList, f.snapshotID)
		fmt.Printf("createSnapshot: SnapshotId %s SourceVolumeId %s CreationTime %s\n",
			resp.Snapshot.SnapshotId, resp.Snapshot.SourceVolumeId, resp.Snapshot.CreationTime.AsTime().Format(time.RFC3339Nano))
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

func (f *feature) ICallDeleteAllSnapshots() error {
	for _, s := range f.snapIDList {
		f.snapshotID = s
		f.iCallDeleteSnapshot()
	}
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
			resp.Snapshot.SnapshotId, resp.Snapshot.SourceVolumeId, resp.Snapshot.CreationTime.AsTime().Format(time.RFC3339Nano))
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

func (f *feature) iCallLinkVolumeToSnapshot() error {
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

func (f *feature) iCallLinkVolumeToSnapshotAgain() error {
	req := f.createVolumeRequest
	source := &csi.VolumeContentSource_SnapshotSource{SnapshotId: f.snapshotID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Snapshot{Snapshot: source}
	fmt.Printf("Calling CreateVolume with snapshot source")

	_ = f.createAVolume(req, "single CreateVolume from Snap")
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iCallLinkVolumeToVolume() error {
	req := f.createVolumeRequest
	req.Name = "volFromVol-" + req.Name
	source := &csi.VolumeContentSource_VolumeSource{VolumeId: f.volID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: source}
	fmt.Printf("Calling CreateVolume with volume source")

	_ = f.createAVolume(req, "single CreateVolume from Volume")
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iCallLinkVolumeToVolumeAgain() error {
	req := f.createVolumeRequest
	source := &csi.VolumeContentSource_VolumeSource{VolumeId: f.volID}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: source}
	fmt.Printf("Calling CreateVolume with volume source")

	_ = f.createAVolume(req, "single CreateVolume from Volume")
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
		ts := entry.GetSnapshot().CreationTime.AsTime().Format(time.RFC3339Nano)
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
	nerrors, err := f.waitOnParallelResponses("Controller publish", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
	}

	t1 := time.Now()
	fmt.Printf("Controller publish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(4 * SleepTime)
	return nil
}

// waitOnParallelResponses waits on the responses from threads and returns the number of errors and/or and error
func (f *feature) waitOnParallelResponses(method string, nVols int, _ []string, done chan bool, errchan chan error) (int, error) {
	nerrors := 0
	for i := 0; i < nVols; i++ {
		if f.volIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return nerrors, errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("%s received error: %s\n", method, err.Error())
			f.addError(err)
			nerrors++
		}
	}
	return nerrors, nil
}

func (f *feature) iNodeStageVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	// make a staging directory for each
	for i := 0; i < nVols; i++ {
		dataDirName := fmt.Sprintf("/tmp/stagedir%d", i)
		fmt.Printf("Creating %s\n", dataDirName)
		var fileMode os.FileMode
		fileMode = 0o777
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
		dataDirName := fmt.Sprintf("/tmp/stagedir%d", i)
		go func(id string, dataDirName string, done chan bool, errchan chan error) {
			err := f.nodeStageVolume(id, dataDirName)
			done <- true
			errchan <- err
		}(id, dataDirName, done, errchan)
	}

	// Wait for responses
	nerrors, err := f.waitOnParallelResponses("Node stage", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
	}

	t1 := time.Now()
	fmt.Printf("Node stage volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(2 * SleepTime)
	return nil
}

func (f *feature) iNodePublishVolumesInParallel(nVols int) error {
	nvols := len(f.volIDList)
	// make a data directory for each
	for i := 0; i < nVols; i++ {
		dataDirName := fmt.Sprintf("/tmp/datadir%d", i)
		fmt.Printf("Checking %s\n", dataDirName)
		var fileMode os.FileMode
		fileMode = 0o777
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
	nerrors, err := f.waitOnParallelResponses("Node publish", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
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
	nerrors, err := f.waitOnParallelResponses("Node unpublish", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
	}

	t1 := time.Now()
	fmt.Printf("Node unpublish volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iNodeUnstageVolumesInParallel(nVols int) error {
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
		dataDirName := fmt.Sprintf("/tmp/stagedir%d", i)
		go func(id string, dataDirName string, done chan bool, errchan chan error) {
			err := f.nodeUnstageVolume(id, dataDirName)
			done <- true
			errchan <- err
		}(id, dataDirName, done, errchan)
	}

	// Wait for responses
	nerrors, err := f.waitOnParallelResponses("Node unstage", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
	}

	t1 := time.Now()
	fmt.Printf("Node unstage volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
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

	// Wait for responses
	nerrors, err := f.waitOnParallelResponses("Controller unpublish", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
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

	// Wait for responses
	nerrors, err := f.waitOnParallelResponses("Delete volume", nVols, f.volIDList, done, errchan)
	if err != nil {
		return err
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
		symID := idComponents[len(idComponents)-2]
		fmt.Printf("Waiting for volume %s %s to be deleted\n", id, symVolumeID)
		max := 40 * nVols
		if 300 > max {
			max = 300
		}
		for i := 0; i < max; i++ {
			vol, err := f.pmaxClient.GetVolumeByID(context.Background(), symID, symVolumeID)
			if vol == nil {
				deleted = strings.Contains(err.Error(), "Could not find")
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

func (f *feature) iMungeTheCSIVolumeID() error {
	str := []byte(f.volID)
	strlen := len(str)
	str[strlen-20] = 'z'
	f.volID = string(str)
	return nil
}

func (f *feature) iUnmungeTheCSIVolumeID() error {
	f.volID = f.volIDList[0]
	f.errs = make([]error, 0)
	return nil
}

func (f *feature) theVolumeIsNotDeleted() error {
	id := f.volIDList[0]
	splitid := strings.Split(id, "-")
	deviceID := splitid[len(splitid)-1]
	vol, err := f.pmaxClient.GetVolumeByID(context.Background(), f.symID, deviceID)
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
		fmt.Printf("GetCapacity %d and MaxVolumeSize %v\n", f.getCapacityResponse.AvailableCapacity, f.getCapacityResponse.MaximumVolumeSize)
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

func (f *feature) theVolumeSizeIs(expectedVolSize string) error {
	if len(f.errs) > 0 {
		return nil
	}
	if f.createVolumeResponse == nil {
		return fmt.Errorf("expected CreateVolume response but there was none")
	}
	capGB := f.createVolumeResponse.GetVolume().VolumeContext["CapacityGB"]
	if capGB != expectedVolSize {
		return fmt.Errorf("Expected %s GB but got %s", expectedVolSize, capGB)
	}
	return nil
}

func (f *feature) iCreateSnapshotsInParallel(nSnaps int) error {
	idchan := make(chan string, nSnaps)
	errchan := make(chan error, nSnaps)
	t0 := time.Now()
	// Send requests
	for i := 0; i < nSnaps; i++ {
		name := fmt.Sprintf("Scale_Test_Snap%d", i)
		go func(name string, idchan chan string, errchan chan error) {
			var resp *csi.CreateSnapshotResponse
			var err error
			req := &csi.CreateSnapshotRequest{
				SourceVolumeId: f.volID,
				Name:           name,
			}
			if req != nil {
				resp, err = f.createSnapshot(req)
				if resp != nil {
					fmt.Println("response for createSnapshot", resp)
					idchan <- resp.GetSnapshot().GetSnapshotId()
				} else {
					fmt.Println("response for createSnapshot is nil")
					idchan <- ""
				}
			}
			errchan <- err
		}(name, idchan, errchan)
	}
	// wait on complete, collecting ids and errors
	nerrors := 0
	for i := 0; i < nSnaps; i++ {
		var id string
		var err error
		id = <-idchan
		if id != "" {
			f.snapIDList = append(f.snapIDList, id)
		}
		err = <-errchan
		if err != nil {
			fmt.Printf("create snapshot received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	if len(f.snapIDList) > nSnaps {
		f.snapIDList = f.snapIDList[0:nSnaps]
	}
	fmt.Printf("Create snapshot time for %d snap %d errors: %v %v\n", nSnaps, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nSnaps))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iCreateVolumesFromSnapshotInParallel(nVols int) error {
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
			source := &csi.VolumeContentSource_SnapshotSource{SnapshotId: f.snapshotID}
			req.VolumeContentSource = new(csi.VolumeContentSource)
			req.VolumeContentSource.Type = &csi.VolumeContentSource_Snapshot{Snapshot: source}
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

func (f *feature) iCreateVolumesFromVolumeInParallel(nVols int) error {
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
			source := &csi.VolumeContentSource_VolumeSource{VolumeId: f.volID}
			req.VolumeContentSource = new(csi.VolumeContentSource)
			req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: source}
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

func (f *feature) iCallDeleteSnapshotInParallel() error {
	nSnaps := len(f.snapIDList)
	fmt.Printf("The number of snapshots to delete%d ", nSnaps)
	done := make(chan bool, nSnaps)
	errchan := make(chan error, nSnaps)
	t0 := time.Now()
	// Send requests
	for index := 0; index < nSnaps; index++ {
		go func(snapID string, done chan bool, errchan chan error) {
			err := f.deleteSnapshot(snapID)
			done <- true
			errchan <- err
		}(f.snapIDList[index], done, errchan)
	}
	// Wait on complete, collecting ids and errors
	nerrors := 0
	for i := 0; i < nSnaps; i++ {
		if f.snapIDList[i] == "" {
			continue
		}
		finished := <-done
		if !finished {
			return errors.New("premature completion")
		}
		err := <-errchan
		if err != nil {
			fmt.Printf("Delete snapshot received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Delete Snapshot time for %d Snaphots %d errors: %v %v\n", nSnaps, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nSnaps))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iCallCreateSnapshotOnNewVolume() error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	volID := f.volIDList[len(f.volIDList)-1]
	req := &csi.CreateSnapshotRequest{
		SourceVolumeId: volID,
		Name:           "snapshot-0eb5347a-0000-11e9-ab1c-005056a64ad3",
	}
	resp, err := client.CreateSnapshot(ctx, req)
	if err != nil {
		fmt.Printf("CreateSnapshot on new volume returned error: %s\n", err.Error())
		f.addError(err)
	} else {
		f.snapshotID = resp.Snapshot.SnapshotId
		f.snapIDList = append(f.snapIDList, f.snapshotID)
		fmt.Printf("createSnapshot: SnapshotId %s SourceVolumeId %s CreationTime %s\n",
			resp.Snapshot.SnapshotId, resp.Snapshot.SourceVolumeId, resp.Snapshot.CreationTime.AsTime().Format(time.RFC3339Nano))
	}
	time.Sleep(RetrySleepTime)
	return nil
}

func (f *feature) iCallCreateVolumeFromNewVolume() error {
	req := f.createVolumeRequest
	req.Name = "volFromVol-" + req.Name
	// Take the latest VolumeId created from f.volIDList
	source := &csi.VolumeContentSource_VolumeSource{VolumeId: f.volIDList[len(f.volIDList)-1]}
	req.VolumeContentSource = new(csi.VolumeContentSource)
	req.VolumeContentSource.Type = &csi.VolumeContentSource_Volume{Volume: source}
	fmt.Printf("Calling CreateVolume with new volume source")
	_ = f.createAVolume(req, "single CreateVolume from new Volume")
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) createSnapshot(req *csi.CreateSnapshotRequest) (*csi.CreateSnapshotResponse, error) {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	var resp *csi.CreateSnapshotResponse
	var err error
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		resp, err = client.CreateSnapshot(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("Create Snapshot retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	return resp, err
}

func (f *feature) deleteSnapshot(snapID string) error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	req := &csi.DeleteSnapshotRequest{
		SnapshotId: snapID,
	}
	var err error
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		_, err = client.DeleteSnapshot(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("DeleteSnapshot ID %s: retry %s\n", req.SnapshotId, err.Error())
		time.Sleep(RetrySleepTime)
	}
	return err
}

func (f *feature) iCreateASnapshotPerVolumeInParallel() error {
	nSnaps := len(f.volIDList) // nSnaps is the no. of vols present, on which we make 1 snap each
	idchan := make(chan string, nSnaps)
	errchan := make(chan error, nSnaps)
	t0 := time.Now()
	// Send requests
	for i := 0; i < nSnaps; i++ {
		name := fmt.Sprintf("Scale_Test_Snap%d", i)
		go func(volID, name string, idchan chan string, errchan chan error) {
			var resp *csi.CreateSnapshotResponse
			var err error
			req := &csi.CreateSnapshotRequest{
				SourceVolumeId: volID,
				Name:           name,
			}
			if req != nil {
				resp, err = f.createSnapshot(req)
				if resp != nil {
					fmt.Println("response for createSnapshot", resp)
					idchan <- resp.GetSnapshot().GetSnapshotId()
				} else {
					fmt.Println("response for createSnapshot is nil")
					idchan <- ""
				}
			}
			errchan <- err
		}(f.volIDList[i], name, idchan, errchan)
	}
	// wait on complete, collecting ids and errors
	nerrors := 0
	for i := 0; i < nSnaps; i++ {
		var id string
		var err error
		id = <-idchan
		if id != "" {
			f.snapIDList = append(f.snapIDList, id)
		}
		err = <-errchan
		if err != nil {
			fmt.Printf("create snapshot received error: %s\n", err.Error())
			f.addError(err)
			nerrors++
		}
	}
	t1 := time.Now()
	if len(f.snapIDList) > nSnaps {
		f.snapIDList = f.snapIDList[0:nSnaps]
	}
	fmt.Printf("Create snapshot time for %d snap %d errors: %v %v\n", nSnaps, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nSnaps))
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) iCallDeleteSnapshotAndCreateSnapshotInParallel() error {
	errChan := make(chan error, 2)
	go func() {
		err := f.iCallCreateSnapshot()
		errChan <- err
	}()
	go func() {
		err := f.iCallDeleteSnapshot()
		errChan <- err
	}()
	for i := 0; i < 2; i++ {
		err := <-errChan
		if err != nil {
			f.addError(err)
		}
	}
	return nil
}

func (f *feature) iCheckIfVolumeExist() error {
	_, _, devID, err := f.parseCsiID(f.volID)
	if err != nil {
		fmt.Printf("volID: %s malformed. Error: %s:\n", f.volID, err.Error())
	}
	volume, err := f.pmaxClient.GetVolumeByID(context.Background(), f.symID, devID)
	if err != nil {
		fmt.Printf("GetVolumeByID %s:\n", err.Error())
		f.addError(err)
	} else {
		if strings.Contains(volume.VolumeIdentifier, "DS") {
			fmt.Printf("Volume (%s) exist and is tagged to delete (%s)\n", volume.VolumeID, volume.VolumeIdentifier)
		} else {
			f.addError(fmt.Errorf("volume (%s) exist and Soft-Delete Failed", volume.VolumeID))
		}
	}
	return nil
}

func (f *feature) iCheckIfVolumeIsDeleted() error {
	volName, _, devID, err := f.parseCsiID(f.volID)
	if err != nil {
		fmt.Printf("volID: %s malformed. Error: %s:\n", f.volID, err.Error())
	}
	return f.checkIfVolumeIsDeleted(volName, f.symID, devID)
}

func (f *feature) checkIfVolumeIsDeleted(volName, symID, devID string) error {
	vol, err := f.pmaxClient.GetVolumeByID(context.Background(), symID, devID)
	if err != nil {
		fmt.Printf("GetVolumeByID : %s", err.Error())
		fmt.Println(", Volume is successfully deleted")
	} else {
		if volName+"-DS" == vol.VolumeIdentifier {
			f.addError(errors.New("Volume still exist"))
			fmt.Printf("volume (%s) exist", vol.VolumeID)
		} else {
			fmt.Println("Volume is successfully deleted")
		}
	}
	return nil
}

func (f *feature) iDeleteASnapshot() error {
	f.snapshotID = f.snapIDList[0]
	f.iCallDeleteSnapshot()
	if len(f.snapIDList) > 1 {
		f.snapIDList = f.snapIDList[1:]
	}
	return nil
}

func (f *feature) parseCsiID(csiID string) (
	volName string, arrayID string, devID string, err error,
) {
	if csiID == "" {
		err = fmt.Errorf("A Volume ID is required for the request")
		return
	}
	// get the Device ID and Array ID
	idComponents := strings.Split(csiID, "-")
	// Protect against mal-formed component
	numOfIDComponents := len(idComponents)
	if numOfIDComponents < 3 {
		// Not well formed
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	// Device ID is the last token
	devID = idComponents[numOfIDComponents-1]
	// Array ID is the second to last token
	arrayID = idComponents[numOfIDComponents-2]

	// The two here is for two dashes - one at front of array ID and one between the Array ID and Device ID
	lengthOfTrailer := len(devID) + len(arrayID) + 2
	length := len(csiID)
	if length <= lengthOfTrailer+2 {
		// Not well formed...
		err = fmt.Errorf("The CSI ID %s is not formed correctly", csiID)
		return
	}
	// calculate the volume name, which is everything before the array ID
	volName = csiID[0 : length-lengthOfTrailer]
	return
}

func (f *feature) whenICallExpandVolumeToCylinders(nCYL int64) error {
	err := f.controllerExpandVolume(f.volID, nCYL)
	if err != nil {
		fmt.Printf("ControllerExpandVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("ControllerExpandVolume completed successfully\n")
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) controllerExpandVolume(volID string, nCYL int64) error {
	const cylinderSizeInBytes = 1966080
	var resp *csi.ControllerExpandVolumeResponse
	var err error
	var req *csi.ControllerExpandVolumeRequest
	if nCYL != 0 {
		req = &csi.ControllerExpandVolumeRequest{
			VolumeId:      volID,
			CapacityRange: &csi.CapacityRange{RequiredBytes: nCYL * cylinderSizeInBytes},
		}
	} else {
		req = &csi.ControllerExpandVolumeRequest{
			VolumeId: volID,
		}
	}
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		resp, err = client.ControllerExpandVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("Controller ExpandVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	f.expandVolumeResponse = resp
	return err
}

func (f *feature) whenICallNodeExpandVolume() error {
	nodePublishReq := f.nodePublishVolumeRequest
	if nodePublishReq == nil {
		err := fmt.Errorf("Volume is not stage, nodePublishVolumeRequest not found")
		return err
	}
	err := f.nodeExpandVolume(f.volID, nodePublishReq.TargetPath)
	if err != nil {
		fmt.Printf("NodeExpandVolume %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("NodeExpandVolume completed successfully\n")
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) nodeExpandVolume(volID, volPath string) error {
	var resp *csi.NodeExpandVolumeResponse
	var err error
	req := &csi.NodeExpandVolumeRequest{
		VolumeId:   volID,
		VolumePath: volPath,
	}
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		resp, err = client.NodeExpandVolume(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("Node ExpandVolume retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	f.nodeExpandVolumeResponse = resp
	return err
}

func (f *feature) iCallNodeGetVolumeStats() error {
	nodePublishReq := f.nodePublishVolumeRequest
	if nodePublishReq == nil {
		err := fmt.Errorf("Volume is not stage, nodePublishVolumeRequest not found")
		return err
	}
	err := f.nodeGetVolumeStats(f.volID, nodePublishReq.TargetPath)
	if err != nil {
		fmt.Printf("NodeGetVolumeStats %s:\n", err.Error())
		f.addError(err)
	} else {
		fmt.Printf("NodeGetVolumeStats completed successfully\n")
	}
	time.Sleep(SleepTime)
	return nil
}

func (f *feature) nodeGetVolumeStats(volID string, volPath string) error {
	var resp *csi.NodeGetVolumeStatsResponse
	var err error
	req := &csi.NodeGetVolumeStatsRequest{
		VolumeId:   volID,
		VolumePath: volPath,
	}
	ctx := context.Background()
	client := csi.NewNodeClient(grpcClient)
	// Retry loop to deal with API being overwhelmed
	for i := 0; i < f.maxRetryCount; i++ {
		resp, err = client.NodeGetVolumeStats(ctx, req)
		if err == nil {
			break
		}
		fmt.Printf("NodeGetVolumeStats retry: %s\n", err.Error())
		time.Sleep(RetrySleepTime)
	}
	fmt.Printf("NodeGetVolumeStats: (%v)", resp)
	return err
}

func (f *feature) iCallControllerGetVolume() error {
	ctx := context.Background()
	client := csi.NewControllerClient(grpcClient)
	req := &csi.ControllerGetVolumeRequest{
		VolumeId: f.volID,
	}
	resp, err := client.ControllerGetVolume(ctx, req)
	if err != nil {
		fmt.Printf("ControllerGetVolume returned error: %s\n", err.Error())
		f.addError(err)
	}
	fmt.Printf("ControllerGetVolume: Volume %v VolumeCondition %s PublishedNodeIDs %v\n",
		resp.Volume, resp.Status.VolumeCondition, resp.Status.PublishedNodeIds)
	time.Sleep(RetrySleepTime)
	return nil
}

func (f *feature) iHaveSetHostIOLimitsOnTheStorageGroup() error {
	f.setIOLimits = true
	return nil
}

func FeatureContext(s *godog.ScenarioContext) {
	f := &feature{}
	s.Step(`^a Powermax service$`, f.aPowermaxService)
	s.Step(`^a basic block volume request "([^"]*)" "(\d+)"$`, f.aBasicBlockVolumeRequest)
	s.Step(`^adds replication capability with mode "([^"]*)" namespace "([^"]*)"$`, f.addsReplicationCapability)
	s.Step(`^adds auto SRDFG replication capability with mode "([^"]*)" namespace "([^"]*)"$`, f.addsAutoSRDFReplicationCapability)
	s.Step(`^I call CreateVolume$`, f.iCallCreateVolume)
	s.Step(`^when I call DeleteVolume$`, f.whenICallDeleteVolume)
	s.Step(`^there are no errors$`, f.thereAreNoErrors)
	s.Step(`^the error message should contain "([^"]*)"$`, f.theErrorMessageShouldContain)
	s.Step(`^a mount volume request "([^"]*)"$`, f.aMountVolumeRequest)
	s.Step(`^when I call PublishVolume$`, f.whenICallPublishVolume)
	s.Step(`^when I call UnpublishVolume$`, f.whenICallUnpublishVolume)
	s.Step(`^when I call PublishVolume "([^"]*)"$`, f.whenICallPublishVolume)
	s.Step(`^when I call UnpublishVolume "([^"]*)"$`, f.whenICallUnpublishVolume)
	s.Step(`^when I call ExpandVolume to (\d+) cylinders$$`, f.whenICallExpandVolumeToCylinders)
	s.Step(`^when I call NodeExpandVolume$`, f.whenICallNodeExpandVolume)
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
	s.Step(`^I call LinkVolumeToSnapshot$`, f.iCallLinkVolumeToSnapshot)
	s.Step(`^I call LinkVolumeToSnapshotAgain$`, f.iCallLinkVolumeToSnapshotAgain)
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
	sf.allVolumesAreDeletedSuccessfully.Step(`^I munge the CSI VolumeId$`, f.iMungeTheCSIVolumeID)
	s.Step(`^I unmunge the CSI VolumeId$`, f.iUnmungeTheCSIVolumeID)
	s.Step(`^the volume is not deleted$`, f.theVolumeIsNotDeleted)
	s.Step(`^a get capacity request "([^"]*)"$`, f.aGetCapacityRequest)
	s.Step(`^I call GetCapacity$`, f.iCallGetCapacity)
	s.Step(`^a valid GetCapacityResponse is returned$`, f.aValidGetCapacityResponseIsReturned)
	s.Step(`^an idempotent test$`, f.anIdempotentTest)
	s.Step(`^a volume request with file system "([^"]*)" fstype "([^"]*)" access "([^"]*)" voltype "([^"]*)"$`, f.aVolumeRequestFileSystem)
	s.Step(`^when I call NodeStageVolume "([^"]*)"$`, f.whenICallNodeStageVolume)
	s.Step(`^when I call NodeUnstageVolume "([^"]*)"$`, f.whenICallNodeUnstageVolume)
	s.Step(`^I node stage (\d+) volumes in parallel$`, f.iNodeStageVolumesInParallel)
	s.Step(`^I node unstage (\d+) volumes in parallel$`, f.iNodeUnstageVolumesInParallel)
	s.Step(`^the volume size is "([^"]*)"$`, f.theVolumeSizeIs)
	s.Step(`^I call LinkVolumeToVolume$`, f.iCallLinkVolumeToVolume)
	s.Step(`^I call LinkVolumeToVolumeAgain$`, f.iCallLinkVolumeToVolumeAgain)
	s.Step(`^I create (\d+) snapshots in parallel$`, f.iCreateSnapshotsInParallel)
	s.Step(`^I create (\d+) volumes from snapshot in parallel$`, f.iCreateVolumesFromSnapshotInParallel)
	s.Step(`^I create (\d+) volumes from volume in parallel$`, f.iCreateVolumesFromVolumeInParallel)
	s.Step(`^I call DeleteSnapshot in parallel$`, f.iCallDeleteSnapshotInParallel)
	s.Step(`^I call DeleteAllSnapshots$`, f.ICallDeleteAllSnapshots)
	s.Step(`^I call CreateSnapshot on new volume$`, f.iCallCreateSnapshotOnNewVolume)
	s.Step(`^I call CreateVolume from new volume$`, f.iCallCreateVolumeFromNewVolume)
	s.Step(`^I call DeleteSnapshot and CreateSnapshot in parallel$`, f.iCallDeleteSnapshotAndCreateSnapshotInParallel)
	s.Step(`^I call CreateStorageProtectionGroup with mode "([^"]*)"$`, f.iCallCreateStorageProtectionGroup)
	s.Step(`^I call CreateRemoteVolume with mode "([^"]*)"$`, f.iCallCreateRemoteVolume)
	s.Step(`^I call Delete LocalStorageProtectionGroup$`, f.iCallDeleteLocalStorageProtectionGroup)
	s.Step(`^I call Delete RemoteStorageProtectionGroup$`, f.iCallDeleteRemoteStorageProtectionGroup)
	s.Step(`^I call ExecuteAction with action "([^"]*)"$`, f.iCallExecuteAction)
	s.Step(`^I call GetStorageProtectionGroupStatus to get "([^"]*)"$`, f.iCallGetStorageProtectionGroupStatus)
	s.Step(`^I check if volume exist$`, f.iCheckIfVolumeExist)
	s.Step(`^I check if volume is deleted$`, f.iCheckIfVolumeIsDeleted)
	s.Step(`^I delete a snapshot$`, f.iDeleteASnapshot)
	s.Step(`^I create a snapshot per volume in parallel$`, f.iCreateASnapshotPerVolumeInParallel)
	s.Step(`^when I call ControllerGetVolume$`, f.iCallControllerGetVolume)
	s.Step(`^when I call NodeGetVolumeStats$`, f.iCallNodeGetVolumeStats)
	s.Step(`^I have SetHostIOLimits on the storage group$`, f.iHaveSetHostIOLimitsOnTheStorageGroup)
	s.Step(`^when I call DeleteLocalVolume`, f.whenICallDeleteLocalVolume)
}
