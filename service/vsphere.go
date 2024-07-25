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

// This piece of code was developed for RDM support mainly thanks to the project govmax on github.com
// This file in particular from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go

package service

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"reflect"
	"regexp"
	"strings"

	log "github.com/sirupsen/logrus"

	"github.com/vmware/govmomi/find"
	"github.com/vmware/govmomi/vim25/methods"
	"github.com/vmware/govmomi/vim25/mo"

	"github.com/akutz/goof"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/vim25/types"
	"golang.org/x/net/context"
)

// VMHost - structure to hold the VM host
type VMHost struct {
	client *govmomi.Client
	Ctx    context.Context
	mac    string
	VM     *object.VirtualMachine
}

// NewVMHost connects to a ESXi or vCenter instance and returns a *VMHost
// This method is referenced from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go
func NewVMHost(insecure bool, hostURLparam, user, pass string, ifaceExcludeFilter *regexp.Regexp) (*VMHost, error) {
	ctx, _ := context.WithCancel(context.Background())
	hostURL, err := url.Parse("https://" + hostURLparam + "/sdk")
	hostURL.User = url.UserPassword(user, pass)

	cli, err := govmomi.NewClient(ctx, hostURL, insecure)
	if err != nil {
		return nil, err
	}

	mac, err := getLocalMAC(net.Interfaces, ifaceExcludeFilter)
	if err != nil {
		return nil, err
	}

	vmh := &VMHost{
		client: cli,
		Ctx:    ctx,
		mac:    mac,
	}

	vm, err := vmh.findVM(vmh.mac)
	if err != nil {
		return nil, err
	}
	vmh.VM = vm

	return vmh, nil
}

////////////////////////////////////////////////////////////////////
//              Returns if Eth0 MAC Address of VM                 //
////////////////////////////////////////////////////////////////////

func (vmh *VMHost) getMACAddressOfVM(vm *object.VirtualMachine) (string, error) {
	vmDeviceList, err := vm.Device(context.TODO())
	if err != nil {
		return "", errors.New("Cannot read VM VirtualDevices")
	}
	return vmDeviceList.PrimaryMacAddress(), nil
}

func getLocalMAC(ifaceListFunc func() ([]net.Interface, error), ifaceExcludeFilter *regexp.Regexp) (string, error) {
	ifs, err := ifaceListFunc()
	if err != nil {
		return "", err
	}
	for _, v := range ifs {
		if ifaceExcludeFilter != nil {
			if ifaceExcludeFilter.MatchString(v.Name) {
				continue
			}
			if v.HardwareAddr.String() != "" {
				return v.HardwareAddr.String(), nil
			}
		} else {
			if v.HardwareAddr.String() != "" {
				return v.HardwareAddr.String(), nil
			}
		}
	}
	return "", errors.New("no network interface found")
}

///////////////////////////////////////////////////////////////////
//     Find the target VM in Host with specified MAC Address     //
//    Using MAC address to identify VM since that is the only    //
//       property known to the VM that its host also knows       //
//                   WITHOUT VMWARE tools installed.             //
///////////////////////////////////////////////////////////////////

func (vmh *VMHost) findVM(targetMACAddress string) (vm *object.VirtualMachine, err error) {
	targetMACAddress = strings.ToUpper(targetMACAddress)
	f := find.NewFinder(vmh.client.Client, true)
	allDatacenters, err := f.DatacenterList(vmh.Ctx, "*")
	if err != nil {
		return nil, errors.New("Could not get List of Datacenters")
	}
	for _, datacenter := range allDatacenters {
		f.SetDatacenter(datacenter)
		allVMs, err := f.VirtualMachineList(vmh.Ctx, "*")
		if err != nil {
			return nil, errors.New("Could not get List of VMs")
		}
		for _, vm := range allVMs {
			VMMac, err := vmh.getMACAddressOfVM(vm)
			VMMac = strings.ToUpper(VMMac)
			if err != nil {
				log.Debugf("Could not get MAC Address of VM (%v), datacenter (%v)", vm, datacenter)
			}
			if VMMac == targetMACAddress && err == nil {
				log.Debugf("Found VM: %v , mac(%s)", vm, VMMac)
				return vm, nil
			}
		}
	}
	return nil, errors.New("Could not find VM with specified MAC Address of " + targetMACAddress)
}

