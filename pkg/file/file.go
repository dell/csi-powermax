/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

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

package file

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gofsutil"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// These map to the above fields in the form of HTTP header names.
const (
	ServiceLevelParam                    = "ServiceLevel"
	CapacityMiB                          = "CapacityMiB"
	MiBSizeInBytes                       = 1048576
	StoragePoolParam                     = "SRP"
	CSIPVCNamespace                      = "csi.storage.k8s.io/pvc/namespace"
	CSIPersistentVolumeName              = "csi.storage.k8s.io/pv/name"
	CSIPersistentVolumeClaimName         = "csi.storage.k8s.io/pvc/name"
	HeaderPersistentVolumeName           = "x-csi-pv-name"
	HeaderPersistentVolumeClaimName      = "x-csi-pv-claimname"
	HeaderPersistentVolumeClaimNamespace = "x-csi-pv-namespace"
	QueryNASServerID                     = "nas_server_id"
	QueryName                            = "name"
	AllowRootParam                       = "allowRoot"
	NASServerIDParam                     = "NASServerID"
	NASServerNameParam                   = "NASServerName"
	QueryFileSystemID                    = "file_system_id"
	NFSExportPathParam                   = "NFSExportPath"
	NFSExportIDParam                     = "NFSExportID"
)

// CreateFileSystem creates a file system
func CreateFileSystem(ctx context.Context, reqID string, accessibility *csi.TopologyRequirement, params map[string]string, symID, storagePoolID, serviceLevel, nasServerName, fileSystemIdentifier, allowRoot string, sizeInMiB int64, pmaxClient pmax.Pmax) (*csi.CreateVolumeResponse, error) {
	// Get NAS Server ID from NASServer Name
	nasServerList, err := pmaxClient.GetNASServerList(ctx, symID, types.QueryParams{QueryName: nasServerName})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "can not fetch NAS server list for %s : %s", symID, err.Error())
	}
	if len(nasServerList.Entries) < 1 {
		return nil, status.Errorf(codes.Internal, "No NAS server found with name %s on %s", nasServerName, symID)
	}
	nasServerID := nasServerList.Entries[0].ID
	log.Infof("found NASServerID: %s, for NASServer name: %s", nasServerID, nasServerName)
	// log all parameters used in CreateFileSystem call
	fields := map[string]interface{}{
		"SymmetrixID":                        symID,
		"SRP":                                storagePoolID,
		"Accessibility":                      accessibility,
		"fileSystemIdentifier":               fileSystemIdentifier,
		"requiredMiB":                        sizeInMiB,
		"CSIRequestID":                       reqID,
		NASServerIDParam:                     nasServerID,
		NASServerNameParam:                   nasServerName,
		"ServiceLevel":                       serviceLevel,
		AllowRootParam:                       allowRoot,
		HeaderPersistentVolumeName:           params[CSIPersistentVolumeName],
		HeaderPersistentVolumeClaimName:      params[CSIPersistentVolumeClaimName],
		HeaderPersistentVolumeClaimNamespace: params[CSIPVCNamespace],
	}
	log.WithFields(fields).Info("Executing CreateVolume: FileSystem with following fields")

	var fileSystem *types.FileSystem
	var alreadyExist bool
	// Check if already exist
	alreadyExist, fileSystem, err = checkIfFileSystemExist(ctx, symID, nasServerID, fileSystemIdentifier, sizeInMiB, pmaxClient)
	if err != nil {
		return nil, err
	}
	if !alreadyExist {
		// Create File System
		fileSystem, err = pmaxClient.CreateFileSystem(ctx, symID, fileSystemIdentifier, nasServerID, serviceLevel, sizeInMiB)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "can not create filesystem for %s : %s", fileSystemIdentifier, err.Error())
		}
	}
	// return csi response
	// Formulate the return response
	fsID := fileSystem.ID
	volID := fmt.Sprintf("%s-%s-%s", fileSystemIdentifier, symID, fsID)
	// Set the volume context
	attributes := map[string]string{
		NASServerIDParam:   nasServerID,
		NASServerNameParam: nasServerName,
		ServiceLevelParam:  serviceLevel,
		StoragePoolParam:   storagePoolID,
		AllowRootParam:     allowRoot,
		CapacityMiB:        strconv.FormatInt(fileSystem.SizeTotal, 10),
		// Format the time output
		"CreationTime": time.Now().Format("20060102150405"),
	}
	volResp := &csi.Volume{
		VolumeId:      volID,
		CapacityBytes: sizeInMiB * MiBSizeInBytes,
		VolumeContext: attributes,
	}
	if accessibility != nil {
		volResp.AccessibleTopology = accessibility.Preferred
	}
	csiResp := &csi.CreateVolumeResponse{
		Volume: volResp,
	}
	log.WithFields(fields).Infof("Created volume : fileSystem with ID: %s", volResp.VolumeId)
	return csiResp, nil
}

