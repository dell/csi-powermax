package inttest

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	pmax "github.com/dell/csi-powermax/pmax"
	types "github.com/dell/csi-powermax/pmax/types/v90"
)

const (
	SleepTime = 10 * time.Second
)

var (
	client   pmax.Pmax
	endpoint = "https://10.247.73.217:8443"
	// username should match an existing user in Unisphere
	username = "username"
	// password should be the value for the corresponding user in Unisphere

	password                = "password"
	symmetrixID             = "000197900046"
	defaultStorageGroup     = "csi-Integration-Test"
	nonFASTManagedSG        = "csi-Integration-No-FAST"
	defaultFCPortGroup      = "l2se0042_fc_pg"
	defaultiSCSIPortGroup   = "l2se0042_iscsi_pg"
	defaultFCInitiator      = "FA-2D:5:10000000c9748df8"
	defaultiSCSIInitiator   = "SE-1E:000:iqn.1993-08.org.debian:01:5ae293b352a2"
	defaultiSCSIInitiatorID = "iqn.1994-05.com.redhat:94d8f6116bc"
	defaultFCInitiatorID    = "10000000c9748df8"
	fcInitiator1            = "1a000000c9748df8"
	fcInitiator2            = "1b000000c9748df8"
	fcInitiator3            = "1c000000c9748df8"
	fcInitiator4            = "1d000000c9748df8"
	fcInitiator5            = "1e000000c9748df8"
	iscsiInitiator1         = "iqn.1993-08.org.centos:01:5ae577b352a0"
	iscsiInitiator2         = "iqn.1993-08.org.centos:01:5ae577b352a1"
	iscsiInitiator3         = "iqn.1993-08.org.centos:01:5ae577b352a2"
	iscsiInitiator4         = "iqn.1993-08.org.centos:01:5ae577b352a3"
	iscsiInitiator5         = "iqn.1993-08.org.centos:01:5ae577b352a4"
	defaultFCHost           = "l2se0042_fc_ig"
	defaultiSCSIHost        = "l2se0042_iscsi_ig"
	defaultSRP              = "SRP_1"
	defaultServiceLevel     = "Diamond"
	volumePrefix            = "xx"
	sgPrefix                = "zz"
)

func TestMain(m *testing.M) {
	status := 0

	// Process environment variables
	endpoint = setenvVariable("Endpoint", endpoint)
	username = setenvVariable("Username", username)
	password = setenvVariable("Password", password)
	symmetrixID = setenvVariable("SymmetrixID", symmetrixID)
	volumePrefix = setenvVariable("VolumePrefix", volumePrefix)
	defaultStorageGroup = setenvVariable("DefaultStorageGroup", defaultStorageGroup)
	defaultSRP = setenvVariable("DefaultStoragePool", defaultSRP)

	if st := m.Run(); st > status {
		status = st
	}
	fmt.Printf("status %d\n", status)

	os.Exit(status)
}

func setenvVariable(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		if key != "Username" && key != "Password" {
			fmt.Printf("%s=%s\n", key, defaultValue)
		}
		return defaultValue
	}
	if key != "Username" && key != "Password" {
		fmt.Printf("%s=%s\n", key, value)
	}
	return value
}

func getClient(t *testing.T) error {
	var err error
	client, err = pmax.NewClientWithArgs(endpoint, "", "CSI Driver for Dell EMC PowerMax v1.0",
		true, false)
	if err != nil {
		t.Error("cannot create client: ", err.Error())
		return err
	}
	err = client.Authenticate(&pmax.ConfigConnect{
		Endpoint: endpoint,
		Username: username,
		Password: password})
	if err != nil {
		t.Error("cannot authenticate: ", err.Error())
		return err
	}
	return nil
}

func TestAuthentication(t *testing.T) {
	_ = getClient(t)
}

func TestGetSymmetrixIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	symIDList, err := client.GetSymmetrixIDList()
	if err != nil || symIDList == nil {
		t.Error("cannot get SymmetrixIDList: ", err.Error())
		return
	}
	if len(symIDList.SymmetrixIDs) == 0 {
		t.Error("expected at least one Symmetrix ID in list")
		return
	}
	for _, id := range symIDList.SymmetrixIDs {
		fmt.Printf("symmetrix ID: %s\n", id)
	}
}

func TestGetSymmetrix(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	symmetrix, err := client.GetSymmetrixByID(symmetrixID)
	if err != nil || symmetrix == nil {
		t.Error("cannot get Symmetrix id "+symmetrixID, err.Error())
		return
	}
	fmt.Printf("Symmetrix %s: %#v\n", symmetrixID, symmetrix)
}

func TestGetVolumeIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	volumeIDList, err := client.GetVolumeIDList(symmetrixID, "", false)
	if err != nil || volumeIDList == nil {
		t.Error("cannot get volumeIDList: ", err.Error())
		return
	}
	fmt.Printf("%d volume IDs\n", len(volumeIDList))
	// Make sure no duplicates
	dupMap := make(map[string]bool)
	for i := 0; i < len(volumeIDList); i++ {
		if volumeIDList[i] == "" {
			t.Error("Got an empty volume ID")
		}
		id := volumeIDList[i]
		if dupMap[id] == true {
			t.Error("Got duplicate ID:" + id)
		}
		dupMap[id] = true
	}

	volumeIDList, err = client.GetVolumeIDList(symmetrixID, "csi", true)
	if err != nil || volumeIDList == nil {
		t.Error("cannot get volumeIDList: ", err.Error())
		return
	}
	fmt.Printf("%d CSI volume IDs\n", len(volumeIDList))
	for _, id := range volumeIDList {
		fmt.Printf("CSI volume: %s\n", id)
	}

	volumeIDList, err = client.GetVolumeIDList(symmetrixID, "ce9072c0", true)
	if err != nil || volumeIDList == nil {
		t.Error("cannot get volumeIDList: ", err.Error())
		return
	}
	fmt.Printf("%d CSI volume IDs\n", len(volumeIDList))
}

func TestGetVolume(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	// Get some CSI volumes
	volumeIDList, err := client.GetVolumeIDList(symmetrixID, "csi", true)
	if err != nil || volumeIDList == nil {
		t.Error("cannot get CSI volumeIDList: ", err.Error())
		return
	}
	if len(volumeIDList) == 0 {
		t.Error("no CSI volumes")
		return
	}
	for i, id := range volumeIDList {
		if i >= 3 {
			break
		}
		volume, err := client.GetVolumeByID(symmetrixID, id)
		if err != nil {
			t.Error("cannot retrieve Volume: " + err.Error())
		} else {
			fmt.Printf("Volume %#v\n", volume)
		}

	}
}

func TestGetNonExistentVolume(t *testing.T) {
	volume, err := client.GetVolumeByID(symmetrixID, "88888")
	if err != nil {
		fmt.Printf("TestGetNonExistentVolume: %s\n", err.Error())
	} else {
		fmt.Printf("%#v\n", volume)
		t.Error("Expected volume 88888 to be non-existent")
	}
}

func TestGetStorageGroupIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	sgIDList, err := client.GetStorageGroupIDList(symmetrixID)
	if err != nil || sgIDList == nil {
		t.Error("cannot get StorageGroupIDList: ", err.Error())
		return
	}
	if len(sgIDList.StorageGroupIDs) == 0 {
		t.Error("expected at least one StorageGroup ID in list")
		return
	}
	for _, id := range sgIDList.StorageGroupIDs {
		fmt.Printf("StorageGroup ID: %s\n", id)
	}
}

func TestGetStorageGroup(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	storageGroup, err := client.GetStorageGroup(symmetrixID, defaultStorageGroup)
	if err != nil || storageGroup == nil {
		t.Error("Expected to find " + defaultStorageGroup + " but didn't")
		return
	}
	fmt.Printf("%#v\n", storageGroup)
}

func TestGetStoragePool(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	storagePool, err := client.GetStoragePool(symmetrixID, defaultSRP)
	if err != nil || storagePool == nil {
		t.Error("Expected to find " + defaultSRP + " but didn't")
		return
	}
	fmt.Printf("%#v\n", storagePool)
}

func TestCreateStorageGroup(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	now := time.Now()
	storageGroupID := fmt.Sprintf("csi-%s-Int%d-SG", sgPrefix, now.Nanosecond())
	storageGroup, err := client.CreateStorageGroup(symmetrixID, storageGroupID,
		defaultSRP, defaultServiceLevel, false)
	if err != nil || storageGroup == nil {
		t.Error("Failed to create " + storageGroupID)
		return
	}
	fmt.Println("Fetching the newly create storage group from array")
	//Check if the SG exists on array
	storageGroup, err = client.GetStorageGroup(symmetrixID, storageGroupID)
	if err != nil || storageGroup == nil {
		t.Error("Expected to find " + storageGroupID + " but didn't")
		return
	}
	fmt.Printf("%#v\n", storageGroup)
	fmt.Println("Cleaning up the storage group: " + storageGroupID)
	err = client.DeleteStorageGroup(symmetrixID, storageGroupID)
	if err != nil {
		t.Error("Failed to delete " + storageGroupID)
		return
	}
	//Check if the SG exists on array
	storageGroup, err = client.GetStorageGroup(symmetrixID, storageGroupID)
	if err == nil || storageGroup != nil {
		t.Error("Expected a failure in fetching " + storageGroupID + " but didn't")
		return
	}
	fmt.Println(fmt.Sprintf("Error received while fetching %s: %s", storageGroupID, err.Error()))
}

