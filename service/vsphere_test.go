package service

import (
	"fmt"
	"strings"
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
	// Test case: successful connection to ESXi or vCenter
	t.Run("Could not find VM with specified MAC Address", func(t *testing.T) {
		useHttp = true
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
		if err == nil {
			t.Error("Expected non-nil error, got nil")
		}
		if !strings.Contains(err.Error(), "Could not find VM with specified MAC Address") {
			t.Errorf("Expected error containing \"Could not find VM with specified MAC Address\", got %v", err)
		}

	})

	// Test case: error connecting to ESXi or vCenter
	t.Run("Error connecting", func(t *testing.T) {
		insecure := true
		hostURLparam := "localhost"
		user := ""
		pass := ""

		_, err := NewVMHost(insecure, hostURLparam, user, pass)
		if err == nil {
			t.Error("Expected non-nil error, got nil")
		}
	})
}

func TestGetLocalMAC(t *testing.T) {
	var mac, err = getLocalMAC()
	if mac != "" {
		assert.NoError(t, err)
	}
}

func TestGetSCSILuns(t *testing.T) {
	// Test case: Successful retrieval of SCSI Luns
	t.Run("Successful retrieval", func(t *testing.T) {
		simulator.Test(func(ctx context.Context, c *vim25.Client) {
			// Create a mock VMHost
			mockVMHost := &VMHost{
				client: &govmomi.Client{
					Client: c,
				},
				VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
				Ctx: context.Background(),
			}

			// Call the function
			scsiLuns, err := mockVMHost.GetSCSILuns()

			// Assert the results
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}

			if len(scsiLuns) != 2 {
				t.Errorf("Expected 2 SCSI Luns, got %d", len(scsiLuns))
			}

			for _, scsiLun := range scsiLuns {
				expectedCanonicalName := map[string]any{"mpx.vmhba0:C0:T0:L0": nil, "mpx.vmhba1:C0:T0:L0": nil}
				if _, found := expectedCanonicalName[scsiLun.CanonicalName]; !found {
					t.Errorf("Expected canonical name %s, got %s", expectedCanonicalName, scsiLun.CanonicalName)
				}
			}
		})
	})
}

func TestAttachRDM(t *testing.T) {
	// Test case: Device is not found in the list of available devices
	t.Run("Device is not found in the list of available devices", func(t *testing.T) {
		simulator.Test(func(ctx context.Context, c *vim25.Client) {
			// Create a mock VMHost
			mockVMHost := &VMHost{
				client: &govmomi.Client{
					Client: c,
				},
				Ctx: context.Background(),
				VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
			}

			state, err := mockVMHost.VM.PowerState(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if state != types.VirtualMachinePowerStatePoweredOn {
				task, err := mockVMHost.VM.PowerOn(ctx)
				if err != nil {
					t.Fatal(err)
				}

				err = task.Wait(ctx)
				if err != nil {
					t.Fatal(err)
				}
			}

			deviceNAA := "deviceNAA"

			mockVMHost.client.Client = c
			err = mockVMHost.AttachRDM(mockVMHost.VM, deviceNAA)
			fmt.Println(err)
			if err == nil {
				t.Errorf("Expected error, got nil")
			}
		})
	})
}

func TestDetachRDM(t *testing.T) {
	// Test case: Device is not found in the list of available devices
	t.Run("Device is not found in the list of available devices", func(t *testing.T) {
		simulator.Test(func(ctx context.Context, c *vim25.Client) {
			// Create a mock VMHost
			mockVMHost := &VMHost{
				client: &govmomi.Client{
					Client: c,
				},
				Ctx: context.Background(),
				VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
			}
			deviceNAA := "deviceNAA"

			err := mockVMHost.DetachRDM(mockVMHost.VM, deviceNAA)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		})
	})
}

func TestRescanAllHba(t *testing.T) {
	// Test case: Successful rescan
	t.Run("Successful rescan", func(t *testing.T) {
		simulator.Test(func(ctx context.Context, c *vim25.Client) {
			// Create a mock VMHost
			mockVMHost := &VMHost{
				client: &govmomi.Client{
					Client: c,
				},
				Ctx: context.Background(),
				VM:  object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
			}

			host, err := mockVMHost.VM.HostSystem(ctx)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}

			err = mockVMHost.RescanAllHba(host)
			if err != nil {
				t.Errorf("Expected no error, got %v", err)
			}
		})
	})
}

func createGovmomiClient(ctx context.Context) (error, *govmomi.Client) {
	m := simulator.ESX()
	defer m.Remove()

	err := m.Create()
	if err != nil {
		return err, nil
	}

	s := m.Service.NewServer()
	defer s.Close()

	client, err := govmomi.NewClient(ctx, s.URL, true)
	if err != nil {
		return err, nil
	}

	return err, client
}