func checkIfFileSystemExist(ctx context.Context, symID, nasServerID, fileSystemName string, sizeInMiB int64, pmaxClient pmax.Pmax) (bool, *types.FileSystem, error) {
	query := types.QueryParams{QueryName: fileSystemName, QueryNASServerID: nasServerID}
	fileSystemIter, err := pmaxClient.GetFileSystemList(ctx, symID, query)
	if err != nil {
		return false, nil, status.Errorf(codes.Internal, "can not fetch filesystem list for %s : %s", fileSystemName, err.Error())
	}
	if fileSystemIter.Count == 0 {
		// Not found
		return false, nil, nil
	}
	fsID := fileSystemIter.ResultList.FileSystemList[0].ID
	fileSystem, err := pmaxClient.GetFileSystemByID(ctx, symID, fsID)
	if err != nil {
		return false, nil, status.Errorf(codes.Internal, "can not fetch filesystem for name: %s, ID: %s : %s", fileSystemName, fsID, err.Error())
	}
	if fileSystem.SizeTotal != sizeInMiB {
		return false, nil, status.Errorf(codes.AlreadyExists, "A fileSystem with the same name exists but has a different size than required.")
	}
	return true, fileSystem, nil
}

// DeleteFileSystem deletes a file system
func DeleteFileSystem(ctx context.Context, symID, fileSystemID string, pmaxClient pmax.Pmax) error {
	err := pmaxClient.DeleteFileSystem(ctx, symID, fileSystemID)
	return err
}

// CreateNFSExport creates a NFS export for the given file system
func CreateNFSExport(ctx context.Context, reqID, symID, fsID string, am *csi.VolumeCapability_AccessMode, volumeContext map[string]string, pmaxClient pmax.Pmax) (*csi.ControllerPublishVolumeResponse, error) {
	nasServerID := volumeContext[NASServerIDParam]
	nasServerName := volumeContext[NASServerNameParam]
	allowRoot := volumeContext[AllowRootParam]
	// log all parameters used in CreateFileSystem call
	fields := map[string]interface{}{
		"SymmetrixID":      symID,
		"CSIRequestID":     reqID,
		"fileSystemID":     fsID,
		NASServerIDParam:   nasServerID,
		NASServerNameParam: nasServerName,
		AllowRootParam:     allowRoot,
		"Access Mode":      am.GetMode().String(),
	}
	log.WithFields(fields).Info("Executing ControllerPublishVolume: FileSystem with following fields")
	// check if fileSystem exist
	fs, err := pmaxClient.GetFileSystemByID(ctx, symID, fsID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fetching fileSystem : %s on symID %s failed with error %s", fsID, symID, err.Error())
	}
	// found the file system, check for nfsExport
	nfsName := fs.Name
	var nfsExport *types.NFSExport
	var alreadyExist bool
	alreadyExist, nfsExport, err = checkIfNFSExportExist(ctx, symID, fsID, nfsName, pmaxClient)
	if err != nil {
		return nil, err
	}
	if !alreadyExist {
		// Create a new NFS Export
		payload := types.CreateNFSExport{
			StorageResource: fsID,
			Name:            nfsName,
			Path:            fmt.Sprintf("/%s", fs.Name),
			DefaultAccess:   "ReadWrite",
		}
		if allowRoot == "true" {
			payload.DefaultAccess = "Root"
		}
		nfsExport, err = pmaxClient.CreateNFSExport(ctx, symID, payload)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "can not create NFS export %s for %s : %s", nfsName, fsID, err.Error())
		}
	}
	nas, err := pmaxClient.GetNASServerByID(ctx, symID, nasServerID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failure getting NAS server %s for fs %s", fsID, err.Error())
	}
	fileInterface, err := pmaxClient.GetFileInterfaceByID(ctx, symID, nas.PreferredInterfaceSettings.CurrentPreferredIPV4)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failure getting file interface %s", err.Error())
	}

	publishContext := map[string]string{
		NASServerNameParam: nasServerName, // we need to pass that to node part of the driver
		NASServerIDParam:   nasServerID,   // we need to pass that to node part of the driver
		NFSExportPathParam: fileInterface.IPAddress + ":/" + nfsExport.Path,
		NFSExportIDParam:   nfsExport.ID,
		AllowRootParam:     allowRoot,
	}
	return &csi.ControllerPublishVolumeResponse{PublishContext: publishContext}, nil
}

