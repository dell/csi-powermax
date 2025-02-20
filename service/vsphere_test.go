package service

import (
	"fmt"
	"net/http"
	"net/http/httptest"
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
	simulator.Test(func(_ context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
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
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
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
	simulator.Test(func(_ context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
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
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
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
	simulator.Test(func(_ context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
		}
		t.Run("Successful GetAvailableSCSIController", func(t *testing.T) {
			_, err := mockVMHost.getAvailableSCSIController()
			assert.NoError(t, err)
		})
	})
}

func TestCreateController(t *testing.T) {
	simulator.Test(func(_ context.Context, c *vim25.Client) {
		mockVMHost := &VMHost{
			client: &govmomi.Client{
				Client: c,
			},
			Ctx: context.Background(),
			VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
		}

		t.Run("Successful createController", func(t *testing.T) {
			controllers, err := mockVMHost.getSCSIControllers()
			assert.NoError(t, err)

			err = mockVMHost.createController(&controllers[0])
			assert.NoError(t, err)
		})
	})
}

func reconfigureVMWithSCSIDisk(ctx context.Context, vm *object.VirtualMachine, disk *types.VirtualDisk) error {
	spec := types.VirtualMachineConfigSpec{
		DeviceChange: []types.BaseVirtualDeviceConfigSpec{
			&types.VirtualDeviceConfigSpec{
				Operation: types.VirtualDeviceConfigSpecOperationAdd,
				Device:    disk,
			},
		},
	}

	task, err := vm.Reconfigure(ctx, spec)
	if err != nil {
		return fmt.Errorf("failed to reconfigure virtual machine: %v", err)
	}

	err = task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for reconfiguration task: %v", err)
	}

	return nil
}

func reconfigureVM(ctx context.Context, vm *object.VirtualMachine, spec types.VirtualMachineConfigSpec) error {
	task, err := vm.Reconfigure(ctx, spec)
	if err != nil {
		return fmt.Errorf("failed to reconfigure virtual machine: %v", err)
	}

	err = task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for reconfiguration task: %v", err)
	}

	return nil
}

func TestAttachRDMNew(t *testing.T) {
	// simulator.Test(func(ctx context.Context, c *vim25.Client) {
	// 	vmh := simulator.Map.Any("VirtualMachine").(*simulator.VirtualMachine)
	// 	vmh.Name = "test-vm"

	// 	vm := object.NewVirtualMachine(c, vmh.Reference())

	// 	ds := simulator.Map.Any("Datastore").(*simulator.Datastore)

	// 	devices, err := vm.Device(ctx)
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}

	// 	controller, err := devices.FindDiskController("")
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}

	// 	capacityInBytes := int64(512 * 1024)
	// 	capacityInKB := int64(0)

	// 	disk := devices.CreateDisk(controller, ds.Reference(), "")
	// 	disk.CapacityInBytes = capacityInBytes
	// 	disk.CapacityInKB = capacityInKB

	// 	err = vm.AddDevice(ctx, disk)
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}

	// 	newDevices, err := vm.Device(ctx)
	// 	if err != nil {
	// 		t.Fatal(err)
	// 	}
	// 	disks := newDevices.SelectByType((*types.VirtualDisk)(nil))
	// 	if len(disks) == 0 {
	// 		t.Fatalf("len(disks)=%d", len(disks))
	// 	}

	// 	mockVMHost := &VMHost{
	// 		client: &govmomi.Client{
	// 			Client: c,
	// 		},
	// 		Ctx: ctx,
	// 		VM:  vm,
	// 	}

	// reconfigureVMWithSCSIDisk(ctx, vm, &types.VirtualDisk{
	// 	CapacityInKB: int64(1024),
	// 	VirtualDevice: types.VirtualDevice{
	// 		Backing: &types.VirtualDiskFlatVer2BackingInfo{
	// 			DiskMode:        string(types.VirtualDiskModePersistent),
	// 			ThinProvisioned: types.NewBool(true),
	// 		},
	// 	},
	// })

	// err = reconfigureVM(ctx, vm, spec)
	// assert.NoError(t, err)

	// // Power on the VM
	// state, err := mockVMHost.VM.PowerState(ctx)
	// assert.NoError(t, err)

	// if state != types.VirtualMachinePowerStatePoweredOn {
	// 	task, err := mockVMHost.VM.PowerOn(ctx)
	// 	assert.NoError(t, err)

	// 	err = task.Wait(ctx)
	// 	assert.NoError(t, err)
	// }

	// // Create a new disk to attach
	// disk := types.VirtualDisk{
	// 	CapacityInKB: int64(1024),
	// 	VirtualDevice: types.VirtualDeviceConfigSpec{
	// 		Backing: &types.VirtualDiskFlatVer2BackingInfo{
	// 			DiskMode:        string(types.VirtualDiskModePersistent),
	// 			ThinProvisioned: types.NewBool(true),
	// 		},
	// 	},
	// }
	// mockVMHost.VM.AttachDisk(ctx, disk)

	// var rdmBacking types.VirtualDiskFlatVer2BackingInfo
	// rdmBacking.FileName = ""
	// rdmBacking.DiskMode = "independent_persistent"

	// var rdmDisk types.VirtualDisk
	// rdmDisk.Backing = &rdmBacking
	// rdmDisk.CapacityInKB = 1024

	// controller, err := mockVMHost.getAvailableSCSIController()
	// assert.NoError(t, err)

	// if controller == nil {
	// 	controllers, err := mockVMHost.getSCSIControllers()
	// 	assert.NoError(t, err)

	// 	if len(controllers) == 0 {
	// 		assert.NoError(t, err)
	// 	}

	// 	if len(controllers) == 4 {
	// 		assert.NoError(t, err)
	// 	}

	// 	err = mockVMHost.createController(&controllers[0])
	// 	assert.NoError(t, err)

	// 	controller, err = mockVMHost.getAvailableSCSIController()
	// 	assert.NoError(t, err)
	// }

	// rdmDisk.ControllerKey = controller.VirtualController.Key
	// var x int32 = -1
	// rdmDisk.UnitNumber = &x

	// err = mockVMHost.VM.AddDevice(ctx, &rdmDisk)
	// assert.NoError(t, err)

	// 	deviceNAA := "mpx.vmhba0:C0:T0:L0"
	// 	err = mockVMHost.AttachRDM(mockVMHost.VM, deviceNAA)
	// 	assert.NoError(t, err)

	// 	fmt.Printf("CPU reservation: %d\n", *vmh.Config.CpuAllocation.Reservation)
	// })

	useHTTP = true
	m := simulator.ESX()
	defer m.Remove()

	err := m.Create()
	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}

	s := m.Service.NewServer()
	defer s.Close()

	// forwardUrl := s.URL

	// server := fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// 	client := &http.Client{}

	// 	// Create a new HTTP request with the same method, URL, and headers
	// 	req, err := http.NewRequest(r.Method, forwardUrl.String(), r.Body)
	// 	if err != nil {
	// 		http.Error(w, err.Error(), http.StatusInternalServerError)
	// 		return
	// 	}

	// 	// Set the headers from the original request
	// 	for key, values := range r.Header {
	// 		for _, value := range values {
	// 			req.Header.Add(key, value)
	// 		}
	// 	}

	// 	// Forward the request
	// 	resp, err := client.Do(req)
	// 	if err != nil {
	// 		http.Error(w, err.Error(), http.StatusInternalServerError)
	// 		return
	// 	}
	// 	defer resp.Body.Close()

	// 	// Write the response to the original response writer
	// 	for key, values := range resp.Header {
	// 		for _, value := range values {
	// 			w.Header().Add(key, value)
	// 		}
	// 	}

	// 	w.WriteHeader(resp.StatusCode)
	// 	_, err = io.Copy(w, resp.Body)
	// 	if err != nil {
	// 		http.Error(w, err.Error(), http.StatusInternalServerError)
	// 		return
	// 	}
	// }))

	// s.Server.URL = server.URL

	user := s.URL.User.Username()
	pass, _ := s.URL.User.Password()

	// s.URL, err = url.Parse(server.URL)
	assert.NoError(t, err)

	host, err := NewVMHost(true, s.URL.Host, user, pass)
	assert.NoError(t, err)

	fmt.Printf("host: %v\n", host)

	deviceNAA := "mpx.vmhba0:C0:T0:L0"
	err = host.AttachRDM(host.VM, deviceNAA)
	assert.NoError(t, err)
}

