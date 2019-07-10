package types

import (
	"strings"
)

// Error : contains fields to report rest interface errors
type Error struct {
	Message        string `json:"message"`
	HTTPStatusCode int    `json:"httpStatusCode"`
	ErrorCode      int    `json:"errorCode"`
}

func (e Error) Error() string {
	return e.Message
}

// Version : /unixmax/restapi/system/version
type Version struct {
	Version string `json:"version"`
}

// SymmetrixIDList : contains list of symIDs
type SymmetrixIDList struct {
	SymmetrixIDs []string `json:"symmetrixID"`
}

// Symmetrix : information about a Symmetrix system
type Symmetrix struct {
	SymmetrixID    string `json:"symmetrixID"`
	DeviceCount    int    `json:"device_count"`
	Ucode          string `json:"ucode"`
	Model          string `json:"model"`
	Local          bool   `json:"local"`
	AllFlash       bool   `json:"all_flash"`
	DisplayName    string `json:"display_name"`
	DiskCount      int    `json:"disk_count"`
	CacheSizeMB    int    `json:"cache_size_mb"`
	DataEncryption string `json:"data_encryption"`
}

// StoragePoolList : list of storage pools in the system
type StoragePoolList struct {
	StoragePoolIDs []string `json:"srpID"`
}

// StoragePool : information about a storage pool
type StoragePool struct {
	StoragePoolID        string         `json:"srpID"`
	DiskGrouCount        int            `json:"num_of_disk_groups"`
	Description          string         `json:"description"`
	Emulation            string         `json:"emulation"`
	CompressionState     string         `json:"compression_state"`
	EffectiveUsedCapPerc int            `json:"effective_used_capacity_percent"`
	ReservedCapPerc      int            `json:"reserved_cap_percent"`
	SrdfDseAllocCap      float64        `json:"total_srdf_dse_allocated_cap_gb"`
	RdfaDse              bool           `json:"rdfa_dse"`
	DiskGroupIDs         []string       `json:"diskGroupId"`
	ExternalCap          float64        `json:"external_capacity_gb"`
	SrpCap               *SrpCap        `json:"srp_capacity"`
	SrpEfficiency        *SrpEfficiency `json:"srp_efficiency"`
}

// SrpCap : capacity of an SRP
type SrpCap struct {
	SubAllocCapInTB float64 `json:"subscribed_allocated_tb"`
	SubTotInTB      float64 `json:"subscribed_total_tb"`
	SnapModInTB     float64 `json:"snapshot_modified_tb"`
	SnapTotInTB     float64 `json:"snapshot_total_tb"`
	UsableUsedInTB  float64 `json:"usable_used_tb"`
	UsableTotInTB   float64 `json:"usable_total_tb"`
}

// SrpEfficiency : efficiency attributes of an SRP
type SrpEfficiency struct {
	EfficiencyRatioToOne     float32 `json:"overall_efficiency_ratio_to_one"`
	DataReductionRatioToOne  float32 `json:"data_reduction_ratio_to_one"`
	DataReductionEnabledPerc float32 `json:"data_reduction_enabled_percent"`
	VirtProvSavingRatioToOne float32 `json:"virtual_provisioning_savings_ratio_to_one"`
	SanpSavingRatioToOne     float32 `json:"snapshot_savings_ratio_to_one"`
}

// constants of storage units
const (
	CapacityUnitTb  = "TB"
	CapacityUnitGb  = "GB"
	CapacityUnitMb  = "MB"
	CapacityUnitCyl = "CYL"
)

// VolumeAttributeType : volume attributes
type VolumeAttributeType struct {
	CapacityUnit string `json:"capacityUnit"` // CAPACITY_UNIT_{TB,GB,MB,CYL}
	VolumeSize   string `json:"volume_size"`
}

// VolumeIdentifierType : volume identifier
type VolumeIdentifierType struct {
	VolumeIdentifierChoice string `json:"volumeIdentifierChoice,omitempty"`
	IdentifierName         string `json:"identifier_name,omitempty"`
	AppendNumber           string `json:"append_number,omitempty"`
}

// Link : key and URI
type Link struct {
	Key string   `json:"key"`
	URI []string `json:"uri"`
}

// Task : holds execution order with a description
type Task struct {
	ExecutionOrder int    `json:"execution_order"`
	Description    string `json:"description"`
}

// constants
const (
	JobStatusUnscheduled = "UNSCHEDULED"
	JobStatusScheduled   = "SCHEDULED"
	JobStatusSucceeded   = "SUCCEEDED"
	JobStatusFailed      = "FAILED"
	JobStatusRunning     = "RUNNING"
)

// JobIDList : list of Job ids
type JobIDList struct {
	JobIDs []string `json:"jobId"`
}