func checkIfNFSExportExist(ctx context.Context, symID, fsID string, nfsName string, pmaxClient pmax.Pmax) (bool, *types.NFSExport, error) {
	query := types.QueryParams{QueryName: nfsName, QueryFileSystemID: fsID}
	nfsExportIter, err := pmaxClient.GetNFSExportList(ctx, symID, query)
	if err != nil {
		return false, nil, status.Errorf(codes.Internal, "can not fetch nfsExport list for %s : %s", nfsName, err.Error())
	}
	if nfsExportIter.Count == 0 {
		// Not found
		return false, nil, nil
	}
	nfsID := nfsExportIter.ResultList.NFSExportList[0].ID
	nfsExport, err := pmaxClient.GetNFSExportByID(ctx, symID, nfsID)
	if err != nil {
		return false, nil, status.Errorf(codes.Internal, "can not fetch nfsExport for name: %s, ID: %s : %s", nfsID, nfsID, err.Error())
	}
	if nfsExport.Name != nfsName {
		return false, nil, status.Errorf(codes.AlreadyExists, "A NFS export with the different name exists but has a same file system ID than required.")
	}
	return true, nfsExport, nil
}

// DeleteNFSExport deletes a NFS Export for the given file system
func DeleteNFSExport(ctx context.Context, reqID, symID, fsID string, pmaxClient pmax.Pmax) (*csi.ControllerUnpublishVolumeResponse, error) {
	// get the fileSystem
	fs, err := pmaxClient.GetFileSystemByID(ctx, symID, fsID)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			// The file system is already deleted
			log.Infof("DeleteNFSExport: Could not find file system: %s/%s so assume it's already deleted", symID, fsID)
			return &csi.ControllerUnpublishVolumeResponse{}, nil
		}
		return nil, status.Errorf(codes.Internal, "Could not retrieve fileSystem: (%s)", err.Error())
	}
	// log all parameters used in DeleteNFSExport call
	fields := map[string]interface{}{
		"SymmetrixID":   symID,
		"CSIRequestID":  reqID,
		"fileSystemID":  fsID,
		"NFSExportName": fs.Name,
	}
	log.WithFields(fields).Info("Executing ControllerUnpublishVolume: FileSystem with following fields")

	// check if NFS exist
	alreadyExist, nfsExport, err := checkIfNFSExportExist(ctx, symID, fsID, fs.Name, pmaxClient)
	if err != nil {
		return nil, err
	}
	if !alreadyExist {
		// NFS export is deleted
		return &csi.ControllerUnpublishVolumeResponse{}, nil
	}
	// Delete the NFS Export
	err = pmaxClient.DeleteNFSExport(ctx, symID, nfsExport.ID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "ControllerUnpublish: FileSystem %s- error: %s deleting NFS Export %s", fsID, err.Error(), nfsExport.ID)
	}
	return &csi.ControllerUnpublishVolumeResponse{}, nil
}

