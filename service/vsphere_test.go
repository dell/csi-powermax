package service

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/vmware/govmomi"
	"github.com/vmware/govmomi/object"
	"github.com/vmware/govmomi/simulator"
	"github.com/vmware/govmomi/vim25"

	"golang.org/x/net/context"
)

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
			err, client := createGovmomiClient(ctx)
			if err != nil {
				t.Fatal(err)
			}
			mockVMHost := &VMHost{
				client: client,
				Ctx:    context.Background(),
				VM:     object.NewVirtualMachine(c, simulator.Map.Any("VirtualMachine").Reference()),
			}
			deviceNAA := "deviceNAA"

			err = mockVMHost.AttachRDM(mockVMHost.VM, deviceNAA)
			if err == nil {
				t.Errorf("Expected error, got nil")
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
