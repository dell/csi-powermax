/*
 Copyright © 2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"github.com/stretchr/testify/assert"
	"testing"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	types "github.com/dell/gopowermax/v2/types/v100"
	"fmt"

)

func TestCaseUnlinkTarget(t *testing.T) {
	pmaxClient, _ := symmetrix.GetPowerMaxClient("123")
	symVol := &types.Volume{
		VolumeID:              "vol-12345",
		Type:                  "Thin",
		Emulation:             "FBA",
		SSID:                  "ssid-001",
		AllocatedPercent:      75,
		CapacityGB:            1000.5,
		FloatCapacityMB:       1024000.5,
		CapacityCYL:           5000,
		Status:                "Online",
		Reserved:              true,
		Pinned:                false,
		PhysicalName:          "Disk 1",
		VolumeIdentifier:      "vol-identifier-123",
		WWN:                   "1234567890ABCDEF",
		Encapsulated:          false,
		NumberOfStorageGroups: 3,
		NumberOfFrontEndPaths: 4,
		StorageGroupIDList:    []string{"sg-001", "sg-002", "sg-003"},
		RDFGroupIDList:        []types.RDFGroupID{{RDFGroupNumber: 42, Label:"label", }, {RDFGroupNumber: 42, Label:"label", }},
		// SymmetrixPortKey:      []SymmetrixPortKeyType{{Key: "port-1"}, {Key: "port-2"}},
		SnapSource:            true,
		SnapTarget:            false,
		CUImageBaseAddress:    "0x1000",
		HasEffectiveWWN:       true,
		EffectiveWWN:          "abcdef1234567890",
		EncapsulatedWWN:       "encapsulated-wwn",
		OracleInstanceName:    "oracle-instance-01",
		MobilityIDEnabled:     true,
		StorageGroups:         []types.StorageGroupName{{StorageGroupName: "group1",ParentStorageGroupName: "group2"}, {StorageGroupName: "group1",ParentStorageGroupName: "group2"}},
		UnreducibleDataGB:     200.5,
		NGUID:                 "nguid-1234",
	}

		var err = unlinkTarget(symVol, "123", "456", "a", 0, pmaxClient)
		expectederr := fmt.Errorf("can't unlink as link state is not defined. retry after sometime")
		if err != nil {
			assert.Equal(t, expectederr, err)
		}
}

func TestIsValid(t *testing.T) {

	dr := deletionRequest{
		DeviceID : "",
		VolumeHandle : "456",
		SymID   : "788",
	}
	var err = dr.isValid("")
	expectederr := fmt.Errorf("device id can't be empty")
	if err != nil {
		assert.Equal(t, expectederr, err)
	}

	dr1 := deletionRequest{
		DeviceID : "123",
		VolumeHandle : "456",
		SymID   : "",
	}
	err = dr1.isValid("")
	expectederr = fmt.Errorf("sym id can't be empty")
	if err != nil {
		assert.Equal(t, expectederr, err)
	}

	dr2 := deletionRequest{
		DeviceID : "123",
		VolumeHandle : "456",
		SymID   : "124",
	}
	err = dr2.isValid("_DEL")
	expectederr = fmt.Errorf("device id doesn't contain the cluster prefix")
	if err != nil {
		assert.Equal(t, expectederr, err)
	}

	dr3 := deletionRequest{
		DeviceID : "123",
		VolumeHandle : "_DEL",
		SymID   : "124",
	}
	err = dr3.isValid("_DEL")
	expectederr = fmt.Errorf("device has not been marked for deletion")
	if err != nil {
		assert.Equal(t, expectederr, err)
	}
}

func TestCaseGetDevice(t *testing.T) {
	var csidev=getDevice("1234", []*csiDevice{{}, {}} )
	var expectedoutput *csiDevice 
	expectedoutput = nil 
	assert.Equal(t, expectedoutput, csidev)
}
func TestCaseGetDeviceRanges(t *testing.T) {
	sdID1:=symDeviceID{
		DeviceID: "1234",
		IntVal: 1,
	}
	sdID2:=symDeviceID{
		DeviceID: "2345",
		IntVal: 2,
	}
	expectedOutput := []deviceRange{
		{
		Start: sdID1,
		End: sdID2,
		},
	}
	var devicerang=getDeviceRanges([]symDeviceID{{DeviceID: "1234",IntVal: 1},{DeviceID: "2345",IntVal: 2}})
	assert.Equal(t, expectedOutput, devicerang)
}
func TestCaseQueueDeviceForDeletion(t *testing.T) {

	sdID1:=symDeviceID{
		DeviceID: "1234",
		IntVal: 2,
	}
	csiDev := csiDevice{
		SymDeviceID    :  sdID1,
		SymID      :      "123",
		VolumeIdentifier : "123",
	}
	DeviceList1 := []*csiDevice{&csiDev, &csiDev}
	deletionQueuevalue := deletionQueue{
		DeviceList:   DeviceList1,
		SymID:        "123",
	}
	var err= deletionQueuevalue.QueueDeviceForDeletion(csiDev)
	expectederror := fmt.Errorf("%s", fmt.Sprintf("%s: found existing entry in deletion queue with volume handle: %s, added at: %v",
	csiDev.print(), csiDev.VolumeIdentifier, csiDev.Status.AdditionTime))
	assert.Equal(t, expectederror, err)
}