func TestCreateStorageGroupNonFASTManaged(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	now := time.Now()
	storageGroupID := fmt.Sprintf("csi-%s-Int%d-SG-No-FAST", sgPrefix, now.Nanosecond())
	storageGroup, err := client.CreateStorageGroup(symmetrixID, storageGroupID,
		"None", "None", false)
	if err != nil || storageGroup == nil {
		t.Error("Failed to create " + storageGroupID)
		return
	}
	fmt.Printf("%#v\n", storageGroup)
	if storageGroup.SRP != "" {
		t.Error("Expected no SRP but received: " + storageGroup.SRP)
	}
	fmt.Println("Cleaning up the storage group: " + storageGroupID)
	err = client.DeleteStorageGroup(symmetrixID, storageGroupID)
	if err != nil {
		t.Error("Failed to delete " + storageGroupID)
		return
	}
	//Check if the SG exists on array
	storageGroup, err = client.GetStorageGroup(symmetrixID, storageGroupID)
	if err == nil || storageGroup != nil {
		t.Error("Expected a failure in fetching " + storageGroupID + " but didn't")
		return
	}
	fmt.Println(fmt.Sprintf("Error received while fetching %s: %s", storageGroupID, err.Error()))
}

func TestGetJobs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	jobIDList, err := client.GetJobIDList(symmetrixID, "")
	if err != nil {
		t.Error("failed to get Job ID LIst")
		return
	}
	for i, id := range jobIDList {
		if i >= 10 {
			break
		}
		job, err := client.GetJobByID(symmetrixID, id)
		if err != nil {
			t.Error("failed to get job: " + id)
			return
		}
		fmt.Printf("%s\n", client.JobToString(job))
	}

	jobIDList, err = client.GetJobIDList(symmetrixID, types.JobStatusRunning)
	if err != nil {
		t.Error("failed to get Job ID LIst")
		return
	}
	for i, id := range jobIDList {
		if i >= 10 {
			break
		}
		job, err := client.GetJobByID(symmetrixID, id)
		if err != nil {
			t.Error("failed to get job: " + id)
			return
		}
		fmt.Printf("%s\n", client.JobToString(job))
		if job.Status != types.JobStatusRunning {
			t.Error("Expected Running job: " + client.JobToString(job))
		}
	}
}

func TestGetStoragePoolList(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	spList, err := client.GetStoragePoolList(symmetrixID)
	if err != nil {
		t.Error("Failed to get StoragePoolList: " + err.Error())
		return
	}
	for _, value := range spList.StoragePoolIDs {
		fmt.Printf("Storage Resource Pool: %s\n", value)
	}
}

func TestGetMaskingViews(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	mvList, err := client.GetMaskingViewList(symmetrixID)
	if err != nil {
		t.Error("Failed to get MaskingViewList: " + err.Error())
		return
	}
	for _, mvID := range mvList.MaskingViewIDs {
		fmt.Printf("Masking View: %s\n", mvID)
		mv, err := client.GetMaskingViewByID(symmetrixID, mvID)
		if err != nil {
			t.Error("Failed to GetMaskingViewByID: ", err.Error())
			return
		}
		fmt.Printf("%#v\n", mv)
		conns, err := client.GetMaskingViewConnections(symmetrixID, mvID, "")
		if err != nil {
			t.Error("Failed to GetMaskingViewConnections: ", err.Error())
			return
		}
		for _, conn := range conns {
			fmt.Printf("mv connection VolumeID %s HostLUNAddress %s InitiatorID %s DirectorPort %s\n",
				conn.VolumeID, conn.HostLUNAddress, conn.InitiatorID, conn.DirectorPort)
		}
	}
}

