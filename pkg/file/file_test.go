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
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gofsutil"
	types "github.com/dell/gopowermax/v2/types/v100"
	"go.uber.org/mock/gomock"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCreateFileSystem(t *testing.T) {
	tests := []struct {
		name                      string
		reqID                     string
		access                    *csi.TopologyRequirement
		params                    map[string]string
		symID                     string
		storagePool               string
		service                   string
		nasName                   string
		fsID                      string
		allowRoot                 string
		size                      int64
		getNASServerListResponse  *types.NASServerIterator
		getNASServerListError     error
		getFileSystemListResponse *types.FileSystemIterator
		getFileSystemListError    error
		getFileSystemByIDResponse *types.FileSystem
		getFileSystemByIDError    error
		createFileSystemResponse  *types.FileSystem
		createFileSystemError     error
		wantResp                  *csi.CreateVolumeResponse
		wantErr                   error
	}{
		{
			name:        "Valid input",
			reqID:       "123",
			access:      &csi.TopologyRequirement{},
			params:      map[string]string{"key": "value"},
			symID:       "sym123",
			storagePool: "pool123",
			service:     "service123",
			nasName:     "nas123",
			fsID:        "fs123",
			allowRoot:   "true",
			size:        100,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListResponse: &types.FileSystemIterator{
				ResultList: types.FileSystemList{FileSystemList: []types.FileSystemIDName{
					{
						ID:   "fs1",
						Name: "fs1-name",
					},
				}},
				Count: 1,
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			wantResp: &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					CapacityBytes: 104857600,
					VolumeId:      "fs123-sym123-fs1",
					VolumeContext: map[string]string{
						"CapacityMiB":   "100",
						"CreationTime":  time.Now().Format("20060102150405"),
						"NASServerID":   "nas1",
						"NASServerName": "nas123",
						"SRP":           "pool123",
						"ServiceLevel":  "service123",
						"allowRoot":     "true",
					},
				},
			},
			wantErr: nil,
		},
		{
			name:                     "Empty NAS Servers",
			reqID:                    "",
			access:                   nil,
			params:                   nil,
			symID:                    "0000000000001",
			storagePool:              "",
			service:                  "",
			nasName:                  "nas-1",
			fsID:                     "",
			allowRoot:                "",
			size:                     0,
			getNASServerListResponse: &types.NASServerIterator{},
			wantResp:                 nil,
			wantErr:                  status.Error(codes.Internal, "No NAS server found with name nas-1 on 0000000000001"),
		},
		{
			name:                  "Error calling GetNASServerList",
			reqID:                 "",
			access:                nil,
			params:                nil,
			symID:                 "0000000000001",
			storagePool:           "",
			service:               "",
			nasName:               "nas-1",
			fsID:                  "",
			allowRoot:             "",
			size:                  0,
			getNASServerListError: errors.New("GetNASServerList error"),
			wantResp:              nil,
			wantErr:               status.Error(codes.Internal, "can not fetch NAS server list for 0000000000001 : GetNASServerList error"),
		},
		{
			name:        "Error calling GetFileSystemList",
			reqID:       "",
			access:      nil,
			params:      nil,
			symID:       "0000000000001",
			storagePool: "",
			service:     "",
			nasName:     "nas-1",
			fsID:        "fsid1",
			allowRoot:   "",
			size:        0,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListError: errors.New("GetFileSystemList error"),
			wantResp:               nil,
			wantErr:                status.Error(codes.Internal, "can not fetch filesystem list for fsid1 : GetFileSystemList error"),
		},
		{
			name:        "Empty file system list",
			reqID:       "123",
			access:      &csi.TopologyRequirement{},
			params:      map[string]string{"key": "value"},
			symID:       "sym123",
			storagePool: "pool123",
			service:     "service123",
			nasName:     "nas123",
			fsID:        "fs123",
			allowRoot:   "true",
			size:        100,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListResponse: &types.FileSystemIterator{
				Count: 0,
			},
			createFileSystemResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			wantResp: &csi.CreateVolumeResponse{
				Volume: &csi.Volume{
					CapacityBytes: 104857600,
					VolumeId:      "fs123-sym123-fs1",
					VolumeContext: map[string]string{
						"CapacityMiB":   "100",
						"CreationTime":  time.Now().Format("20060102150405"),
						"NASServerID":   "nas1",
						"NASServerName": "nas123",
						"SRP":           "pool123",
						"ServiceLevel":  "service123",
						"allowRoot":     "true",
					},
				},
			},
			wantErr: nil,
		},
		{
			name:        "Error calling CreateFileSystem",
			reqID:       "123",
			access:      &csi.TopologyRequirement{},
			params:      map[string]string{"key": "value"},
			symID:       "sym123",
			storagePool: "pool123",
			service:     "service123",
			nasName:     "nas123",
			fsID:        "fs123",
			allowRoot:   "true",
			size:        100,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListResponse: &types.FileSystemIterator{
				Count: 0,
			},
			createFileSystemError: errors.New("CreateFileSystem error"),
			wantResp:              nil,
			wantErr:               status.Errorf(codes.Internal, "can not create filesystem for fs123 : CreateFileSystem error"),
		},
		{
			name:        "Error calling GetFileSystemByID",
			reqID:       "123",
			access:      &csi.TopologyRequirement{},
			params:      map[string]string{"key": "value"},
			symID:       "sym123",
			storagePool: "pool123",
			service:     "service123",
			nasName:     "nas123",
			fsID:        "fs123",
			allowRoot:   "true",
			size:        100,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListResponse: &types.FileSystemIterator{
				ResultList: types.FileSystemList{FileSystemList: []types.FileSystemIDName{
					{
						ID:   "fs1",
						Name: "fs1-name",
					},
				}},
				Count: 1,
			},
			getFileSystemByIDError: errors.New("GetFileSystemByID error"),
			wantResp:               nil,
			wantErr:                status.Error(codes.Internal, "can not fetch filesystem for name: fs123, ID: fs1 : GetFileSystemByID error"),
		},
		{
			name:        "Filesystem exists with different size",
			reqID:       "123",
			access:      &csi.TopologyRequirement{},
			params:      map[string]string{"key": "value"},
			symID:       "sym123",
			storagePool: "pool123",
			service:     "service123",
			nasName:     "nas123",
			fsID:        "fs123",
			allowRoot:   "true",
			size:        100,
			getNASServerListResponse: &types.NASServerIterator{Entries: []types.NASServerList{
				{
					ID:   "nas1",
					Name: "nas1-name",
				},
			}},
			getFileSystemListResponse: &types.FileSystemIterator{
				ResultList: types.FileSystemList{FileSystemList: []types.FileSystemIDName{
					{
						ID:   "fs1",
						Name: "fs1-name",
					},
				}},
				Count: 1,
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 200,
			},
			wantResp: nil,
			wantErr:  status.Error(codes.AlreadyExists, "A fileSystem with the same name exists but has a different size than required."),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetNASServerList(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNASServerListResponse, tt.getNASServerListError)
			pmaxClient.EXPECT().GetFileSystemList(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getFileSystemListResponse, tt.getFileSystemListError)
			pmaxClient.EXPECT().GetFileSystemByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getFileSystemByIDResponse, tt.getFileSystemByIDError)
			pmaxClient.EXPECT().CreateFileSystem(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.createFileSystemResponse, tt.createFileSystemError)
			gotResp, gotErr := CreateFileSystem(
				context.Background(),
				tt.reqID,
				tt.access,
				tt.params,
				tt.symID,
				tt.storagePool,
				tt.service,
				tt.nasName,
				tt.fsID,
				tt.allowRoot,
				tt.size,
				pmaxClient,
			)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("CreateFileSystem() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("CreateFileSystem() gotErr = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestDeleteFileSystem(t *testing.T) {
	tests := []struct {
		name                  string
		symID                 string
		fileSystemID          string
		deleteFileSystemError error
		wantErr               error
	}{
		{
			name:         "Valid input",
			symID:        "sym123",
			fileSystemID: "fs123",
			wantErr:      nil,
		},
		{
			name:                  "Error from DeleteFileSystem",
			symID:                 "sym123",
			fileSystemID:          "fs123",
			deleteFileSystemError: status.Error(codes.InvalidArgument, "unable to delete filesystem"),
			wantErr:               status.Errorf(codes.InvalidArgument, "unable to delete filesystem"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().DeleteFileSystem(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.deleteFileSystemError)

			err := DeleteFileSystem(context.Background(), tt.symID, tt.fileSystemID, pmaxClient)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("DeleteFileSystem() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestCreateNFSExport(t *testing.T) {
	tests := []struct {
		name                         string
		symID                        string
		fsID                         string
		nfsName                      string
		am                           *csi.VolumeCapability_AccessMode
		volumeCtx                    map[string]string
		getFileSystemByIDResponse    *types.FileSystem
		getFileSystemByIDError       error
		getNFSExportListResponse     *types.NFSExportIterator
		getNFSExportListError        error
		getNFSExportByIDResponse     *types.NFSExport
		getNFSExportByIDError        error
		createNFSExportResponse      *types.NFSExport
		createNFSExportError         error
		getNASServerByIDResponse     *types.NASServer
		getNASServerByIDError        error
		getFileInterfaceByIDResponse *types.FileInterface
		getFileInterfaceByIDError    error
		wantResp                     *csi.ControllerPublishVolumeResponse
		wantErr                      error
	}{
		{
			name:    "Valid input",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				Count: 0,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			createNFSExportResponse: &types.NFSExport{
				ID:   "nfsexport123",
				Path: "nfsexport123",
			},
			getNASServerByIDResponse: &types.NASServer{},
			getFileInterfaceByIDResponse: &types.FileInterface{
				IPAddress: "10.0.0.0.1",
			},
			wantResp: &csi.ControllerPublishVolumeResponse{PublishContext: map[string]string{
				NASServerIDParam:   "nasserver123",
				NASServerNameParam: "nasserver1",
				NFSExportIDParam:   "nfsexport123",
				NFSExportPathParam: "10.0.0.0.1:/nfsexport123",
				AllowRootParam:     "true",
			}},
			wantErr: nil,
		},
		{
			name:    "NFS export already exists",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				ResultList: types.NFSExportList{
					NFSExportList: []types.NFSExportIDName{
						{
							ID:   "nfsid123",
							Name: "nfsname123",
						},
					},
				},
				Count: 1,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			wantResp: nil,
			wantErr:  status.Error(codes.AlreadyExists, "A NFS export with the different name exists but has a same file system ID than required."),
		},
		{
			name:    "Error calling GetFileSystemByID",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDError: status.Error(codes.NotFound, "File system not found"),
			wantResp:               nil,
			wantErr:                status.Error(codes.Internal, "fetching fileSystem : nfs123 on symID fs123 failed with error rpc error: code = NotFound desc = File system not found"),
		},
		{
			name:    "Error calling CreateNFSExport",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				Count: 0,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			createNFSExportError: errors.New("Error creating NFS export"),
			wantResp:             nil,
			wantErr:              status.Error(codes.Internal, "can not create NFS export fs1-name for nfs123 : Error creating NFS export"),
		},
		{
			name:    "Error calling GetNasServerByID",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				Count: 0,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			createNFSExportResponse: &types.NFSExport{
				ID:   "nfsexport123",
				Path: "nfsexport123",
			},
			getNASServerByIDError: status.Error(codes.NotFound, "NASServer not found"),
			wantResp:              nil,
			wantErr:               status.Error(codes.Internal, "failure getting NAS server nfs123 for fs rpc error: code = NotFound desc = NASServer not found"),
		},
		{
			name:    "Error calling GetFileInterfaceByID",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				Count: 0,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			createNFSExportResponse: &types.NFSExport{
				ID:   "nfsexport123",
				Path: "nfsexport123",
			},
			getNASServerByIDResponse:  &types.NASServer{},
			getFileInterfaceByIDError: status.Error(codes.NotFound, "File interface not found"),
			wantResp:                  nil,
			wantErr:                   status.Error(codes.Internal, "failure getting file interface rpc error: code = NotFound desc = File interface not found"),
		},
		{
			name:    "Error calling GetNFSExportList",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListError: status.Error(codes.NotFound, "NFS export list not found"),
			wantResp:              nil,
			wantErr:               status.Error(codes.Internal, "can not fetch nfsExport list for fs1-name : rpc error: code = NotFound desc = NFS export list not found"),
		},
		{
			name:    "Error calling GetNFSExportByID",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			am:      &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			volumeCtx: map[string]string{
				NASServerNameParam: "nasserver1",
				NASServerIDParam:   "nasserver123",
				AllowRootParam:     "true",
			},
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				ResultList: types.NFSExportList{
					NFSExportList: []types.NFSExportIDName{
						{
							ID:   "nfsexport123",
							Name: "nfsexport123",
						},
					},
				},
				Count: 1,
			},
			getNFSExportByIDError: status.Error(codes.NotFound, "NFS export not found"),
			wantResp:              nil,
			wantErr:               status.Error(codes.Internal, "can not fetch nfsExport for name: nfsexport123, ID: nfsexport123 : rpc error: code = NotFound desc = NFS export not found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetFileSystemByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getFileSystemByIDResponse, tt.getFileSystemByIDError)
			pmaxClient.EXPECT().GetNFSExportList(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNFSExportListResponse, tt.getNFSExportListError)
			pmaxClient.EXPECT().GetNFSExportByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNFSExportByIDResponse, tt.getNFSExportByIDError)

			pmaxClient.EXPECT().CreateNFSExport(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.createNFSExportResponse, tt.createNFSExportError)
			pmaxClient.EXPECT().GetNASServerByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNASServerByIDResponse, tt.getNASServerByIDError)
			pmaxClient.EXPECT().GetFileInterfaceByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getFileInterfaceByIDResponse, tt.getFileInterfaceByIDError)

			gotResp, gotErr := CreateNFSExport(context.Background(), tt.symID, tt.fsID, tt.nfsName, tt.am, tt.volumeCtx, pmaxClient)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("CreateNFSExport() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("CreateNFSExport() gotErr = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestDeleteNFSExport(t *testing.T) {
	tests := []struct {
		name                      string
		symID                     string
		fsID                      string
		nfsName                   string
		getFileSystemByIDResponse *types.FileSystem
		getFileSystemByIDError    error
		getNFSExportListResponse  *types.NFSExportIterator
		getNFSExportByIDResponse  *types.NFSExport
		deleteNFSExportError      error
		wantResp                  *csi.ControllerUnpublishVolumeResponse
		wantErr                   error
	}{
		{
			name:    "Successfully delete NFS Export",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs123",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				ResultList: types.NFSExportList{
					NFSExportList: []types.NFSExportIDName{
						{
							ID:   "nfsid123",
							Name: "nfsname123",
						},
					},
				},
				Count: 1,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "fs123",
			},
			wantResp: &csi.ControllerUnpublishVolumeResponse{},
			wantErr:  nil,
		},
		{
			name:    "Error calling DeleteNFSExport",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs123",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				ResultList: types.NFSExportList{
					NFSExportList: []types.NFSExportIDName{
						{
							ID:   "nfsid123",
							Name: "nfsname123",
						},
					},
				},
				Count: 1,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "fs123",
				ID:   "nfsid123",
			},
			deleteNFSExportError: status.Error(codes.Internal, "failed to delete nfs export"),
			wantResp:             nil,
			wantErr:              status.Error(codes.Internal, "ControllerUnpublish: FileSystem nfs123- error: rpc error: code = Internal desc = failed to delete nfs export deleting NFS Export nfsid123"),
		},
		{
			name:    "NFS export with different name exists but has a same file system ID than required",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				ResultList: types.NFSExportList{
					NFSExportList: []types.NFSExportIDName{
						{
							ID:   "nfsid123",
							Name: "nfsname123",
						},
					},
				},
				Count: 1,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			wantResp: nil,
			wantErr:  status.Error(codes.AlreadyExists, "A NFS export with the different name exists but has a same file system ID than required."),
		},
		{
			name:                      "Error from GetFileSystemByID due to file system not found",
			symID:                     "sym123",
			fsID:                      "fs123",
			nfsName:                   "nfs123",
			getFileSystemByIDResponse: nil,
			getFileSystemByIDError:    errors.New("File system not found"),
			wantResp:                  &csi.ControllerUnpublishVolumeResponse{},
			wantErr:                   nil,
		},
		{
			name:                      "Error from GetFileSystemByID",
			symID:                     "sym123",
			fsID:                      "fs123",
			nfsName:                   "nfs123",
			getFileSystemByIDResponse: nil,
			getFileSystemByIDError:    errors.New("error getting file system by ID"),
			wantResp:                  nil,
			wantErr:                   status.Error(codes.Internal, "Could not retrieve fileSystem: (error getting file system by ID)"),
		},
		{
			name:    "NFS export doesn't exist",
			symID:   "sym123",
			fsID:    "fs123",
			nfsName: "nfs123",
			getFileSystemByIDResponse: &types.FileSystem{
				ID:        "fs1",
				Name:      "fs1-name",
				SizeTotal: 100,
			},
			getNFSExportListResponse: &types.NFSExportIterator{
				Count: 0,
			},
			getNFSExportByIDResponse: &types.NFSExport{
				Name: "nfsexport123",
			},
			wantResp: &csi.ControllerUnpublishVolumeResponse{},
			wantErr:  nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetFileSystemByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getFileSystemByIDResponse, tt.getFileSystemByIDError)
			pmaxClient.EXPECT().GetNFSExportList(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNFSExportListResponse, nil)
			pmaxClient.EXPECT().GetNFSExportByID(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.getNFSExportByIDResponse, nil)
			pmaxClient.EXPECT().DeleteNFSExport(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes().Return(tt.deleteNFSExportError)

			gotResp, gotErr := DeleteNFSExport(context.Background(), tt.symID, tt.fsID, tt.nfsName, pmaxClient)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("DeleteNFSExport() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("DeleteNFSExport() error = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestStageFileSystem(t *testing.T) {
	tests := []struct {
		name           string
		reqID          string
		symID          string
		fsID           string
		privTgt        string
		publishContext map[string]string
		getMountsFunc  func(ctx context.Context) ([]gofsutil.Info, error)
		mkDirAllFunc   func(path string, perm os.FileMode) error
		mountFunc      func(ctx context.Context, source string, target string, fstype string, options ...string) error
		wantResp       *csi.NodeStageVolumeResponse
		wantErr        error
	}{
		{
			name:           "Staging already done (privTgt exists from call to GetMounts)",
			reqID:          "123",
			symID:          "sym123",
			fsID:           "fs123",
			privTgt:        "/mnt/privatetgt",
			publishContext: map[string]string{NASServerNameParam: "nas1", NASServerIDParam: "nas1-id", NFSExportPathParam: "/export/path", NFSExportIDParam: "nfs1-id", AllowRootParam: "true"},
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{
					{
						Path: "/mnt/privatetgt",
					},
				}, nil
			},
			wantResp: &csi.NodeStageVolumeResponse{},
			wantErr:  nil,
		},
		{
			name:           "Staging successful",
			reqID:          "123",
			symID:          "sym123",
			fsID:           "fs123",
			privTgt:        "/mnt/privatetgt",
			publishContext: map[string]string{NASServerNameParam: "nas1", NASServerIDParam: "nas1-id", NFSExportPathParam: "/export/path", NFSExportIDParam: "nfs1-id", AllowRootParam: "true"},
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return nil },
			mountFunc: func(_ context.Context, _ string, _ string, _ string, _ ...string) error {
				return nil
			},
			wantResp: &csi.NodeStageVolumeResponse{},
			wantErr:  nil,
		},
		{
			name:           "Error calling mkDirAll",
			reqID:          "123",
			symID:          "sym123",
			fsID:           "fs123",
			privTgt:        "/mnt/privatetgt",
			publishContext: map[string]string{NASServerNameParam: "nas1", NASServerIDParam: "nas1-id", NFSExportPathParam: "/export/path", NFSExportIDParam: "nfs1-id", AllowRootParam: "true"},
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return errors.New("error") },
			wantResp:     nil,
			wantErr:      status.Error(codes.Internal, "can't create target folder /mnt/privatetgt: error"),
		},
		{
			name:           "Error calling mount",
			reqID:          "123",
			symID:          "sym123",
			fsID:           "fs123",
			privTgt:        "/mnt/privatetgt",
			publishContext: map[string]string{NASServerNameParam: "nas1", NASServerIDParam: "nas1-id", NFSExportPathParam: "/export/path", NFSExportIDParam: "nfs1-id", AllowRootParam: "true"},
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return nil },
			mountFunc: func(_ context.Context, _ string, _ string, _ string, _ ...string) error {
				return errors.New("error")
			},
			wantResp: nil,
			wantErr:  status.Error(codes.Internal, "error mount nfs share /export/path to target path: error"),
		},
		{
			name:           "Error calling GetMounts",
			reqID:          "123",
			symID:          "sym123",
			fsID:           "fs123",
			privTgt:        "/mnt/privatetgt",
			publishContext: map[string]string{NASServerNameParam: "nas1", NASServerIDParam: "nas1-id", NFSExportPathParam: "/export/path", NFSExportIDParam: "nfs1-id", AllowRootParam: "true"},
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, errors.New("error")
			},
			wantResp: nil,
			wantErr:  status.Error(codes.Internal, "could not reliably determine existing mount status"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			GetMounts = tt.getMountsFunc
			MkdirAll = tt.mkDirAllFunc
			Mount = tt.mountFunc

			gotResp, gotErr := StageFileSystem(context.Background(), tt.reqID, tt.symID, tt.fsID, tt.privTgt, tt.publishContext, nil)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("StageFileSystem() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("StageFileSystem() gotErr = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestPublishFileSystem(t *testing.T) {
	tests := []struct {
		name          string
		req           *csi.NodePublishVolumeRequest
		reqID         string
		symID         string
		fsID          string
		getMountsFunc func(ctx context.Context) ([]gofsutil.Info, error)
		mkDirAllFunc  func(path string, perm os.FileMode) error
		bindMountFunc func(_ context.Context, _ string, _ string, _ ...string) error
		wantResp      *csi.NodePublishVolumeResponse
		wantErr       error
	}{
		{
			name: "Valid input",
			req: &csi.NodePublishVolumeRequest{
				Readonly: true,
			},
			reqID: "123",
			symID: "sym123",
			fsID:  "fs123",
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return nil },
			bindMountFunc: func(_ context.Context, _ string, _ string, _ ...string) error {
				return nil
			},
			wantResp: &csi.NodePublishVolumeResponse{},
			wantErr:  nil,
		},
		{
			name: "Already published",
			req: &csi.NodePublishVolumeRequest{
				TargetPath: "/mnt/privatetgt",
			},
			reqID: "123",
			symID: "sym123",
			fsID:  "fs123",
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{
					{
						Device: "test/dev/sda",
						Path:   "/mnt/privatetgt",
					},
				}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return nil },
			bindMountFunc: func(_ context.Context, _ string, _ string, _ ...string) error {
				return nil
			},
			wantResp: &csi.NodePublishVolumeResponse{},
			wantErr:  nil,
		},
		{
			name: "Error calling GetMounts",
			req: &csi.NodePublishVolumeRequest{
				StagingTargetPath: "/mnt/stagingsrc",
				TargetPath:        "/mnt/privatetgt",
			},
			reqID: "123",
			symID: "sym123",
			fsID:  "fs123",
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, errors.New("error")
			},
			wantResp: nil,
			wantErr:  status.Error(codes.Internal, "can't check mounts for path /mnt/privatetgt: rpc error: code = Internal desc = could not reliably determine existing mount status"),
		},
		{
			name: "Error calling mkDirAll",
			req: &csi.NodePublishVolumeRequest{
				TargetPath: "/mnt/privatetgt",
			},
			reqID: "123",
			symID: "sym123",
			fsID:  "fs123",
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return errors.New("error") },
			wantResp:     nil,
			wantErr:      status.Error(codes.Internal, "can't create target folder /mnt/privatetgt: error"),
		},
		{
			name: "Error calling bindMount",
			req: &csi.NodePublishVolumeRequest{
				StagingTargetPath: "/mnt/stagingsrc",
				TargetPath:        "/mnt/privatetgt",
			},
			reqID: "123",
			symID: "sym123",
			fsID:  "fs123",
			getMountsFunc: func(_ context.Context) ([]gofsutil.Info, error) {
				return []gofsutil.Info{}, nil
			},
			mkDirAllFunc: func(_ string, _ os.FileMode) error { return nil },
			bindMountFunc: func(_ context.Context, _ string, _ string, _ ...string) error {
				return errors.New("error")
			},
			wantResp: nil,
			wantErr:  status.Error(codes.Internal, "error bind disk /mnt/stagingsrc to target path /mnt/privatetgt: err error"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			GetMounts = tt.getMountsFunc
			MkdirAll = tt.mkDirAllFunc
			BindMount = tt.bindMountFunc

			gotResp, gotErr := PublishFileSystem(context.Background(), tt.req, tt.reqID, tt.symID, tt.fsID, nil)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("PublishFileSystem() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("PublishFileSystem() gotErr = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}

func TestExpandFileSystem(t *testing.T) {
	tests := []struct {
		name                      string
		reqID                     string
		symID                     string
		fsID                      string
		requestedSize             int64
		allocatedSize             int64
		getFileSystemByIDResponse *types.FileSystem
		getFileSystemByIDError    error
		modifyFileSystemResponse  *types.FileSystem
		modifyFileSystemError     error
		wantResp                  *csi.ControllerExpandVolumeResponse
		wantErr                   error
	}{
		{
			name:                      "Valid input",
			reqID:                     "123",
			symID:                     "sym123",
			fsID:                      "fs123",
			requestedSize:             200,
			allocatedSize:             100,
			getFileSystemByIDResponse: &types.FileSystem{ID: "fs123", Name: "fs123-name", SizeTotal: 100},
			modifyFileSystemResponse:  &types.FileSystem{ID: "fs123", Name: "fs123-name", SizeTotal: 200},
			wantResp: &csi.ControllerExpandVolumeResponse{
				CapacityBytes:         int64(200) * MiBSizeInBytes,
				NodeExpansionRequired: false,
			},
			wantErr: nil,
		},
		{
			name:                      "Invalid input - requested size is less than allocated size",
			reqID:                     "123",
			symID:                     "sym123",
			fsID:                      "fs123",
			requestedSize:             50,
			allocatedSize:             100,
			getFileSystemByIDResponse: &types.FileSystem{ID: "fs123", Name: "fs123-name", SizeTotal: 100},
			wantResp:                  nil,
			wantErr:                   status.Error(codes.InvalidArgument, "Attempting to shrink the volume size - unsupported operation"),
		},
		{
			name:                      "Invalid input - requested size is equal to allocated size",
			reqID:                     "123",
			symID:                     "sym123",
			fsID:                      "fs123",
			requestedSize:             100,
			allocatedSize:             100,
			getFileSystemByIDResponse: &types.FileSystem{ID: "fs123", Name: "fs123-name", SizeTotal: 100},
			wantResp: &csi.ControllerExpandVolumeResponse{
				CapacityBytes:         int64(100) * MiBSizeInBytes,
				NodeExpansionRequired: false,
			},
			wantErr: nil,
		},
		{
			name:                      "Error from GetFileSystemByID",
			reqID:                     "123",
			symID:                     "sym123",
			fsID:                      "fs123",
			requestedSize:             200,
			allocatedSize:             100,
			getFileSystemByIDResponse: nil,
			getFileSystemByIDError:    errors.New("error"),
			wantResp:                  nil,
			wantErr:                   status.Errorf(codes.Internal, "fetching fileSystem : fs123 on symID sym123 failed with error error"),
		},
		{
			name:                      "Error from ModifyFileSystem",
			reqID:                     "123",
			symID:                     "sym123",
			fsID:                      "fs123",
			requestedSize:             200,
			allocatedSize:             100,
			getFileSystemByIDResponse: &types.FileSystem{ID: "fs123", Name: "fs123-name", SizeTotal: 100},
			modifyFileSystemResponse:  nil,
			modifyFileSystemError:     errors.New("error from ModifyFileSystem"),
			wantResp:                  nil,
			wantErr:                   status.Error(codes.Internal, "error from ModifyFileSystem"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := mocks.NewMockPmaxClient(gomock.NewController(t))
			pmaxClient.EXPECT().GetFileSystemByID(gomock.Any(), tt.symID, tt.fsID).AnyTimes().Return(tt.getFileSystemByIDResponse, tt.getFileSystemByIDError)
			pmaxClient.EXPECT().ModifyFileSystem(gomock.Any(), tt.symID, tt.fsID, gomock.Any()).AnyTimes().Return(tt.modifyFileSystemResponse, tt.modifyFileSystemError)

			gotResp, gotErr := ExpandFileSystem(context.Background(), tt.reqID, tt.symID, tt.fsID, tt.requestedSize, pmaxClient)
			if !reflect.DeepEqual(gotResp, tt.wantResp) {
				t.Errorf("ExpandFileSystem() gotResp = %v, want %v", gotResp, tt.wantResp)
			}
			if !errors.Is(gotErr, tt.wantErr) {
				t.Errorf("ExpandFileSystem() gotErr = %v, want %v", gotErr, tt.wantErr)
			}
		})
	}
}
