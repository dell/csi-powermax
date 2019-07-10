package types

// This file contains structures declaration of REST paylod to Unisphere

// StorageGroupIDList : list of sg's
type StorageGroupIDList struct {
	StorageGroupIDs []string `json:"storageGroupId"`
}

// StorageGroup holds all the fields of an SG
type StorageGroup struct {
	StorageGroupID     string   `json:"storageGroupId"`
	SLO                string   `json:"slo"`
	SRP                string   `json:"srp"`
	Workload           string   `json:"workload"`
	SLOCompliance      string   `json:"slo_compliance"`
	NumOfVolumes       int      `json:"num_of_vols"`
	NumOfChildSGs      int      `json:"num_of_child_sgs"`
	NumOfParentSGs     int      `json:"num_of_parent_sgs"`
	NumOfMaskingViews  int      `json:"num_of_masking_views"`
	NumOfSnapshots     int      `json:"num_of_snapshots"`
	CapacityGB         float64  `json:"cap_gb"`
	DeviceEmulation    string   `json:"device_emulation"`
	Type               string   `type:"type"`
	Unprotected        bool     `type:"unprotected"`
	ChildStorageGroup  []string `json:"child_storage_group"`
	ParentStorageGroup []string `json:"parent_storage_group"`
	MaskingView        []string `json:"maskingview"`
}

// StorageGroupResult holds result of an operation
type StorageGroupResult struct {
	StorageGroup []StorageGroup `json:"storageGroup"`
	Success      bool           `json:"success"`
	Message      string         `json:"message"`
}

// CreateStorageGroupParam : Payload for creating Storage Group
type CreateStorageGroupParam struct {
	StorageGroupID            string                      `json:"storageGroupId,omitempty"`
	CreateEmptyStorageGroup   bool                        `json:"create_empty_storage_group,omitempty"`
	SRPID                     string                      `json:"srpId,omitempty"`
	SLOBasedStorageGroupParam []SLOBasedStorageGroupParam `json:"sloBasedStorageGroupParam,omitempty"`
	Emulation                 string                      `json:"emulation,omitempty"`
	ExecutionOption           string                      `json:"executionOption,omitempty"`
}

// MergeStorageGroupParam : Payloads for updating Storage Group
type MergeStorageGroupParam struct {
	StorageGroupID string `json:"storageGroupId,omitempty"`
}

// SplitStorageGroupVolumesParam holds parameters to split
type SplitStorageGroupVolumesParam struct {
	VolumeIDs      []string `json:"volumeId,omitempty"`
	StorageGroupID string   `json:"storageGroupId,omitempty"`
	MaskingViewID  string   `json:"maskingViewId,omitempty"`
}

// SplitChildStorageGroupParam holds param to split
// child SG
type SplitChildStorageGroupParam struct {
	StorageGroupID string `json:"storageGroupId,omitempty"`
	MaskingViewID  string `json:"maskingViewId,omitempty"`
}

// MoveVolumeToStorageGroupParam stores parameters to
// move volumes to SG
type MoveVolumeToStorageGroupParam struct {
	VolumeIDs      []string `json:"volumeId,omitempty"`
	StorageGroupID string   `json:"storageGroupId,omitempty"`
	Force          bool     `json:"force,omitempty"`
}

// EditCompressionParam hold param to edit compression
// attribute with an SG
type EditCompressionParam struct {
	Compression bool `json:"compression,omitempty"`
}

// SetHostIOLimitsParam holds param to set host IO limit
type SetHostIOLimitsParam struct {
	HostIOLimitMBSec    string `json:"host_io_limit_mb_sec,omitempty"`
	HostIOLimitIOSec    string `json:"host_io_limit_io_sec,omitempty"`
	DynamicDistribution string `json:"dynamicDistribution,omitempty"`
}

// RemoveVolumeParam holds volume ids to remove from SG
type RemoveVolumeParam struct {
	VolumeIDs []string `json:"volumeId,omitempty"`
}

// AddExistingStorageGroupParam contains SG ids and compliance alert flag
type AddExistingStorageGroupParam struct {
	StorageGroupIDs        []string `json:"storageGroupId,omitempty"`
	EnableComplianceAlerts bool     `json:"enableComplianceAlerts,omitempty"`
}

// SLOBasedStorageGroupParam holds parameters related to an SG and SLO
type SLOBasedStorageGroupParam struct {
	SLOID                                          string                `json:"sloId,omitempty"`
	WorkloadSelection                              string                `json:"workloadSelection,omitempty"`
	NumberOfVolumes                                int                   `json:"num_of_vols"`
	VolumeAttribute                                VolumeAttributeType   `json:"volumeAttribute,omitempty"`
	AllocateCapacityForEachVol                     bool                  `json:"allocate_capacity_for_each_vol,omitempty"`
	PersistPrealloctedCapacityThroughReclaimOrCopy bool                  `json:"persist_preallocated_capacity_through_reclaim_or_copy,omitempty"`
	NoCompression                                  bool                  `json:"noCompression,omitempty"`
	VolumeIdentifier                               *VolumeIdentifierType `json:"volumeIdentifier,omitempty"`
	SetHostIOLimitsParam                           *SetHostIOLimitsParam `json:"setHostIOLimitsParam,omitempty"`
}

// AddNewStorageGroupParam contains parameters required to add a
// new storage group
type AddNewStorageGroupParam struct {
	SRPID                     string                      `json:"srpId,omitempty"`
	SLOBasedStorageGroupParam []SLOBasedStorageGroupParam `json:"sloBasedStorageGroupParam,omitempty"`
	Emulation                 string                      `json:"emulation,omitempty"`
	EnableComplianceAlerts    bool                        `json:"enableComplianceAlerts,omitempty"`
}