func TestCreateVolumeInStorageGroup1(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	now := time.Now()
	volumeName := fmt.Sprintf("csi%s-Int%d", volumePrefix, now.Nanosecond())
	fmt.Printf("volumeName: %s\n", volumeName)
	addVolumeParam := &types.AddVolumeParam{
		NumberOfVols: 1,
		VolumeAttribute: types.VolumeAttributeType{
			VolumeSize:   "1",
			CapacityUnit: "CYL",
		},
		// CreateNewVolumes: true,
		Emulation: "FBA",
		VolumeIdentifier: types.VolumeIdentifierType{
			VolumeIdentifierChoice: "identifier_name",
			IdentifierName:         volumeName,
		},
	}

	payload := &types.UpdateStorageGroupPayload{
		EditStorageGroupActionParam: types.EditStorageGroupActionParam{
			ExpandStorageGroupParam: &types.ExpandStorageGroupParam{
				AddVolumeParam: addVolumeParam,
			},
		},
	}

	payloadBytes, err := json.Marshal(&payload)
	if err != nil {
		t.Error("Encoding error on json")
	}
	fmt.Printf("payload: %s\n", string(payloadBytes))

	job, err := client.UpdateStorageGroup(symmetrixID, defaultStorageGroup, payload)
	if err != nil {
		t.Error("Error returned from UpdateStorageGroup")
		return
	}
	jobID := job.JobID
	job, err = client.WaitOnJobCompletion(symmetrixID, jobID)
	if err == nil {
		idlist, err := client.GetVolumeIDList(symmetrixID, volumeName, false)
		if err != nil {
			t.Error("Error getting volume IDs: " + err.Error())
		}
		for _, id := range idlist {
			cleanupVolume(id, volumeName, defaultStorageGroup, t)
		}
	}
}

func TestCreateVolumeInStorageGroup2(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	now := time.Now()
	volumeName := fmt.Sprintf("csi%s-Int%d", volumePrefix, now.Nanosecond())
	fmt.Printf("volumeName: %s\n", volumeName)
	vol, err := client.CreateVolumeInStorageGroup(symmetrixID, defaultStorageGroup, volumeName, 1)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("volume:\n%#v\n", vol)
	cleanupVolume(vol.VolumeID, volumeName, defaultStorageGroup, t)
}

func TestAddVolumesInStorageGroup(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	now := time.Now()
	volumeName := fmt.Sprintf("csi%s-Int%d", volumePrefix, now.Nanosecond())
	fmt.Printf("volumeName: %s\n", volumeName)
	vol, err := client.CreateVolumeInStorageGroup(symmetrixID, defaultStorageGroup, volumeName, 1)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("volume:\n%#v\n", vol)
	err = client.AddVolumesToStorageGroup(symmetrixID, nonFASTManagedSG, vol.VolumeID)
	if err != nil {
		t.Error(err)
		return
	}
	sg, err := client.GetStorageGroup(symmetrixID, nonFASTManagedSG)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("SG after adding volume: %#v\n", sg)
	//Remove the volume from SG as part of cleanup
	sg, err = client.RemoveVolumesFromStorageGroup(symmetrixID, nonFASTManagedSG, vol.VolumeID)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("SG after removing volume: %#v\n", sg)
	cleanupVolume(vol.VolumeID, volumeName, defaultStorageGroup, t)
}

func cleanupVolume(volumeID string, volumeName string, storageGroup string, t *testing.T) {
	if volumeName != "" {
		vol, err := client.RenameVolume(symmetrixID, volumeID, "_DEL"+volumeName)
		if err != nil {
			t.Error(err)
			return
		}
		fmt.Printf("volume Renamed: %s\n", vol.VolumeIdentifier)
	}
	sg, err := client.RemoveVolumesFromStorageGroup(symmetrixID, storageGroup, volumeID)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("SG after removing volume: %#v\n", sg)
	pmax.Debug = true
	fmt.Printf("Initiating removal of tracks\n")
	job, err := client.InitiateDeallocationOfTracksFromVolume(symmetrixID, volumeID)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("Waiting on job: %s\n", client.JobToString(job))
	job, err = client.WaitOnJobCompletion(symmetrixID, job.JobID)
	if err != nil {
		t.Error(err)
		return
	}
	fmt.Printf("Job completion status: %s\n", client.JobToString(job))
	switch job.Status {
	case "SUCCEEDED":
	case "FAILED":
		if strings.Contains(job.Result, "The device is already in the requested state") {
			break
		}
		t.Error("Track deallocation job failed: " + job.Result)
	}
	err = client.DeleteVolume(symmetrixID, volumeID)
	if err != nil {
		t.Error("DeleteVolume failed: " + err.Error())
	}
	// Test deletion of the volume again... should return an error
	err = client.DeleteVolume(symmetrixID, volumeID)
	if err == nil {
		t.Error("Expected an error saying volume was not found, but no error")
	}
	fmt.Printf("Received expected error: %s\n", err.Error())
}

