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

// MetadataSC struct  had name details
type MetadataSC struct {
	Name string `json:"name"`
}

//ParametersSC strucd had symid, srp details
type ParametersSC struct {
	Symid        string `json:"SYMID"`
	Srp          string `json:"SRP"`
	ServiceLevel string `json:"ServiceLevel"`
}

//StorageClass struct details for all the parameters of sc
type StorageClass struct {
	Apiversion    string       `json:"apiVersion"`
	Kind          string       `json:"kind"`
	Metadata      MetadataSC   `json:"metadata"`
	Provisioner   string       `json:"provisioner"`
	ReclaimPolicy string       `json:"reclaimPolicy"`
	Parameters    ParametersSC `json:"parameters"`
}

// MetadataPVC Struct had details of name of  pvc
type MetadataPVC struct {
	Name string `json:"name"`
}

// ResourcesPVC Struct parameters for  Persistent volume claim resouces
type ResourcesPVC struct {
	Requests RequestsPVC `json:"requests"`
}

// RequestsPVC Struct parameters  for  Persistent volume claim requests
type RequestsPVC struct {
	Storage string `json:"storage"`
}

// SpecPVC Struct parameters  for  spec PVC
type SpecPVC struct {
	AccessModes      []string     `json:"accessModes"`
	Resources        ResourcesPVC `json:"resources"`
	StorageClassName string       `json:"storageClassName"`
}

// PVC Struct  had all parameters  for  Persistent volume claim
type PVC struct {
	Apiversion string      `json:"apiVersion"`
	Kind       string      `json:"kind"`
	Metadata   MetadataPVC `json:"metadata"`
	Spec       SpecPVC     `json:"spec"`
}