func (vmh *VMHost) getVMScsiDiskDeviceInfo(vm *object.VirtualMachine) ([]types.VirtualMachineScsiDiskDeviceInfo, error) {
	var VMwithProp mo.VirtualMachine
	err := vm.Properties(vmh.Ctx, vm.Reference(), []string{"environmentBrowser"}, &VMwithProp)
	if err != nil {
		return nil, fmt.Errorf("Error finding Environment Browser for VM - %S", err)
	}

	// Query VM To Find Devices avilable for attaching to VM
	var queryConfigRequest types.QueryConfigTarget
	queryConfigRequest.This = VMwithProp.EnvironmentBrowser
	queryConfigResp, err := methods.QueryConfigTarget(vmh.Ctx, vmh.client.Client, &queryConfigRequest)
	if err != nil {
		return nil, fmt.Errorf("Error Obtaining Configuration Options of Host System that VM is On - %S", err)
	}
	vmConfigOptions := *queryConfigResp.Returnval

	return vmConfigOptions.ScsiDisk, nil
}

func (vmh *VMHost) getAvailableSCSIController() (*types.VirtualSCSIController, error) {
	scsiControllers, err := vmh.getSCSIControllers()
	if err != nil {
		return nil, err
	}

	for _, scsiDevice := range scsiControllers {
		scsiDevice := scsiDevice.(types.BaseVirtualSCSIController).GetVirtualSCSIController()
		if len(scsiDevice.VirtualController.Device) < 15 {
			return scsiDevice, nil
		}
	}
	return nil, nil
}

func (vmh *VMHost) getSCSIControllers() (object.VirtualDeviceList, error) {
	virtualDeviceList, err := vmh.VM.Device(vmh.Ctx)
	if err != nil {
		return nil, fmt.Errorf("Error Obtaining List of Devices Attached to VM - %s", err)
	}

	var c types.VirtualSCSIController
	return virtualDeviceList.SelectByType(&c), nil
}

// GetSCSILuns fetches all the SCSILuns discovered on the host
// This method is referenced from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go
func (vmh *VMHost) GetSCSILuns() ([]*types.ScsiLun, error) {
	host, err := vmh.VM.HostSystem(vmh.Ctx)
	if err != nil {
		return nil, err
	}

	ss, err := host.ConfigManager().StorageSystem(vmh.Ctx)
	if err != nil {
		return nil, err
	}

	var hss mo.HostStorageSystem
	err = ss.Properties(vmh.Ctx, ss.Reference(), nil, &hss)
	if err != nil {
		return nil, err
	}

	scsiLuns := make([]*types.ScsiLun, 0)
	for _, sl := range hss.StorageDeviceInfo.ScsiLun {
		scsiLuns = append(scsiLuns, sl.(types.BaseScsiLun).GetScsiLun())
	}

	return scsiLuns, nil
}

func (vmh *VMHost) createController(controller *types.BaseVirtualDevice) error {
	devices, err := vmh.VM.Device(context.TODO())
	if err != nil {
		return err
	}

	d, err := devices.CreateSCSIController(reflect.TypeOf(*controller).Name())
	if err != nil {
		return err
	}

	err = vmh.VM.AddDevice(vmh.Ctx, d)
	if err != nil {
		return fmt.Errorf("Error creating new SCSI Controller for RDM - %s", err)
	}

	return nil
}