func TestCreateVolumeInStorageGroupInParallel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping this test in short mode")
	}
	// make sure we have a client
	getClient(t)
	CreateVolumesInParallel(5, t)
}

func CreateVolumesInParallel(nVols int, t *testing.T) {
	fmt.Printf("testing CreateVolumeInStorageGroup with %d parallel requests\n", nVols)
	volIDList := make([]string, nVols)
	// make channels for communication
	idchan := make(chan string, nVols)
	errchan := make(chan error, nVols)
	t0 := time.Now()

	// create a temporary storage group
	now := time.Now()
	storageGroupName := fmt.Sprintf("pmax-%s-Int%d-SG", sgPrefix, now.Nanosecond())
	_, err := client.CreateStorageGroup(symmetrixID, storageGroupName,
		defaultSRP, defaultServiceLevel, false)
	if err != nil {
		t.Errorf("Unable to create temporary Storage Group: %s", storageGroupName)
	}
	// Send requests
	for i := 0; i < nVols; i++ {
		name := fmt.Sprintf("pmax-Int%d-Scale%d", now.Nanosecond(), i)
		go func(volumeName string, idchan chan string, errchan chan error) {
			var err error
			resp, err := client.CreateVolumeInStorageGroup(symmetrixID, storageGroupName, volumeName, 1)
			if resp != nil {
				fmt.Printf("ID %s Name %s\n%#v\n", resp.VolumeID, volumeName, resp)
				idchan <- resp.VolumeID
			} else {
				idchan <- ""
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
			volIDList[i] = id
		}
		err = <-errchan
		if err != nil {
			err = fmt.Errorf("create volume received error: %s", err.Error())
			t.Error(err.Error())
			nerrors++
		}
	}
	t1 := time.Now()
	fmt.Printf("Create volume time for %d volumes %d errors: %v %v\n", nVols, nerrors, t1.Sub(t0).Seconds(), t1.Sub(t0).Seconds()/float64(nVols))
	fmt.Printf("%v\n", volIDList)
	time.Sleep(SleepTime)
	// Cleanup the volumes
	for _, id := range volIDList {
		cleanupVolume(id, "", storageGroupName, t)
	}
	// remove the temporary storage group
	err = client.DeleteStorageGroup(symmetrixID, storageGroupName)
	if err != nil {
		t.Errorf("Unable to delete temporary Storage Group: %s", storageGroupName)
	}
}

func TestGetPorts(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	dirName := "FA-1D"
	portName := "4"
	port, err := client.GetPort(symmetrixID, dirName, portName)
	if err != nil {
		t.Errorf("Unable to read FC storage port %s %s: %s", dirName, portName, err)
		return
	}
	fmt.Printf("port %s:%s %#v\n", dirName, portName, port)
	dirName = "SE-1E"
	portName = "0"
	port, err = client.GetPort(symmetrixID, dirName, portName)
	if err != nil {
		t.Errorf("Unable to read iSCSI storage port %s %s: %s", dirName, portName, err)
		return
	}
	fmt.Printf("port %s:%s %#v\n", dirName, portName, port)

}

func TestGetPortGroupIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	pgList, err := client.GetPortGroupList(symmetrixID, "")
	if err != nil || pgList == nil {
		t.Error("cannot get PortGroupList: ", err.Error())
		return
	}
	if len(pgList.PortGroupIDs) == 0 {
		t.Error("expected at least one PortGroup ID in list")
		return
	}
	pgList, err = client.GetPortGroupList(symmetrixID, "fibre")
	if err != nil || pgList == nil {
		t.Error("cannot get FC PortGroupList: ", err.Error())
		return
	}
	if len(pgList.PortGroupIDs) == 0 {
		t.Error("expected at least one FC PortGroup ID in list")
		return
	}
	pgList, err = client.GetPortGroupList(symmetrixID, "iscsi")
	if err != nil || pgList == nil {
		t.Error("cannot get iSCSI PortGroupList: ", err.Error())
		return
	}
	if len(pgList.PortGroupIDs) == 0 {
		t.Error("expected at least one iSCSI PortGroup ID in list")
		return
	}

}
func TestGetPortGroupByFCID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	portGroup, err := client.GetPortGroupByID(symmetrixID, defaultFCPortGroup)
	if err != nil || portGroup == nil {
		t.Error("Expected to find " + defaultFCPortGroup + " but didn't")
		return
	}
}
func TestGetPortGroupByiSCSIID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	portGroup, err := client.GetPortGroupByID(symmetrixID, defaultiSCSIPortGroup)
	if err != nil || portGroup == nil {
		t.Error("Expected to find " + defaultiSCSIPortGroup + " but didn't")
		return
	}
	fmt.Printf("PortGroup: %#v\n", portGroup)
}

func TestGetInitiatorIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	initList, err := client.GetInitiatorList(symmetrixID, "", true, false)
	if err != nil || initList == nil {
		t.Error("cannot get Initiator List: ", err.Error())
		return
	}
	if len(initList.InitiatorIDs) == 0 {
		t.Error("expected at least one Initiator ID in list")
		return
	}
	// Get the FC initiator list for the default initiator HBA
	initList, err = client.GetInitiatorList(symmetrixID, defaultFCInitiatorID, false, true)
	if err != nil {
		t.Error("Receieved error : ", err.Error())
	}
	if len(initList.InitiatorIDs) != 0 {
		fmt.Println(initList.InitiatorIDs)
	} else {
		fmt.Println("Received an empty FC list")
		t.Error("Expected to find atleast one FC initiator")
	}
	// Get the iSCSI initiator list for the default IQN
	initList, err = client.GetInitiatorList(symmetrixID, defaultiSCSIInitiatorID, true, true)
	if err != nil {
		t.Error("Receieved error : ", err.Error())
	}
	if len(initList.InitiatorIDs) != 0 {
		fmt.Println(initList.InitiatorIDs)
	} else {
		fmt.Println("Received an empty iSCSI list")
		t.Error("Expected to find atleast one iSCSI initiator")
	}

	// Get the initiator list for an IQN not on the array
	initList, err = client.GetInitiatorList(symmetrixID, "iqn.1993-08.org.desian:01:5ae293b352a2", true, true)
	if err != nil {
		t.Error("Received error : ", err.Error())
	}
	if len(initList.InitiatorIDs) != 0 {
		fmt.Println(initList.InitiatorIDs)
	} else {
		fmt.Println("Received an empty list as expected for unknown IQN")
	}
}
func TestGetInitiatorByFCID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	initiator, err := client.GetInitiatorByID(symmetrixID, defaultFCInitiator)
	if err != nil || initiator == nil {
		t.Error("Expected to find " + defaultFCInitiator + " but didn't")
		return
	}
	fmt.Printf("defaultFCInitator: %#v\n", initiator)
}

func TestGetInitiatorByiSCSIID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	initiator, err := client.GetInitiatorByID(symmetrixID, defaultiSCSIInitiator)
	if err != nil || initiator == nil {
		t.Error("Expected to find " + defaultiSCSIInitiator + " but didn't")
		return
	}
	fmt.Printf("defaultiSCSIInitator: %#v\n", initiator)
}

func TestFCGetInitiators(t *testing.T) {
	// Get all the initiators and print the FC ones
	initList, err := client.GetInitiatorList(symmetrixID, "", false, false)
	if err != nil || initList == nil {
		t.Error("cannot get Initiator List: ", err.Error())
		return
	}

	// Read our FC initiators from the /sys/class/fc_host directory
	cmd := exec.Command("/bin/sh", "-c", "cd /sys/class/fc_host; cat */port_name")
	bytes, err := cmd.Output()
	if err != nil {
		return
	}

	// Look for our initiators on the array
	ourInits := strings.Split(string(bytes), "\n")
	if len(ourInits) == 0 {
		// We have any initiators that we know
		return
	}
	fcInitiators := make([]string, 0)
	for _, ourInit := range ourInits {
		ourInit := strings.TrimSpace(strings.Replace(ourInit, "0x", "", 1))
		if ourInit == "" {
			continue
		}
		//fmt.Printf("ourInit: %s\n", ourInit)
		for _, init := range initList.InitiatorIDs {
			if strings.HasSuffix(init, ourInit) {
				fmt.Printf("initiator: %s\n", init)
				fcInitiators = append(fcInitiators, init)
			}
		}
	}

	// Print the matching initiator structures from the Symmetrix
	for _, fcInit := range fcInitiators {
		initiator, err := client.GetInitiatorByID(symmetrixID, fcInit)
		if err != nil || initiator == nil {
			t.Errorf("Unable to read FC initiator: %s", err)
			return
		}
		fmt.Printf("%#v\n", initiator)
	}
}

func TestGetHostIDs(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	hostList, err := client.GetHostList(symmetrixID)
	for _, id := range hostList.HostIDs {
		fmt.Printf("Host ID: %s\n", id)
	}
	if err != nil || hostList == nil {
		t.Error("cannot get Host List: ", err.Error())
		return
	}
	if len(hostList.HostIDs) == 0 {
		t.Error("expected at least one Host ID in list")
		return
	}
}
func TestGetHostByFCID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	host, err := client.GetHostByID(symmetrixID, defaultFCHost)
	if err != nil || host == nil {
		t.Error("Expected to find FC Host" + defaultFCHost + " but didn't")
		return
	}
	fmt.Printf("defaultHost: %#v\n", host)
}