// Job : information about a job
type Job struct {
	JobID                    string `json:"jobId"`
	Name                     string `json:"name"`
	Status                   string `json:"status"`
	Username                 string `json:"username"`
	LastModifiedDate         string `json:"last_modified_date"`
	LastModifiedMilliseconds int64  `json:"last_modified_milliseconds"`
	ScheduledDate            string `json:"scheduled_date"`
	ScheduledMilliseconds    int64  `json:"scheduled_milliseconds"`
	CompletedDate            string `json:"completed_date"`
	CompletedMilliseconds    int64  `json:"completed_milliseconds"`
	Tasks                    []Task `json:"task"`
	ResourceLink             string `json:"resourceLink"`
	Result                   string `json:"result"`
	Links                    []Link `json:"links"`
}

// GetJobResource parses the Resource link and returns three things:
// The 1) the symmetrixID, 2) the resource type (e.g.) volume, and 3) the resourceID
// If the Resource Link cannot be parsed, empty strings are returned.
func (j *Job) GetJobResource() (string, string, string) {
	if j.ResourceLink == "" {
		return "", "", ""
	}
	parts := strings.Split(j.ResourceLink, "/")
	nparts := len(parts)
	if nparts < 3 {
		return "", "", ""
	}
	return parts[nparts-3], parts[nparts-2], parts[nparts-1]
}

// PortGroupList : list of port groups
type PortGroupList struct {
	PortGroupIDs []string `json:"portGroupId"`
}

// PortKey : combination of a port and a key
type PortKey struct {
	DirectorID string `json:"directorId"`
	PortID     string `json:"portId"`
}

// PortGroup : Information about a port group
type PortGroup struct {
	PortGroupID        string    `json:"portGroupId"`
	SymmetrixPortKey   []PortKey `json:"symmetrixPortKey"`
	NumberPorts        int64     `json:"num_of_ports"`
	NumberMaskingViews int64     `json:"number_of_masking_views"`
	PortGroupType      string    `json:"type"`
	MaskingView        []string  `json:"maskingview"`
}

// InitiatorList : list of initiators
type InitiatorList struct {
	InitiatorIDs []string `json:"initiatorId"`
}

// Initiator : Information about an initiator
type Initiator struct {
	InitiatorID          string    `json:"initiatorId"`
	SymmetrixPortKey     []PortKey `json:"symmetrixPortKey"`
	InitiatorType        string    `json:"type"`
	FCID                 string    `json:"fcid,omitempty"`
	IPAddress            string    `json:"ip_address,omitempty"`
	HostID               string    `json:"host,omitempty"`
	HostGroupIDs         []string  `json:"hostGroup,omitempty"`
	LoggedIn             bool      `json:"logged_in"`
	OnFabric             bool      `json:"on_fabric"`
	FlagsInEffect        string    `json:"flags_in_effect"`
	NumberVols           int64     `json:"num_of_vols"`
	NumberHostGroups     int64     `json:"num_of_host_groups"`
	NumberMaskingViews   int64     `json:"number_of_masking_views"`
	NumberPowerPathHosts int64     `json:"num_of_powerpath_hosts"`
}

// HostList : list of hosts
type HostList struct {
	HostIDs []string `json:"hostId"`
}

// Host : Information about a host
type Host struct {
	HostID             string   `json:"hostId"`
	NumberMaskingViews int64    `json:"num_of_masking_views"`
	NumberInitiators   int64    `json:"num_of_initiators"`
	NumberHostGroups   int64    `json:"num_of_host_groups"`
	PortFlagsOverride  bool     `json:"port_flags_override"`
	ConsistentLun      bool     `json:"consistent_lun"`
	EnabledFlags       string   `json:"enabled_flags"`
	DisabledFlags      string   `json:"disabled_flags"`
	HostType           string   `json:"type"`
	Initiators         []string `json:"initiator"`
	MaskingviewIDs     []string `json:"maskingview"`
	NumPowerPathHosts  int64    `json:"num_of_powerpath_hosts"`
}

// DirectorIDList : list of directors
type DirectorIDList struct {
	DirectorIDs []string `json:"directorId"`
}

// PortList : list of ports
type PortList struct {
	SymmetrixPortKey []PortKey `json:"symmetrixPortKey"`
}

// SymmetrixPortType : type of symmetrix port
type SymmetrixPortType struct {
	ISCSITarget bool     `json:"iscsi_target,omitempty"`
	IPAddresses []string `json:"ip_addresses,omitempty"`
	Identifier  string   `json:"identifier,omitempty"`
}

// Port is a minimal represation of a Symmetrix Port for iSCSI target purpose
type Port struct {
	SymmetrixPort SymmetrixPortType `json:"symmetrixPort"`
}
