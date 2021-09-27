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
package k8integration

import (
	"fmt"
	"math/rand"
	"strings"

	"github.com/cucumber/godog"
)

type feature struct {
	errs                 []error
	volumeCreateRequest  *VolumeCreateRequest
	volumeCreateResponse *[]VolumeCreateResponse
	scenarioID           int
}

func (f *feature) addError(err error) {
	f.errs = append(f.errs, err)
}

func (f *feature) kubernetesSessionIsActive() error {
	_, err := CreateSSH()
	if err != nil {
		fmt.Println("Kubernetes cluster session is not active")
		return err
	}

	f.volumeCreateRequest = defaultVolumeRequest()
	f.volumeCreateResponse = nil
	f.scenarioID = rand.Int()
	f.errs = make([]error, 0)

	return nil
}
func (f *feature) controllerIsRunning() error {
	fmt.Println("Checking for controller plugin service")
	podStatus := ControllerIsRunning()
	if podStatus == nil {
		fmt.Println("controller plugin service is running successfully")
	}
	return podStatus
}

func (f *feature) iCallCreateVolume() error {
	fmt.Println("creating Storage class and PVC")
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)
	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil
}

func (f *feature) verifyVolumesAreCreated() error {
	var err error
	fmt.Println("Checking for errors")
	f.volumeCreateResponse, err = ValidatePvcreate(f.volumeCreateRequest)
	if err != nil {
		fmt.Println("volume creation is failed")
		return f.errs[0]

	}
	return nil
}

func (f *feature) iValidateVolumeDetailsAgainstUnisphere() error {
	return nil
}

func (f *feature) iCallDeleteVolume() error {
	fmt.Println("deleting PV and SC")
	err := DeletePV(f.scenarioID)
	if err != nil {
		fmt.Println("volume delete is failed")
		f.addError(err)
	}
	return nil
}

func (f *feature) iCallCreateVolumeWithName(VolName string) error {
	fmt.Println("creating volume with name")
	f.volumeCreateRequest.VolumeNames[0] = VolName
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)
	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil

}

func (f *feature) verifyVolumesAreNotCreated() error {
	var err error
	fmt.Println("Checking for errors")
	f.volumeCreateResponse, err = ValidatePvcreate(f.volumeCreateRequest)
	if err != nil && strings.Contains(err.Error(), "Failed to create") {
		fmt.Printf("Failed to create all volumes %s:\n", err.Error())
		return nil
	}
	return err
}

func (f *feature) iCallCreateVolumeWithNameAndSize(VolName string, VolSize string) error {
	fmt.Println("creating volume with name")
	f.volumeCreateRequest.VolumeNames[0] = VolName
	f.volumeCreateRequest.VolSize = VolSize
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)
	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil

}

func (f *feature) iCallCreateVolumeWithDifferentSize(VolSize string) error {
	fmt.Println("creating volume with different size ")
	f.volumeCreateRequest.VolSize = VolSize
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)

	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil

}

func (f *feature) iCallCreateVolumeWithSLOption(Sl string) error {
	fmt.Println("creating volume with service level name")
	f.volumeCreateRequest.ServiceLevel = Sl
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)
	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil

}
func (f *feature) iCallCreateMultipleVolumes(NoofVolumes int) error {
	fmt.Println("creating No of volumes")
	f.volumeCreateRequest.VolumeNames = CreateVolumeList(NoofVolumes, f.scenarioID)
	err := CreatePV(f.volumeCreateRequest, f.scenarioID)
	if err != nil {
		fmt.Printf("creation of PVC is failed %s:\n", err.Error())
		f.addError(err)
	}
	return nil
}
func (f *feature) verifyVolumesAreDeleted() error {
	err := ValidatePvDelete(f.volumeCreateRequest)
	if err != nil && strings.Contains(err.Error(), "Failed to delete") {
		fmt.Printf("Failed to create all volumes %s:\n", err.Error())
		return f.errs[0]

	}
	return nil

}

func FeatureContext(s *godog.Suite) {
	f := &feature{}
	s.Step(`^Kubernetes session is active$`, f.kubernetesSessionIsActive)
	s.Step(`^I call Create Volume$`, f.iCallCreateVolume)
	s.Step(`^verify volumes are created$`, f.verifyVolumesAreCreated)
	s.Step(`^I validate volume details against Unisphere`, f.iValidateVolumeDetailsAgainstUnisphere)
	s.Step(`^I call DeleteVolume$`, f.iCallDeleteVolume)
	s.Step(`^I call Create Volume with Name "([^"]*)"$`, f.iCallCreateVolumeWithName)
	s.Step(`^verify volumes are not created$`, f.verifyVolumesAreNotCreated)
	s.Step(`^I call Create Volume with Name and Size "([^"]*)" "([^"]*)"$`, f.iCallCreateVolumeWithNameAndSize)
	s.Step(`^I call Create Volume with Different Size "([^"]*)"$`, f.iCallCreateVolumeWithDifferentSize)
	s.Step(`^I call Create Volume with SL Option "([^"]*)"$`, f.iCallCreateVolumeWithSLOption)
	s.Step(`^I call Create multiple volumes (\d+)$`, f.iCallCreateMultipleVolumes)
	s.Step(`^verify volumes are deleted$`, f.verifyVolumesAreDeleted)
}