// StageFileSystem creates a folder structure on the node
func StageFileSystem(ctx context.Context, reqID, symID, fsID string, privTgt string, publishContext map[string]string, _ pmax.Pmax) (
	*csi.NodeStageVolumeResponse, error,
) {
	nasServerName := publishContext[NASServerNameParam]
	nasServerID := publishContext[NASServerIDParam]
	nfsExportPath := publishContext[NFSExportPathParam]
	nfsExportID := publishContext[NFSExportIDParam]
	allowRoot := publishContext[AllowRootParam]

	// log all parameters used in StageFileSystem call
	fields := map[string]interface{}{
		"SymmetrixID":      symID,
		"CSIRequestID":     reqID,
		"fileSystemID":     fsID,
		NFSExportIDParam:   nfsExportID,
		NASServerIDParam:   nasServerID,
		NASServerNameParam: nasServerName,
		NFSExportPathParam: nfsExportPath,
		AllowRootParam:     allowRoot,
		"Staging Path":     privTgt,
	}
	log.WithFields(fields).Info("Executing NodeStageVolume: FileSystem with following fields")

	found, err := isReadyToPublishNFS(privTgt)
	if err != nil {
		return nil, err
	}
	if found {
		// staging already done
		return &csi.NodeStageVolumeResponse{}, nil
	}
	if err := MkdirAll(privTgt, 0o750); err != nil {
		return nil, status.Errorf(codes.Internal,
			"can't create target folder %s: %s", privTgt, err.Error())
	}
	log.Info("stage path successfully created")

	if err := Mount(ctx, nfsExportPath, privTgt, "nfs"); err != nil {
		return nil, status.Errorf(codes.Internal,
			"error mount nfs share %s to target path: %s", nfsExportPath, err.Error())
	}
	log.Info("mount successfully done")

	return &csi.NodeStageVolumeResponse{}, nil
}

// PublishFileSystem bind the file system mount on the node
func PublishFileSystem(ctx context.Context, req *csi.NodePublishVolumeRequest, reqID, symID, fsID string, _ pmax.Pmax) (*csi.NodePublishVolumeResponse, error) {
	// get params for publish
	publishContext := req.GetPublishContext()
	targetPath := req.GetTargetPath()
	isRO := req.GetReadonly()
	nasServerName := publishContext[NASServerNameParam]
	nasServerID := publishContext[NASServerIDParam]
	nfsExportPath := publishContext[NFSExportPathParam]
	nfsExportID := publishContext[NFSExportIDParam]
	allowRoot := publishContext[AllowRootParam]

	// log all parameters used in PublishFileSystem call
	fields := map[string]interface{}{
		"SymmetrixID":      symID,
		"CSIRequestID":     reqID,
		"fileSystemID":     fsID,
		NFSExportIDParam:   nfsExportID,
		NASServerIDParam:   nasServerID,
		NASServerNameParam: nasServerName,
		NFSExportPathParam: nfsExportPath,
		AllowRootParam:     allowRoot,
		"TargetPath":       targetPath,
	}
	log.WithFields(fields).Info("Executing NodePublishVolume: FileSystem with following fields")

	published, err := isAlreadyPublished(targetPath)
	if err != nil {
		return nil, err
	}
	if published {
		return &csi.NodePublishVolumeResponse{}, nil
	}

	if err := MkdirAll(targetPath, 0o750); err != nil {
		return nil, status.Errorf(codes.Internal,
			"can't create target folder %s: %s", targetPath, err.Error())
	}
	log.Info("target path successfully created")

	mountCap := req.GetVolumeCapability().GetMount()
	mntFlags := mountCap.GetMountFlags()

	if isRO {
		mntFlags = append(mntFlags, "ro")
	}
	stagingPath := req.GetStagingTargetPath()
	if err := BindMount(ctx, stagingPath, targetPath, mntFlags...); err != nil {
		return nil, status.Errorf(codes.Internal,
			"error bind disk %s to target path %s: err %s", stagingPath, targetPath, err.Error())
	}

	log.Info("volume successfully binded")
	return &csi.NodePublishVolumeResponse{}, nil
}

