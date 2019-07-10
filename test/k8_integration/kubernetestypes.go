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