func TestGetHostByiSCSIID(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	host, err := client.GetHostByID(symmetrixID, defaultiSCSIHost)
	if err != nil || host == nil {
		t.Error("Expected to find " + defaultiSCSIHost + " but didn't")
		return
	}
	fmt.Printf("defaultHost: %#v\n", host)
}
func TestCreateFCHost(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			return
		}
	}
	initiatorKeys := make([]string, 0)
	initiatorKeys = append(initiatorKeys, fcInitiator1, fcInitiator2, fcInitiator3, fcInitiator4)
	host, err := client.CreateHost(symmetrixID, "IntTestFCHost", initiatorKeys, nil)
	if err != nil || host == nil {
		t.Error("Expected to create FC host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, FC host", host)
	err = client.DeleteHost(symmetrixID, "IntTestFCHost")
	if err != nil {
		t.Error("Could not delete FC Host: " + err.Error())
	}
}

func TestCreateiSCSIHost(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			return
		}
	}
	initiatorKeys := make([]string, 0)
	initiatorKeys = append(initiatorKeys, iscsiInitiator1, iscsiInitiator2, iscsiInitiator3, iscsiInitiator4)
	host, err := client.CreateHost(symmetrixID, "IntTestiSCSIHost", initiatorKeys, nil)
	if err != nil || host == nil {
		t.Error("Expected to create host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, host", host)
	err = client.DeleteHost(symmetrixID, "IntTestiSCSIHost")
	if err != nil {
		t.Error("Could not delete Host: " + err.Error())
	}
}
func TestCreateFCMaskingView(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping this test in short mode")
	}
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	hostID := "IntTestFCMV-Host"
	// In case a prior test left the Host in the system...
	client.DeleteHost(symmetrixID, hostID)

	// create a Host with some initiators
	initiatorKeys := make([]string, 0)
	initiatorKeys = append(initiatorKeys, fcInitiator5)
	fmt.Println("Setting up a host before creation of masking view")
	host, err := client.CreateHost(symmetrixID, hostID, initiatorKeys, nil)
	if err != nil || host == nil {
		t.Error("Expected to create host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, host", host)
	//hostID := "IS_lqam9024_IG"
	portGroupID := "l2se0042_fc_pg"
	storageGroupID := "csi-Int-Test-MV"
	maskingViewID := "IntTestFCMV"
	maskingView, err := client.CreateMaskingView(symmetrixID, maskingViewID, storageGroupID,
		hostID, true, portGroupID)
	if err != nil {
		t.Error("Expected to create MV with FC initiator and port but didn't: " + err.Error())
		cleanupHost(symmetrixID, hostID, t)
		return
	}
	fmt.Println("Fetching the newly created masking view from array")
	//Check if the MV exists on array
	maskingView, err = client.GetMaskingViewByID(symmetrixID, maskingViewID)
	if err != nil || maskingView == nil {
		t.Error("Expected to find " + maskingViewID + " but didn't")
		cleanupHost(symmetrixID, hostID, t)
		return
	}
	fmt.Printf("%#v\n", maskingView)
	fmt.Println("Cleaning up the masking view")
	err = client.DeleteMaskingView(symmetrixID, maskingViewID)
	if err != nil {
		t.Error("Failed to delete " + maskingViewID)
		return
	}
	fmt.Println("Sleeping for 20 seconds")
	time.Sleep(20 * time.Second)
	// Confirm if the masking view got deleted
	maskingView, err = client.GetMaskingViewByID(symmetrixID, maskingViewID)
	if err == nil {
		t.Error("Expected a failure in fetching MV: " + maskingViewID + "but didn't")
		fmt.Printf("%#v\n", maskingView)
		return
	}
	fmt.Println(fmt.Sprintf("Error in fetching %s: %s", maskingViewID, err.Error()))
	cleanupHost(symmetrixID, hostID, t)
}

func TestCreatePortGroup(t *testing.T) {
	t.Skip("Skipping this test until Delete Port Group is implemented")
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	portGroupID := "IntTestPG"
	portKeys := make([]types.PortKey, 0)
	portKey := types.PortKey{
		DirectorID: "FA-1D",
		PortID:     "4",
	}
	portKeys = append(portKeys, portKey)
	portGroup, err := client.CreatePortGroup(symmetrixID, portGroupID, portKeys)
	if err != nil {
		t.Error("Couldn't create port group")
		return
	}
	fmt.Println(portGroup.PortGroupID)
}

