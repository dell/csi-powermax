/*
 Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package k8integration

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// RunningPods stores pod information
type RunningPods struct {
	NAME   string
	READY  string
	STATUS string
}

// VolumeCreateRequest stores volume request information
type VolumeCreateRequest struct {
	VolumeNames  []string
	SYMID        string
	SRP          string
	ServiceLevel string
	AccessModes  string
	VolSize      string
}

// VolumeCreateResponse stores volume response information
type VolumeCreateResponse struct {
	NAME         string
	STATUS       string
	VOLUME       string
	CAPACITY     string
	ACMODES      string
	STORAGECLASS string
	AGE          string
}

// CreateSC creates storage class with json file name and returns error status
func CreateSC(jsonNameForSC string) error {
	fmt.Println("creating storage class")
	cmd := "kubectl create -f /root/DirForDynamicJSONFiles/" + jsonNameForSC
	text1, err := executeKubernetesCommand(cmd)
	if strings.Contains(string(text1), "already exists") {
		fmt.Println("storage class already exists , deleting that")
		err = DeleteSC(jsonNameForSC)
		if err != nil {
			return err
		}
		CreateSC(jsonNameForSC)
	}
	if err != nil {
		return err
	}

	fmt.Println("successfully created storage class")
	return nil
}

// DeleteSC deletes the storage class with json file name and returns error status
func DeleteSC(jsonNameForSC string) error {
	fmt.Println("deleting storage class")
	cmd := "kubectl delete -f /root/DirForDynamicJSONFiles/" + jsonNameForSC
	_, err := executeKubernetesCommand(cmd)
	if err != nil {
		return err
	}
	fmt.Println("successfully deleted storage class")
	return nil
}

// CreatePV will get the dynamically created json file names and create storage class and pvc using the same
func CreatePV(newVolumeCreateRequest *VolumeCreateRequest, uniqueID int) error {
	// Create file name for storageclass
	jsonNameForSC := getSCFileName(uniqueID)

	//create file name for PVC
	jsonNameForPVC := getPVCFileName(uniqueID)

	// create json files with the respective entries and copy the files to kubernetes cluster
	fileCreateStatus := CreateJSONFiles(newVolumeCreateRequest, jsonNameForSC, jsonNameForPVC)
	if fileCreateStatus != nil {
		fmt.Println("creation of json files failed")
		return fileCreateStatus
	}
	fmt.Println("creation of json files succeded")
	err := CreateSC(jsonNameForSC)
	if err != nil {
		return err
	}
	fmt.Println("creating pvc object")
	cmd := "kubectl create -f /root/DirForDynamicJSONFiles/" + jsonNameForPVC
	text1, err := executeKubernetesCommand(cmd)

	if strings.Contains(string(text1), "already exists") {
		fmt.Println("PV already exists , deleting that")
		err = DeletePV(uniqueID)
		if err != nil {
			return err
		}
		text1, err = executeKubernetesCommand(cmd)
	}
	if err != nil {
		return err
	}

	fmt.Println("creating pvc is successful")
	return nil

}

// getSCFileName creates file name for storage class
func getSCFileName(uniqueID int) string {
	return "SC-" + strconv.Itoa(uniqueID) + ".json"
}

// creates file name for PVC
func getPVCFileName(uniqueID int) string {
	return "PVC-" + strconv.Itoa(uniqueID) + ".json"
}

// DeletePV deletes pvc files which is created as part of current scenario
//and also deletes storage class
func DeletePV(uniqueID int) error {
	// Create file name for SC
	jsonNameForSC := getSCFileName(uniqueID)
	// Create file name for PVC
	jsonNameForPVC := getPVCFileName(uniqueID)

	fmt.Println("deleting PV")
	cmd := "kubectl delete -f /root/DirForDynamicJSONFiles/" + jsonNameForPVC
	_, err1 := executeKubernetesCommand(cmd)
	if err1 != nil {
		return err1
	}
	err2 := DeleteSC(jsonNameForSC)
	if err2 != nil {
		return err2
	}
	fmt.Println("successfully deleted PVC")
	return nil
}

// ControllerIsRunning will check whether csi controller pod is running
func ControllerIsRunning() error {
	var oneInstance []string
	fmt.Println("Validating that csi contoller service is active")
	runningPodList := []RunningPods{}
	text1, err := executeKubernetesCommand("kubectl get pods  -n powermax")
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(text1)))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		oneInstance = strings.Fields(scanner.Text())
		newPod := RunningPods{NAME: oneInstance[0], READY: oneInstance[1], STATUS: oneInstance[2]}
		runningPodList = append(runningPodList, newPod)
	}
	for _, runningPods := range runningPodList {
		if strings.Contains(runningPods.NAME, "controller") {
			if runningPods.STATUS == "Running" {
				return nil
			}
		}
	}
	err = errors.New("csi pod is not running")
	return err
}

//ValidatePvcreate will validate whether intended volumes are created successfully and returns error status along with volume details
//and also we can validate other fields like size , storageClassName etc if needed
func ValidatePvcreate(newVolumeCreateRequest *VolumeCreateRequest) (*[]VolumeCreateResponse, error) {
	var oneInstance []string
	VolumesCreatedFromKubernetes := []VolumeCreateResponse{}
	// creating list for volume creation

	//wating for some time to create pvc
	fmt.Println("wating for some time to allow for creating pvc")
	time.Sleep(time.Duration(len(newVolumeCreateRequest.VolumeNames)*60) * time.Second)
	text1, err := executeKubernetesCommand("kubectl get pvc")
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(text1)))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		oneInstance = strings.Fields(scanner.Text())

		if oneInstance[1] == "Bound" && stringInSlice(oneInstance[0], newVolumeCreateRequest.VolumeNames) {
			newPVC1 := VolumeCreateResponse{NAME: oneInstance[0], STATUS: oneInstance[1], VOLUME: oneInstance[2], CAPACITY: oneInstance[3], ACMODES: oneInstance[4], STORAGECLASS: oneInstance[5], AGE: oneInstance[6]}
			VolumesCreatedFromKubernetes = append(VolumesCreatedFromKubernetes, newPVC1)
		}

	}
	fmt.Println(VolumesCreatedFromKubernetes)

	if len(VolumesCreatedFromKubernetes) == len(newVolumeCreateRequest.VolumeNames) {
		fmt.Println("all volumes are created successfully")
		return &VolumesCreatedFromKubernetes, nil
	}
	err = errors.New("Failed to create all volumes ")
	return nil, err
}

// searches given string in givn array
func stringInSlice(str string, list []string) bool {
	for _, v := range list {
		if v == str {
			return true
		}
	}
	return false
}

// CreateVolumeList will create list of volume names to be created
func CreateVolumeList(VolCount int, uniqueID int) []string {
	VolumeList := []string{}

	for i := 0; i < VolCount; i++ {
		VolumeList = append(VolumeList, "volume"+strconv.Itoa(uniqueID)+strconv.Itoa(i))
	}
	return VolumeList
}

//executeKubernetesCommand will execute the given kubernetes command
func executeKubernetesCommand(command string) ([]byte, error) {
	KubernetesConnObj, err := CreateSSH()
	if err != nil {
		return nil, err
	}
	text1, err := KubernetesConnObj.CombinedOutput(command)

	return text1, err
}

// CreateSSH creates ssh connection to the kubernetes master
func CreateSSH() (*ssh.Session, error) {
	// add these entries in env file
	TargetHostIP := os.Getenv("KUBERNETESMASTERIP")
	UserName := os.Getenv("USERNAME")
	PassWord := os.Getenv("PASSWORD")
	if TargetHostIP == "" || UserName == "" || PassWord == "" {
		fmt.Println("please set KUBERNETES_MASTER_IP , USERNAME and PASSWORD")
		err := errors.New("please set KUBERNETES_MASTER_IP , USERNAME and PASSWORD")
		return nil, err
	}
	sshConfig := &ssh.ClientConfig{
		User: UserName,
		Auth: []ssh.AuthMethod{
			ssh.Password(PassWord),
		},
	}
	HostIP := TargetHostIP + ":22"
	sshConfig.HostKeyCallback = ssh.InsecureIgnoreHostKey()
	connection, err := ssh.Dial("tcp", HostIP, sshConfig)
	if err != nil {
		return nil, err
	}
	session, err := connection.NewSession()

	return session, err
}

//ValidatePvDelete will validate whether volumes are deleted successfully and returns error status
func ValidatePvDelete(newVolumeCreateRequest *VolumeCreateRequest) error {
	var oneInstance []string
	volCount := 0
	// creating list for volume deletion
	text1, err := executeKubernetesCommand("kubectl get pvc")
	if err != nil {
		return err
	}
	scanner := bufio.NewScanner(strings.NewReader(string(text1)))
	scanner.Split(bufio.ScanLines)
	for scanner.Scan() {
		oneInstance = strings.Fields(scanner.Text())

		if stringInSlice(oneInstance[0], newVolumeCreateRequest.VolumeNames) {
			volCount++
		}

	}
	if volCount == 0 {
		fmt.Println("all volumes are deleted successfully")
		return nil
	}
	err = errors.New("Failed to delete all volumes ")
	return err
}

//initialising volumecreateRequest object with some default values
func defaultVolumeRequest() (volumeRequest *VolumeCreateRequest) {
	VolList := []string{}
	VolList = append(VolList, "volume1")
	volumeRequest = &VolumeCreateRequest{
		VolumeNames:  VolList,
		SYMID:        "000197600196",
		SRP:          "DEFAULT_SRP",
		ServiceLevel: "Diamond",
		AccessModes:  "ReadWriteOnce",
		VolSize:      "10Gi",
	}
	return volumeRequest
}
