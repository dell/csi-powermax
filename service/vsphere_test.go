package service

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"
	"github.com/vmware/govmomi/vim25/types"

	"golang.org/x/net/context"
)

func TestNewVMHost(t *testing.T) {
	t.Run("Could not find VM with specified MAC Address", func(t *testing.T) {
		useHTTP = true
		// Create the necessary objects
		m := simulator.ESX()
		defer m.Remove()

		err := m.Create()
		if err != nil {
			t.Errorf("Expected nil error, got %v", err)
		}

		s := m.Service.NewServer()
		defer s.Close()

		user := s.URL.User.Username()
		pass, _ := s.URL.User.Password()

		_, err = NewVMHost(true, s.URL.Host, user, pass)
		assert.ErrorContains(t, err, "Could not find VM with specified MAC Address")
	})

	t.Run("Error connecting", func(t *testing.T) {
		insecure := true
		hostURLparam := "localhost"
		user := ""
		pass := ""

		_, err := NewVMHost(insecure, hostURLparam, user, pass)
		assert.Error(t, err)
	})
}

func TestGetLocalMAC(t *testing.T) {
	mac, err := getLocalMAC()
	if mac != "" {
		assert.NoError(t, err)
	}
}

func TestGetSCSILuns(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
			Ctx: context.Background(),
		}

		t.Run("Successful retrieval", func(t *testing.T) {
			scsiLuns, err := mockVMHost.GetSCSILuns()

			assert.NoError(t, err)
			assert.Equal(t, 2, len(scsiLuns))

			for _, scsiLun := range scsiLuns {
				expectedCanonicalName := map[string]any{"mpx.vmhba0:C0:T0:L0": nil, "mpx.vmhba1:C0:T0:L0": nil}
				assert.Contains(t, expectedCanonicalName, scsiLun.CanonicalName)
			}
		})
	})
}

func TestAttachRDM(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}

		// Power on the VM
		state, err := mockVMHost.VM.PowerState(ctx)
		assert.NoError(t, err)

		if state != types.VirtualMachinePowerStatePoweredOn {
			task, err := mockVMHost.VM.PowerOn(ctx)
			assert.NoError(t, err)

			err = task.Wait(ctx)
			assert.NoError(t, err)
		}

		t.Run("no device detected on VM host to add", func(t *testing.T) {
			deviceNAA := "deviceNAA"
			err = mockVMHost.AttachRDM(mockVMHost.VM, deviceNAA)
			assert.EqualError(t, err, "no device detected on VM host to add")
		})

		t.Run("no scsi disk on VM host but found scsi lun", func(t *testing.T) {
			deviceNAA := "mpx.vmhba0:C0:T0:L0"
			err = mockVMHost.AttachRDM(mockVMHost.VM, deviceNAA)
			assert.NoError(t, err)
		})
	})
}

func TestDetachRDM(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}
		t.Run("Device is not found in the list of available devices", func(t *testing.T) {
			deviceNAA := "mpx.vmhba0:C0:T0:L0"
			err := mockVMHost.DetachRDM(mockVMHost.VM, deviceNAA)
			assert.NoError(t, err)
		})
	})
}

func TestRescanAllHba(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}
		t.Run("Successful rescan", func(t *testing.T) {
			host, err := mockVMHost.VM.HostSystem(ctx)
			assert.NoError(t, err)

			err = mockVMHost.RescanAllHba(host)
			assert.NoError(t, err)
		})
	})
}

func TestGetAvailableSCSIController(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}
		t.Run("Successful GetAvailableSCSIController", func(t *testing.T) {
			_, err := mockVMHost.getAvailableSCSIController()
			assert.NoError(t, err)
		})
	})
}

func TestCreateController(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}

		t.Run("Successful createController", func(t *testing.T) {
			controllers, err := mockVMHost.getSCSIControllers()
			assert.NoError(t, err)

			err = mockVMHost.createController(&controllers[0])
			assert.NoError(t, err)
		})
	})
}

func TestRemoveLunDevice(t *testing.T) {
	simulator.Test(func(ctx context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: ctx,
			VM:  object.NewVirtualMachine(c, simulator.Map(ctx).Any("VirtualMachine").Reference()),
		}

		// Retrieve valud SCSI LUNs from simulator.
		scsiLuns, err := mockVMHost.GetSCSILuns()
		assert.NoError(t, err)

		// Contruct a map of SCSI LUNs as expected by the function.
		mapSDI := make(map[string]*types.ScsiLun)
		for _, d := range scsiLuns {
			mapSDI[d.Uuid] = d
		}

		// Retrieve device list.
		devices, err := mockVMHost.VM.Device(ctx)
		assert.NoError(t, err)

		t.Run("Success: Remove Device", func(t *testing.T) {
			virtDevice := &types.VirtualDevice{}
			device := &types.VirtualDevice{}

			devices = append(devices, &types.VirtualDevice{
				DeviceInfo: &types.Description{
					Label: "test device",
				},
			})
			mapSDI["lunUUID"] = &types.ScsiLun{
				CanonicalName: "deviceNAA",
			}

			rdmBacking := types.VirtualDiskRawDiskMappingVer1BackingInfo{
				LunUuid: "lunUUID",
			}

			virtDevice.Backing = &rdmBacking

			err := mockVMHost.removeLunDevice(devices, mapSDI, "deviceNAA", mockVMHost.VM, virtDevice, device)
			assert.NoError(t, err)
		})

		t.Run("Failed: Unable to find device", func(t *testing.T) {
			virtDevice := &types.VirtualDevice{}
			device := &types.VirtualDevice{}

			devices = append(devices, &types.VirtualDevice{
				DeviceInfo: &types.Description{
					Label: "test device",
				},
			})
			mapSDI["lunUUID"] = &types.ScsiLun{
				CanonicalName: "deviceNAA",
			}

			rdmBacking := types.VirtualDiskRawDiskMappingVer1BackingInfo{
				LunUuid: "lunUUID",
			}

			virtDevice.Backing = &rdmBacking

			err = mockVMHost.removeLunDevice(object.VirtualDeviceList{}, mapSDI, "deviceNAA", mockVMHost.VM, virtDevice, device)
			assert.Error(t, err, errors.New("device 'device-0' not found"))
		})
	})
}