// AttachRDM adds the device to the VM as an RDM
// This method is referenced from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go
func (vmh *VMHost) AttachRDM(vm *object.VirtualMachine, deviceNAA string) (err error) {
	vmScsiDiskDeviceInfo, err := vmh.getVMScsiDiskDeviceInfo(vm)
	if err != nil {
		return err
	}

	// var scsiCtlrUnitNumber int
	// Build new Virtual Device to add to VM from list of avilable devices found from our query
	for _, ScsiDisk := range vmScsiDiskDeviceInfo {
		if !strings.Contains(ScsiDisk.Disk.CanonicalName, deviceNAA) {
			continue
		}

		var rdmBacking types.VirtualDiskRawDiskMappingVer1BackingInfo
		rdmBacking.FileName = ""
		rdmBacking.DiskMode = "independent_persistent"
		rdmBacking.CompatibilityMode = "physicalMode"
		rdmBacking.DeviceName = ScsiDisk.Disk.DeviceName
		for _, descriptor := range ScsiDisk.Disk.Descriptor {
			if string([]rune(descriptor.Id)[:4]) == "vml." {
				rdmBacking.LunUuid = descriptor.Id
				break
			}
		}
		var rdmDisk types.VirtualDisk
		rdmDisk.Backing = &rdmBacking
		rdmDisk.CapacityInKB = 1024

		controller, err := vmh.getAvailableSCSIController()
		if err != nil {
			return err
		}

		if controller == nil {
			controllers, err := vmh.getSCSIControllers()
			if err != nil {
				return err
			}

			if len(controllers) == 0 {
				return errors.New("no SCSI controllers found")
			}

			if len(controllers) == 4 {
				return errors.New("no more controllers can be added")
			}

			err = vmh.createController(&controllers[0])
			if err != nil {
				return err
			}

			controller, err = vmh.getAvailableSCSIController()
			if err != nil {
				return err
			}
		}

		rdmDisk.ControllerKey = controller.VirtualController.Key
		var x int32 = -1
		rdmDisk.UnitNumber = &x

		err = vm.AddDevice(vmh.Ctx, &rdmDisk)
		if err != nil {
			return fmt.Errorf("Error adding device %+v \n Logged Item:  %s", rdmDisk, err)
		}
		return nil

	}

	scsiLuns, err := vmh.GetSCSILuns()
	if err != nil {
		return goof.WithError("error getting existing LUNs", err)
	}

	for _, sl := range scsiLuns {
		if strings.Contains(sl.CanonicalName, deviceNAA) {
			return nil
		}
	}

	return errors.New("no device detected on VM host to add")
}

// DetachRDM removes the device from the VM
// This method is referenced from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go
func (vmh *VMHost) DetachRDM(vm *object.VirtualMachine, deviceNAA string) error {
	scsiLuns, err := vmh.GetSCSILuns()
	if err != nil {
		return err
	}

	mapSDI := make(map[string]*types.ScsiLun)
	for _, d := range scsiLuns {
		mapSDI[d.Uuid] = d
	}

	devices, err := vm.Device(context.TODO())
	if err != nil {
		return err
	}

	for _, device := range devices {
		device2 := device.(types.BaseVirtualDevice).GetVirtualDevice()
		if device2.Backing != nil {
			elem := reflect.ValueOf(device2.Backing).Elem()
			lunUUID := elem.FieldByName("LunUuid")
			if lunUUID.Kind() == reflect.Invalid {
				continue
			}
			if sd, ok := mapSDI[lunUUID.String()]; ok && strings.Contains(sd.CanonicalName, deviceNAA) {
				deviceName := devices.Name(device)
				newDevice := devices.Find(deviceName)
				if newDevice == nil {
					return fmt.Errorf("device '%s' not found", deviceName)
				}
				if err = vm.RemoveDevice(context.TODO(), false, newDevice); err != nil {
					return err
				}
				break
			}
		}

	}

	return nil
}

// RescanAllHba rediscovers new devices added to the ESX
// This method is referenced from https://github.com/codedellemc/govmax/blob/master/api/v1/vmomi.go
func (vmh *VMHost) RescanAllHba(hostSystem *object.HostSystem) error {
	storageSystem, err := hostSystem.ConfigManager().StorageSystem(vmh.Ctx)
	if err != nil {
		return err
	}

	err = storageSystem.RescanAllHba(vmh.Ctx)
	if err != nil {
		return err
	}
	return nil
}