// ExpandFileSystem expands the given file system on the array
func ExpandFileSystem(ctx context.Context, reqID, symID, fsID string, requestedSize int64, pmaxClient pmax.Pmax) (*csi.ControllerExpandVolumeResponse, error) {
	// log all parameters used in ExpandVolume call
	fields := map[string]interface{}{
		"RequestID":     reqID,
		"SymmetrixID":   symID,
		"FileSystemID":  fsID,
		"RequestedSize": requestedSize,
	}
	log.WithFields(fields).Info("Executing ExpandVolume: FileSystem with following fields")

	fs, err := pmaxClient.GetFileSystemByID(ctx, symID, fsID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "fetching fileSystem : %s on symID %s failed with error %s", fsID, symID, err.Error())
	}
	allocatedSize := fs.SizeTotal
	if requestedSize < allocatedSize {
		log.Errorf("Attempting to shrink size of file system (%s) from (%d) MiB to (%d) MiB",
			fs.Name, allocatedSize, requestedSize)
		return nil, status.Error(codes.InvalidArgument,
			"Attempting to shrink the volume size - unsupported operation")
	}
	if requestedSize == allocatedSize {
		log.Infof("Idempotent call detected for file system(%s) with requested size (%d) MiB and allocated size (%d) MiB",
			fs.Name, requestedSize, allocatedSize)
		return &csi.ControllerExpandVolumeResponse{
			CapacityBytes:         allocatedSize * MiBSizeInBytes,
			NodeExpansionRequired: false,
		}, nil
	}

	// Expand the file system
	fs, err = pmaxClient.ModifyFileSystem(ctx, symID, fsID, types.ModifyFileSystem{SizeTotal: requestedSize})
	if err != nil {
		log.Errorf("Failed to execute ModifyFileSystem()/expand with error (%s)", err.Error())
		return nil, status.Error(codes.Internal, err.Error())
	}
	// return the response with NodeExpansionRequired = false, as NodeExpandVolume in not required for a file system
	csiResp := &csi.ControllerExpandVolumeResponse{
		CapacityBytes:         int64(fs.SizeTotal) * MiBSizeInBytes,
		NodeExpansionRequired: false,
	}
	return csiResp, nil
}

func isReadyToPublishNFS(stagingPath string) (bool, error) {
	var found bool
	_, found, err := getTargetMount(stagingPath)
	if err != nil {
		return found, err
	}
	if !found {
		log.Warning("staged device not found")
		return found, nil
	}
	return found, nil
}

func isAlreadyPublished(targetPath string) (bool, error) {
	_, found, err := getTargetMount(targetPath)
	if err != nil {
		return false, status.Errorf(codes.Internal,
			"can't check mounts for path %s: %s", targetPath, err.Error())
	}
	if !found {
		return false, nil
	}
	return true, nil
}

func getTargetMount(target string) (gofsutil.Info, bool, error) {
	var targetMount gofsutil.Info
	mounts, err := GetMounts(context.Background())
	if err != nil {
		log.Error("could not reliably determine existing mount status")
		return targetMount, false, status.Error(codes.Internal, "could not reliably determine existing mount status")
	}
	found := false
	for _, mount := range mounts {
		if mount.Path == target {
			found = true
			targetMount = mount
			log.Infof("matching targetMount %s target %s", target, mount.Path)
		}
	}
	return targetMount, found, nil
}

var GetMounts = func(ctx context.Context) ([]gofsutil.Info, error) {
	return gofsutil.GetMounts(ctx)
}

var Mount = func(ctx context.Context, source string, target string, fstype string, options ...string) error {
	return gofsutil.Mount(ctx, source, target, fstype, options...)
}

var BindMount = func(ctx context.Context, source string, target string, options ...string) error {
	return gofsutil.BindMount(ctx, source, target, options...)
}

var MkdirAll = func(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}
