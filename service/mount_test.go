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
	"reflect"
	"testing"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
)

func Test_singleAccessMode(t *testing.T) {
	type args struct {
		accMode *csi.VolumeCapability_AccessMode
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "volume capability mode is single-access node writer",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			},
			want: true,
		},
		{
			name: "volume capability mode is single-access node reader-only",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY},
			},
			want: true,
		},
		{
			name: "volume capability mode is multi-access node writer",
			args: args{
				accMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := singleAccessMode(tt.args.accMode); got != tt.want {
				t.Errorf("singleAccessMode() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_validateVolumeCapability(t *testing.T) {
	type args struct {
		volCap *csi.VolumeCapability
	}
	tests := []struct {
		name           string
		args           args
		isBlock        bool
		wantMount      *csi.VolumeCapability_MountVolume
		wantAccessMode *csi.VolumeCapability_AccessMode
		wantAccessFlag string
		wantErr        bool
	}{
		{
			name: "volume is a block, multi-node reader-only",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
			},
			wantAccessFlag: "ro",
			wantErr:        false,
		},
		{
			name: "volume is a block, multi-node multi-writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "rw",
			wantErr:        false,
		},
		{
			name: "volume is a block, single-node writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        false,
		},
		{
			name: "volume is a mount, multi-node reader-only",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_READER_ONLY,
			},
			wantAccessFlag: "ro",
			wantErr:        false,
		},
		{
			name: "volume is missing access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
				},
			},
			isBlock:        false,
			wantMount:      nil,
			wantAccessMode: nil,
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is block with unknown access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Block{
						Block: &csi.VolumeCapability_BlockVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
					},
				},
			},
			isBlock:   true,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is mount with unknown access mode",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_UNKNOWN,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume is a mount, multi-node multi-writer",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{
						Mount: &csi.VolumeCapability_MountVolume{},
					},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   false,
			wantMount: &csi.VolumeCapability_MountVolume{},
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
		{
			name: "volume type cannot be determined",
			args: args{
				volCap: &csi.VolumeCapability{
					AccessType: &csi.VolumeCapability_Mount{},
					AccessMode: &csi.VolumeCapability_AccessMode{
						Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
					},
				},
			},
			isBlock:   false,
			wantMount: nil,
			wantAccessMode: &csi.VolumeCapability_AccessMode{
				Mode: csi.VolumeCapability_AccessMode_MULTI_NODE_MULTI_WRITER,
			},
			wantAccessFlag: "",
			wantErr:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotIsBlock, gotMount, gotAccessMode, gotAccessFlag, err := validateVolumeCapability(tt.args.volCap, false)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateVolumeCapability() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotIsBlock != tt.isBlock {
				t.Errorf("validateVolumeCapability() gotIsBlock = %v, want %v", gotIsBlock, tt.isBlock)
			}
			if !reflect.DeepEqual(gotMount, tt.wantMount) {
				t.Errorf("validateVolumeCapability() gotMount = %v, want %v", gotMount, tt.wantMount)
			}
			if !reflect.DeepEqual(gotAccessMode, tt.wantAccessMode) {
				t.Errorf("validateVolumeCapability() gotAccessMode = %v, want %v", gotAccessMode, tt.wantAccessMode)
			}
			if gotAccessFlag != tt.wantAccessFlag {
				t.Errorf("validateVolumeCapability() gotAccessFlag = %v, want %v", gotAccessFlag, tt.wantAccessFlag)
			}
		})
	}
}