// SpecificVolumeParam holds volume ids, volume attributes and RDF group num
type SpecificVolumeParam struct {
	VolumeIDs       []string            `json:"volumeId,omitempty"`
	VolumeAttribute VolumeAttributeType `json:"volumeAttribute,omitempty"`
	RDFGroupNumber  int                 `json:"rdfGroupNumber,omitempty"`
}

// AllVolumeParam contains volume attributes and RDF group number
type AllVolumeParam struct {
	VolumeAttribute VolumeAttributeType `json:"volumeAttribute,omitempty"`
	RDFGroupNumber  int                 `json:"rdfGroupNumber,omitempty"`
}

// ExpandVolumesParam holds parameters to expand volumes
type ExpandVolumesParam struct {
	SpecificVolumeParam SpecificVolumeParam `json:"specificVolumeParam,omitempty"`
	AllVolumeParam      AllVolumeParam      `json:"allVolumeParam,omitempty"`
}

// AddSpecificVolumeParam holds volume ids
type AddSpecificVolumeParam struct {
	VolumeIDs []string `json:"volumeId,omitempty"`
}

// AddVolumeParam holds number volumes to add and related param
type AddVolumeParam struct {
	NumberOfVols     int                  `json:"num_of_vols,omitempty"`
	VolumeAttribute  VolumeAttributeType  `json:"volumeAttribute,omitempty"`
	Emulation        string               `json:"emulation,omitempty"`
	VolumeIdentifier VolumeIdentifierType `json:"volumeIdentifier,omitempty"`
}

// ExpandStorageGroupParam holds params related to expanding size of an SG
type ExpandStorageGroupParam struct {
	AddExistingStorageGroupParam *AddExistingStorageGroupParam `json:"addExistingStorageGroupParam,omitempty"`
	AddNewStorageGroupParam      *AddNewStorageGroupParam      `json:"addNewStorageGroupParam,omitempty"`
	ExpandVolumesPar1Gam         *ExpandVolumesParam           `json:"expandVolumesParam,omitempty"`
	AddSpecificVolumeParam       *AddSpecificVolumeParam       `json:"addSpecificVolumeParam,omitempty"`
	AddVolumeParam               *AddVolumeParam               `json:"addVolumeParam,omitempty"`
}

// EditStorageGroupWorkloadParam holds selected work load
type EditStorageGroupWorkloadParam struct {
	WorkloadSelection string `json:"workloadSelection,omitempty,omitempty"`
}

// EditStorageGroupSLOParam hold param to change SLOs
type EditStorageGroupSLOParam struct {
	SLOID string `json:"sloId,omitempty"`
}

// EditStorageGroupSRPParam holds param to change SRPs
type EditStorageGroupSRPParam struct {
	SRPID string `json:"srpId,omitempty"`
}

// RemoveStorageGroupParam holds parameters to remove an SG
type RemoveStorageGroupParam struct {
	StorageGroupIDs []string `json:"storageGroupId,omitempty"`
	Force           bool     `json:"force,omitempty"`
}

// RenameStorageGroupParam holds new name of a storage group
type RenameStorageGroupParam struct {
	NewStorageGroupName string `json:"new_storage_Group_name,omitempty"`
}

// EditStorageGroupActionParam holds parameters to modify an SG
type EditStorageGroupActionParam struct {
	MergeStorageGroupParam        *MergeStorageGroupParam        `json:"mergeStorageGroupParam,omitempty"`
	SplitStorageGroupVolumesParam *SplitStorageGroupVolumesParam `json:"splitStorageGroupVolumesParam,omitempty"`
	SplitChildStorageGroupParam   *SplitChildStorageGroupParam   `json:"splitChildStorageGroupParam,omitempty"`
	MoveVolumeToStorageGroupParam *MoveVolumeToStorageGroupParam `json:"moveVolumeToStorageGroupParam,omitempty"`
	EditCompressionParam          *EditCompressionParam          `json:"editCompressionParam,omitempty"`
	SetHostIOLimitsParam          *SetHostIOLimitsParam          `json:"setHostIOLimitsParam,omitempty"`
	RemoveVolumeParam             *RemoveVolumeParam             `json:"removeVolumeParam,omitempty"`
	ExpandStorageGroupParam       *ExpandStorageGroupParam       `json:"expandStorageGroupParam,omitempty"`
	EditStorageGroupWorkloadParam *EditStorageGroupWorkloadParam `json:"editStorageGroupWorkloadParam,omitempty"`
	EditStorageGroupSLOParam      *EditStorageGroupSLOParam      `json:"editStorageGroupSLOParam,omitempty"`
	EditStorageGroupSRPParam      *EditStorageGroupSRPParam      `json:"editStorageGroupSRPParam,omitempty"`
	RemoveStorageGroupParam       *RemoveStorageGroupParam       `json:"removeStorageGroupParam,omitempty"`
	RenameStorageGroupParam       *RenameStorageGroupParam       `json:"renameStorageGroupParam,omitempty"`
}

// ExecutionOptionSynchronous : execute tasks synchronously
const ExecutionOptionSynchronous = "SYNCHRONOUS"

// ExecutionOptionAsynchronous : execute tasks asynchronously
const ExecutionOptionAsynchronous = "ASYNCHRONOUS"

// UpdateStorageGroupPayload : updates SG rest paylod
type UpdateStorageGroupPayload struct {
	EditStorageGroupActionParam EditStorageGroupActionParam `json:"editStorageGroupActionParam"`
	// ExecutionOption "SYNCHRONOUS" or "ASYNCHRONOUS"
	ExecutionOption string `json:"executionOption"`
}

// UseExistingStorageGroupParam : use this sg ID
type UseExistingStorageGroupParam struct {
	StorageGroupID string `json:"storageGroupId,omitempty"`
}