func fakeServer(t *testing.T, h http.Handler) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(func() {
		s.Close()
	})
	return s
}

// func TestVmCreation(t *testing.T) {
// 	// Connect to the vCenter server
// 	client, err := vim25.NewClient(context.TODO())
// 	if err != nil {
// 		fmt.Println("Failed to create client:", err)
// 		return
// 	}

// 	// Find the datacenter
// 	finder := find.NewFinder(client, true)
// 	datacenter, err := finder.DatacenterOrDefault(context.TODO(), "")
// 	if err != nil {
// 		fmt.Println("Failed to find datacenter:", err)
// 		return
// 	}
// 	finder.SetDatacenter(datacenter)

// 	// Create a new virtual disk
// 	diskSpec := types.VirtualDisk{
// 		VirtualDevice: types.VirtualDevice{
// 			Key: 1,
// 		},
// 		CapacityInKB: int64(10 * 1024 * 1024),
// 	}

// 	// Create a new VM configuration
// 	vmConfig := types.VirtualMachineConfigSpec{
// 		Name:     "example-vm",
// 		MemoryMB: int64(1024),
// 		NumCPUs:  int32(1),
// 		DeviceChange: []types.BaseVirtualDeviceConfigSpec{
// 			&types.VirtualDeviceConfigSpec{
// 				Operation: types.VirtualDeviceConfigSpecOperationAdd,
// 				Device:    &diskSpec,
// 			},
// 		},
// 	}

// 	// Create the VM
// 	vm, err := datacenter.CreateVM(context.TODO(), vmConfig, nil, nil)
// 	if err != nil {
// 		fmt.Println("Failed to create VM:", err)
// 		return
// 	}

// 	fmt.Println("VM created successfully:", vm.Reference().Value)
// }