func TestCreateiSCSIMaskingView(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping this test in short mode")
	}
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error("Unable to get/create pmax client")
			return
		}
	}
	hostID := "IntTestiSCSIMV-Host"
	// In case a prior test left the Host in the system...
	client.DeleteHost(symmetrixID, hostID)

	// create a Host with some initiators
	initiatorKeys := make([]string, 0)
	initiatorKeys = append(initiatorKeys, iscsiInitiator5)
	fmt.Println("Setting up a host before creation of masking view")
	host, err := client.CreateHost(symmetrixID, hostID, initiatorKeys, nil)
	if err != nil || host == nil {
		t.Error("Expected to create iscsi host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, host", host)
	//hostID := "IS_lqam9024_IG"
	portGroupID := "IS_lqam9024_PG"
	storageGroupID := "csi-Int-Test-MV"
	maskingViewID := "IntTestiSCSIMV"
	maskingView, err := client.CreateMaskingView(symmetrixID, maskingViewID, storageGroupID,
		hostID, true, portGroupID)
	if err != nil {
		t.Error("Expected to create MV with iscsi initiator host and port but didn't: " + err.Error())
		cleanupHost(symmetrixID, hostID, t)
		return
	}
	fmt.Println("Fetching the newly created masking view from array")
	//Check if the MV exists on array
	maskingView, err = client.GetMaskingViewByID(symmetrixID, maskingViewID)
	if err != nil || maskingView == nil {
		t.Error("Expected to find " + maskingViewID + " but didn't")
		cleanupHost(symmetrixID, hostID, t)
		return
	}
	fmt.Printf("%#v\n", maskingView)
	fmt.Println("Cleaning up the masking view")
	err = client.DeleteMaskingView(symmetrixID, maskingViewID)
	if err != nil {
		t.Error("Failed to delete " + maskingViewID)
		return
	}
	fmt.Println("Sleeping for 20 seconds")
	time.Sleep(20 * time.Second)
	// Confirm if the masking view got deleted
	maskingView, err = client.GetMaskingViewByID(symmetrixID, maskingViewID)
	if err == nil {
		t.Error("Expected a failure in fetching MV: " + maskingViewID + "but didn't")
		fmt.Printf("%#v\n", maskingView)
		return
	}
	fmt.Println(fmt.Sprintf("Error in fetching %s: %s", maskingViewID, err.Error()))
	cleanupHost(symmetrixID, hostID, t)
}

func cleanupHost(symmetrixID string, hostID string, t *testing.T) {
	fmt.Println("Cleaning up the host")
	err := client.DeleteHost(symmetrixID, hostID)
	if err != nil {
		t.Error("Failed to delete " + hostID)
	}
	return
}

func TestUpdateHostInitiators(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}

	// In case a prior test left the Host in the system...
	client.DeleteHost(symmetrixID, "IntTestHost")

	// create a Host with some initiators
	initiatorKeys := make([]string, 0)
	initiatorKeys = append(initiatorKeys, iscsiInitiator1, iscsiInitiator2)
	host, err := client.CreateHost(symmetrixID, "IntTestHost", initiatorKeys, nil)
	if err != nil || host == nil {
		t.Error("Expected to create host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, host", host)

	// change the list of initiators and update the host
	updatedInitiators := make([]string, 0)
	updatedInitiators = append(updatedInitiators, iscsiInitiator1, iscsiInitiator3, iscsiInitiator4)
	host, err = client.UpdateHostInitiators(symmetrixID, host, updatedInitiators)
	if err != nil || host == nil {
		t.Error("Expected to update host but didn't: " + err.Error())
		return
	}
	fmt.Printf("%#v\n, host", host)

	// validate that we have the right number of intiators
	if len(host.Initiators) != len(updatedInitiators) {
		msg := fmt.Sprintf("Expected %d initiators but received %d", len(updatedInitiators), len(host.Initiators))
		t.Error(msg)
		return
	}

	// delete the host
	err = client.DeleteHost(symmetrixID, "IntTestHost")
	if err != nil {
		t.Error("Could not delete Host: " + err.Error())
	}
}

func TestGetTargetAddresses(t *testing.T) {
	if client == nil {
		err := getClient(t)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	addresses, err := client.GetListOfTargetAddresses(symmetrixID)
	if err != nil {
		t.Error("Error calling GetListOfTargetAddresses " + err.Error())
		return
	}
	fmt.Printf("Addresses: %v\n", addresses)

}
