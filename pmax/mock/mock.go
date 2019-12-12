package mock

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	types "github.com/dell/csi-powermax/pmax/types/v90"
	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// constants
const (
	APIVersion              = "90"
	PREFIX                  = "/univmax/restapi/" + APIVersion
	PREFIXNOVERSION         = "/univmax/restapi"
	defaultUsername         = "username"
	defaultPassword         = "password"
	Debug                   = false
	DefaultStorageGroup     = "CSI-Test-SG-1"
	DefaultStorageGroup1    = "CSI-Test-SG-2"
	DefaultSymmetrixID      = "000197900046"
	PostELMSRSymmetrixID    = "000197900047"
	DefaultStoragePool      = "SRP_1"
	DefaultServiceLevel     = "Optimized"
	DefaultFcStoragePortWWN = "5000000000000001"
)

// Data are internal tables the Mock Unisphere uses to provide functionality.
var Data struct {
	VolumeIDToIdentifier          map[string]string
	VolumeIDToSize                map[string]int
	VolumeIDIteratorList          []string
	VolumeIDToSGList              map[string][]string
	MaskingViewIDToHostID         map[string]string
	MaskingViewIDToHostGroupID    map[string]string
	MaskingViewIDToPortGroupID    map[string]string
	MaskingViewIDToStorageGroupID map[string]string
	StorageGroupIDToMaskingViewID map[string]string
	JobIDToMockJob                map[string]*JobInfo
	StorageGroupIDToNVolumes      map[string]int
	StorageGroupIDToStorageGroup  map[string]*types.StorageGroup
	StorageGroupIDToVolumes       map[string][]string
	MaskingViewIDToMaskingView    map[string]*types.MaskingView
	InitiatorIDToInitiator        map[string]*types.Initiator
	HostIDToHost                  map[string]*types.Host
	PortGroupIDToPortGroup        map[string]*types.PortGroup
	PortIDToSymmetrixPortType     map[string]*types.SymmetrixPortType
	VolumeIDToVolume              map[string]*types.Volume
	JSONDir                       string
	InitiatorHost                 string
}

// InducedErrors constants
var InducedErrors struct {
	NoConnection                   bool
	InvalidJSON                    bool
	BadHTTPStatus                  int
	GetSymmetrixError              bool
	GetVolumeIteratorError         bool
	GetVolumeError                 bool
	UpdateVolumeError              bool
	DeleteVolumeError              bool
	DeviceInSGError                bool
	GetStorageGroupError           bool
	InvalidResponse                bool
	GetStoragePoolError            bool
	UpdateStorageGroupError        bool
	GetJobError                    bool
	JobFailedError                 bool
	VolumeNotCreatedError          bool
	GetJobCannotFindRoleForUser    bool
	CreateStorageGroupError        bool
	StorageGroupAlreadyExists      bool
	DeleteStorageGroupError        bool
	GetStoragePoolListError        bool
	GetPortGroupError              bool
	GetPortError                   bool
	GetDirectorError               bool
	GetInitiatorError              bool
	GetInitiatorByIDError          bool
	GetHostError                   bool
	CreateHostError                bool
	DeleteHostError                bool
	UpdateHostError                bool
	GetMaskingViewError            bool
	CreateMaskingViewError         bool
	MaskingViewAlreadyExists       bool
	DeleteMaskingViewError         bool
	PortGroupNotFoundError         bool
	InitiatorGroupNotFoundError    bool
	StorageGroupNotFoundError      bool
	VolumeNotAddedError            bool
	GetMaskingViewConnectionsError bool
	ResetAfterFirstError           bool
}

// hasError checks to see if the specified error (via pointer)
// is set. If so it returns true, else false.
// Additionally if ResetAfterFirstError is set, the first error
// condition will be reset to no longer be an error condition.
func hasError(errorType *bool) bool {
	if *errorType {
		if InducedErrors.ResetAfterFirstError {
			*errorType = false
			InducedErrors.ResetAfterFirstError = false
		}
		return true
	}
	return false
}

// Reset : re-initializes the variables
func Reset() {
	InducedErrors.NoConnection = false
	InducedErrors.InvalidJSON = false
	InducedErrors.BadHTTPStatus = 0
	InducedErrors.GetSymmetrixError = false
	InducedErrors.GetVolumeIteratorError = false
	InducedErrors.GetVolumeError = false
	InducedErrors.UpdateVolumeError = false
	InducedErrors.DeleteVolumeError = false
	InducedErrors.DeviceInSGError = false
	InducedErrors.GetStorageGroupError = false
	InducedErrors.InvalidResponse = false
	InducedErrors.UpdateStorageGroupError = false
	InducedErrors.GetJobError = false
	InducedErrors.JobFailedError = false
	InducedErrors.VolumeNotCreatedError = false
	InducedErrors.GetJobCannotFindRoleForUser = false
	InducedErrors.CreateStorageGroupError = false
	InducedErrors.StorageGroupAlreadyExists = false
	InducedErrors.DeleteStorageGroupError = false
	InducedErrors.GetStoragePoolListError = false
	InducedErrors.GetStoragePoolError = false
	InducedErrors.GetPortGroupError = false
	InducedErrors.GetPortError = false
	InducedErrors.GetDirectorError = false
	InducedErrors.GetInitiatorError = false
	InducedErrors.GetInitiatorByIDError = false
	InducedErrors.GetHostError = false
	InducedErrors.CreateHostError = false
	InducedErrors.DeleteHostError = false
	InducedErrors.UpdateHostError = false
	InducedErrors.GetMaskingViewError = false
	InducedErrors.CreateMaskingViewError = false
	InducedErrors.MaskingViewAlreadyExists = false
	InducedErrors.DeleteMaskingViewError = false
	InducedErrors.PortGroupNotFoundError = false
	InducedErrors.InitiatorGroupNotFoundError = false
	InducedErrors.StorageGroupNotFoundError = false
	InducedErrors.VolumeNotAddedError = false
	InducedErrors.GetMaskingViewConnectionsError = false
	InducedErrors.ResetAfterFirstError = false
	Data.JSONDir = "mock"
	Data.VolumeIDToIdentifier = make(map[string]string)
	Data.VolumeIDToSize = make(map[string]int)
	Data.VolumeIDIteratorList = make([]string, 0)
	Data.VolumeIDToSGList = make(map[string][]string)
	Data.MaskingViewIDToHostID = make(map[string]string)
	Data.MaskingViewIDToHostGroupID = make(map[string]string)
	Data.MaskingViewIDToPortGroupID = make(map[string]string)
	Data.MaskingViewIDToStorageGroupID = make(map[string]string)
	Data.StorageGroupIDToMaskingViewID = make(map[string]string)
	Data.JobIDToMockJob = make(map[string]*JobInfo)
	Data.StorageGroupIDToNVolumes = make(map[string]int)
	Data.StorageGroupIDToNVolumes[DefaultStorageGroup] = 0
	Data.StorageGroupIDToStorageGroup = make(map[string]*types.StorageGroup)
	Data.MaskingViewIDToMaskingView = make(map[string]*types.MaskingView)
	Data.InitiatorIDToInitiator = make(map[string]*types.Initiator)
	Data.HostIDToHost = make(map[string]*types.Host)
	Data.PortGroupIDToPortGroup = make(map[string]*types.PortGroup)
	Data.PortIDToSymmetrixPortType = make(map[string]*types.SymmetrixPortType)
	Data.VolumeIDToVolume = make(map[string]*types.Volume)
	Data.StorageGroupIDToVolumes = make(map[string][]string)
	initMockCache()
}

func initMockCache() {
	// Initialize SGs
	AddStorageGroup("CSI-Test-SG-1", "SRP_1", "Diamond")
	AddStorageGroup("CSI-Test-SG-2", "SRP_1", "Diamond")
	AddStorageGroup("CSI-Test-SG-3", "SRP_2", "Silver")
	AddStorageGroup("CSI-Test-SG-4", "SRP_2", "Optimized")
	AddStorageGroup("CSI-Test-SG-5", "SRP_2", "None")
	AddStorageGroup("CSI-Test-SG-6", "None", "None")
	// ISCSI directors
	iscsiDir1 := "SE-1E"
	iscsidir1PortKey1 := iscsiDir1 + ":" + "4"
	//iscsiDir2 := "SE-2E"
	// FC directors
	fcDir1 := "FA-1D"
	fcDir2 := "FA-2D"
	fcDir1PortKey1 := fcDir1 + ":" + "5"
	fcDir2PortKey1 := fcDir2 + ":" + "1"
	// Add Port groups
	AddPortGroup("csi-pg", "Fibre", []string{fcDir1PortKey1, fcDir2PortKey1})
	// Initialize initiators
	// Initialize Hosts
	initNode1List := make([]string, 0)
	iqnNode1 := "iqn.1993-08.org.centos:01:5ae577b352a0"
	initNode1 := iscsidir1PortKey1 + ":" + iqnNode1
	initNode1List = append(initNode1List, iqnNode1)
	AddInitiator(initNode1, iqnNode1, "GigE", []string{iscsidir1PortKey1}, "")
	AddHost("CSI-Test-Node-1", "iSCSI", initNode1List)

	initNode2List := make([]string, 0)
	iqn1Node2 := "iqn.1993-08.org.centos:01:5ae577b352a1"
	iqn2Node2 := "iqn.1993-08.org.centos:01:5ae577b352a2"
	init1Node2 := iscsidir1PortKey1 + ":" + iqn1Node2
	init2Node2 := iscsidir1PortKey1 + ":" + iqn2Node2
	initNode2List = append(initNode2List, iqn1Node2)
	initNode2List = append(initNode2List, iqn2Node2)
	AddInitiator(init1Node2, iqn1Node2, "GigE", []string{iscsidir1PortKey1}, "")
	AddInitiator(init2Node2, iqn2Node2, "GigE", []string{iscsidir1PortKey1}, "")
	AddHost("CSI-Test-Node-2", "iSCSI", initNode2List)
	AddMaskingView("CSI-Test-MV-1", "CSI-Test-SG-1", "CSI-Test-Node-1", "iscsi_ports")

	initNode3List := make([]string, 0)
	hba1Node3 := "20000090fa9278dd"
	hba2Node3 := "20000090fa9278dc"
	init1Node3 := fcDir1PortKey1 + ":" + hba1Node3
	init2Node3 := fcDir2PortKey1 + ":" + hba1Node3
	init3Node3 := fcDir1PortKey1 + ":" + hba2Node3
	init4Node3 := fcDir2PortKey1 + ":" + hba2Node3
	AddInitiator(init1Node3, hba1Node3, "Fibre", []string{fcDir1PortKey1}, "")
	AddInitiator(init2Node3, hba1Node3, "Fibre", []string{fcDir2PortKey1}, "")
	AddInitiator(init3Node3, hba2Node3, "Fibre", []string{fcDir1PortKey1}, "")
	AddInitiator(init4Node3, hba2Node3, "Fibre", []string{fcDir2PortKey1}, "")
	initNode3List = append(initNode3List, hba1Node3)
	initNode3List = append(initNode3List, hba2Node3)
	AddHost("CSI-Test-Node-3-FC", "Fibre", initNode3List)
}

var mockRouter http.Handler

// GetHandler returns the http handler
func GetHandler() http.Handler {
	handler := http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if Debug {
				log.Printf("handler called: %s %s", r.Method, r.URL)
			}
			if InducedErrors.InvalidJSON {
				w.Write([]byte(`this is not json`))
			} else if InducedErrors.NoConnection {
				writeError(w, "No Connection", http.StatusRequestTimeout)
			} else if InducedErrors.BadHTTPStatus != 0 {
				writeError(w, "Internal Error", InducedErrors.BadHTTPStatus)
			} else {
				if mockRouter != nil {
					mockRouter.ServeHTTP(w, r)
				} else {
					getRouter().ServeHTTP(w, r)
				}
			}
		})
	return handler
}

func getRouter() http.Handler {
	router := mux.NewRouter()
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/host/{id}", handleHost)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/host", handleHost)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/initiator/{id}", handleInitiator)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/initiator", handleInitiator)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/portgroup/{id}", handlePortGroup)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/portgroup", handlePortGroup)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/storagegroup/{id}", handleStorageGroup)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/storagegroup", handleStorageGroup)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/maskingview/{mvID}/connections", handleMaskingViewConnections)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/maskingview/{mvID}", handleMaskingView)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/maskingview", handleMaskingView)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/srp/{id}", handleStorageResourcePool)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/srp", handleStorageResourcePool)
	router.HandleFunc(PREFIXNOVERSION+"/common/Iterator/{iterId}/page", handleIterator)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/volume/{volID}", handleVolume)
	router.HandleFunc(PREFIX+"/sloprovisioning/symmetrix/{symid}/volume", handleVolume)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/director/{director}/port/{id}", handlePort)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/director/{director}/port", handlePort)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/director/{id}", handleDirector)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/director", handleDirector)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/job/{jobID}", handleJob)
	router.HandleFunc(PREFIX+"/system/symmetrix/{symid}/job", handleJob)
	router.HandleFunc(PREFIX+"/system/symmetrix/{id}", handleSymmetrix)
	router.HandleFunc(PREFIX+"/system/symmetrix", handleSymmetrix)
	router.HandleFunc(PREFIX+"/system/version", handleVersion)
	router.HandleFunc("/", handleNotFound)
	mockRouter = router
	return router
}

// NewVolume creates a new mock volume with the specified characteristics.
func NewVolume(volumeID, volumeIdentifier string, size int, sgList []string) {
	Data.VolumeIDToIdentifier[volumeID] = volumeIdentifier
	fmt.Printf("NewVolume: id %s name %s\n", volumeID, volumeIdentifier)
	Data.VolumeIDToSize[volumeID] = size
	Data.VolumeIDToSGList[volumeID] = sgList
}

// GET /univmax/restapi/system/version
func handleVersion(w http.ResponseWriter, r *http.Request) {
	auth := defaultUsername + ":" + defaultPassword
	authExpected := fmt.Sprintf("Basic " + base64.StdEncoding.EncodeToString([]byte(auth)))
	// Check for valid credentials
	authSupplied := r.Header.Get("Authorization")
	if authExpected != authSupplied {
		writeError(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	w.Write([]byte(`{ "version": "V9.0.1.6" }`))
}

// GET /univmax/restapi/APIVersion/system/symmetrix/{id}"
// GET /univmax/restapi/APIVersion/system/symmetrix"
func handleSymmetrix(w http.ResponseWriter, r *http.Request) {
	if InducedErrors.GetSymmetrixError {
		writeError(w, "Error retrieving Symmetrix: induced error", http.StatusRequestTimeout)
		return
	}
	vars := mux.Vars(r)
	id := vars["id"]
	if id == "" {
		returnJSONFile(Data.JSONDir, "symmetrixList.json", w, nil)
	}
	if id != "000197900046" && id != "000197900047" {
		writeError(w, "Symmetrix not found", http.StatusNotFound)
		return
	}
	if id == "000197900046" {
		returnJSONFile(Data.JSONDir, "symmetrix46.json", w, nil)
	} else if id == "000197900047" {
		returnJSONFile(Data.JSONDir, "symmetrix47.json", w, nil)
	}
}

func handleStorageResourcePool(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	srpID := vars["id"]
	if InducedErrors.GetStoragePoolListError {
		writeError(w, "Error retrieving StoragePools: induced error", http.StatusRequestTimeout)
		return
	}
	if InducedErrors.GetStoragePoolError {
		writeError(w, "Error retrieving Storage Pool(s): induced error", http.StatusRequestTimeout)
		return
	}
	if srpID == "" {
		returnJSONFile(Data.JSONDir, "storageResourcePool.json", w, nil)
	}
	replacements := make(map[string]string)
	replacements["__SRP_ID__"] = "SRP_1"
	returnJSONFile(Data.JSONDir, "storage_pool_template.json", w, replacements)
}

// GET /univmax/restapi/API_VERSON/sloprovisioning/symmetrix/{id}/volume/{id}
// GET /univmax/restapi/API_VERSON/sloprovisioning/symmetrix/{id}/volume
func handleVolume(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	volID := vars["volID"]
	switch r.Method {
	case http.MethodGet:
		if volID == "" {
			if InducedErrors.GetVolumeIteratorError {
				writeError(w, "Error getting VolumeIterator: induced error", http.StatusRequestTimeout)
				return
			}
			// Here we want a volume iterator.
			var like bool
			queryParams := r.URL.Query()
			volumeIdentifier := queryParams.Get("volume_identifier")
			if strings.Contains(volumeIdentifier, "<like>") {
				like = true
				volumeIdentifier = strings.TrimPrefix(volumeIdentifier, "<like>")
			}
			// Copy data to Data.VolumeIDIteratorList, while checking for volumeIdentifier match if needed
			Data.VolumeIDIteratorList = make([]string, 0)
			for _, vol := range Data.VolumeIDToVolume {
				if volumeIdentifier != "" {
					if like {
						if !strings.Contains(vol.VolumeIdentifier, volumeIdentifier) {
							continue
						}
					} else {
						if vol.VolumeIdentifier != volumeIdentifier {
							continue
						}
					}
				}
				Data.VolumeIDIteratorList = append(Data.VolumeIDIteratorList, vol.VolumeID)
			}
			if Debug {
				fmt.Printf("Data.VolumeIDIteratorList %#v", Data.VolumeIDIteratorList)
			}
			iter := &types.VolumeIterator{
				Count:          len(Data.VolumeIDIteratorList),
				ID:             "Volume",
				MaxPageSize:    10,
				ExpirationTime: 0,
			}
			numberToDo := len(Data.VolumeIDIteratorList)
			if numberToDo > iter.MaxPageSize {
				numberToDo = iter.MaxPageSize
			}
			iter.ResultList.From = 1
			iter.ResultList.To = numberToDo
			for i := iter.ResultList.From - 1; i <= iter.ResultList.To-1; i++ {
				volIDList := types.VolumeIDList{VolumeIDs: Data.VolumeIDIteratorList[i]}
				iter.ResultList.VolumeList = append(iter.ResultList.VolumeList, volIDList)
			}
			if Debug {
				fmt.Printf("iter: %#v\n", iter)
			}
			encoder := json.NewEncoder(w)
			err := encoder.Encode(iter)
			if err != nil {
				writeError(w, "json encoding error", http.StatusInternalServerError)
			}
			return
		}
		if InducedErrors.GetVolumeError {
			writeError(w, "Error retrieving Volume: induced error", http.StatusRequestTimeout)
			return
		}
		if volID != "" {
			returnVolume(w, volID)
		}

	case http.MethodPut:
		if InducedErrors.UpdateVolumeError {
			writeError(w, "Error updating Volume: induced error", http.StatusRequestTimeout)
			return
		}
		if volID == "" {
			writeError(w, "Volume ID must be supplied", http.StatusBadRequest)
			return
		}
		decoder := json.NewDecoder(r.Body)
		updateVolumePayload := &types.EditVolumeParam{}
		err := decoder.Decode(updateVolumePayload)
		if err != nil {
			writeError(w, "problem decoding PUT Volume payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Printf("PUT volume payload: %#v\n", updateVolumePayload)
		executionOption := updateVolumePayload.ExecutionOption
		if updateVolumePayload.EditVolumeActionParam.FreeVolumeParam != nil {
			freeVolume(w, updateVolumePayload.EditVolumeActionParam.FreeVolumeParam, volID, executionOption)
			return
		}
		if updateVolumePayload.EditVolumeActionParam.ModifyVolumeIdentifierParam != nil {
			renameVolume(w, updateVolumePayload.EditVolumeActionParam.ModifyVolumeIdentifierParam, volID, executionOption)
			return
		}

	case http.MethodDelete:
		if InducedErrors.DeleteVolumeError {
			writeError(w, "Error deleting Volume: induced error", http.StatusRequestTimeout)
			return
		}
		if InducedErrors.DeviceInSGError {
			writeError(w, "Error deleting Volume: induced error - device is a member of a storage group", http.StatusForbidden)
			return
		}
		deleteVolume(volID)
	}
}

func deleteVolume(volID string) error {
	vol, ok := Data.VolumeIDToVolume[volID]
	if ok {
		if vol.NumberOfStorageGroups > 0 {
			return errors.New("Volume present in storage group. Can't be deleted")
		}
		Data.VolumeIDToVolume[volID] = nil
	} else {
		return errors.New("Volume not found")
	}
	return nil
}

func returnVolume(w http.ResponseWriter, volID string) {
	if volID != "" {
		if vol, ok := Data.VolumeIDToVolume[volID]; ok {
			fmt.Printf("volume: %#v\n", vol)
			writeJSON(w, vol)
			return
		}
		writeError(w, "Volume cannot be found: "+volID, http.StatusNotFound)
	}
}

// This returns a job for freeing space in a volume
func freeVolume(w http.ResponseWriter, param *types.FreeVolumeParam, volID string, executionOption string) {
	if executionOption != types.ExecutionOptionAsynchronous {
		writeError(w, "expected ASYNCHRONOUS", http.StatusBadRequest)
		return
	}
	// Make a job to return
	resourceLink := fmt.Sprintf("sloprovisioning/system/%s/volume/%s", DefaultSymmetrixID, volID)
	if InducedErrors.JobFailedError {
		NewMockJob(volID, types.JobStatusRunning, types.JobStatusFailed, resourceLink)
	} else {
		NewMockJob(volID, types.JobStatusRunning, types.JobStatusSucceeded, resourceLink)
	}
	returnJobByID(w, volID)
}

// This returns the volume itself after renaming
func renameVolume(w http.ResponseWriter, param *types.ModifyVolumeIdentifierParam, volID string, executionOption string) {
	if executionOption != types.ExecutionOptionSynchronous {
		writeError(w, "expected SYNCHRONOUS", http.StatusBadRequest)
		return
	}
	Data.VolumeIDToVolume[volID].VolumeIdentifier = param.VolumeIdentifier.IdentifierName
	returnVolume(w, volID)
}

// JobInfo is used to simulate a job in Unisphere.
// The first call to read it returns Status as the InitialState.
// Subsequent calls return the Status as the FinalState.
type JobInfo struct {
	Job          types.Job
	InitialState string
	FinalState   string
}

// NewMockJob creates a JobInfo that can be queried
func NewMockJob(jobID string, initialState string, finalState string, resourceLink string) *JobInfo {
	job := new(JobInfo)
	job.Job.JobID = jobID
	job.InitialState = initialState
	job.FinalState = finalState
	job.Job.Status = "SCHEDULED"
	job.Job.ResourceLink = resourceLink
	Data.JobIDToMockJob[jobID] = job
	return job
}

func handleJob(w http.ResponseWriter, r *http.Request) {
	if InducedErrors.GetJobError {
		writeError(w, "Error getting Job(s): induced error", http.StatusRequestTimeout)
		return
	}
	vars := mux.Vars(r)
	jobID := vars["jobID"]
	if jobID == "" {
		queryParams := r.URL.Query()
		// Return a job id list
		jobIDList := new(types.JobIDList)
		jobIDList.JobIDs = make([]string, 0)
		for key := range Data.JobIDToMockJob {
			job := Data.JobIDToMockJob[key].Job
			if queryParams.Get("status") == "" || queryParams.Get("status") == job.Status {
				jobIDList.JobIDs = append(jobIDList.JobIDs, key)
			}
		}
		encoder := json.NewEncoder(w)
		encoder.Encode(jobIDList)
		return
	}
	// Return a specific job
	if InducedErrors.GetJobCannotFindRoleForUser {
		InducedErrors.GetJobCannotFindRoleForUser = false
		writeError(w, "Cannot find role for user", http.StatusInternalServerError)
		return
	}
	returnJobByID(w, jobID)
}

func returnJobByID(w http.ResponseWriter, jobID string) {
	job := Data.JobIDToMockJob[jobID]
	if job == nil {
		// Not found
		writeError(w, "Job not found: "+jobID, http.StatusNotFound)
		return
	}
	if job.Job.Status == job.InitialState {
		job.Job.Status = job.FinalState
		job.Job.CompletedDate = time.Now().String()
		job.Job.Result = "Mock job completed"
	} else {
		job.Job.Status = job.InitialState
		job.Job.Result = "Mock job in-progress"
	}
	encoder := json.NewEncoder(w)
	err := encoder.Encode(&job.Job)
	if err != nil {
		writeError(w, "json encoding error", http.StatusInternalServerError)
	}
}

// /unixvmax/restapi/common/Iterator/{iterID]/page}
func handleIterator(w http.ResponseWriter, r *http.Request) {
	var err error
	switch r.Method {
	case http.MethodGet:
		vars := mux.Vars(r)
		queryParams := r.URL.Query()
		from := queryParams.Get("from")
		to := queryParams.Get("to")
		fmt.Printf("mux iterId %s from %s to %s\n", vars["iterId"], from, to)

		result := &types.VolumeResultList{}
		result.From, err = strconv.Atoi(from)
		if err != nil {
			writeError(w, "bad from query parameter", http.StatusBadRequest)
		}
		result.To, err = strconv.Atoi(to)
		if err != nil {
			writeError(w, "bad from query parameter", http.StatusBadRequest)
		}
		for i := result.From - 1; i < result.To-1; i++ {
			volIDList := types.VolumeIDList{VolumeIDs: Data.VolumeIDIteratorList[i]}
			result.VolumeList = append(result.VolumeList, volIDList)
		}
		if Debug {
			fmt.Printf("volumeResultList: %#v\n", result)
		}
		encoder := json.NewEncoder(w)
		err := encoder.Encode(result)
		if err != nil {
			writeError(w, "volumeResultList json encoding error", http.StatusInternalServerError)
		}
	case http.MethodDelete:
		// Nothing to do, will return
	}
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/storagegroup/{id}
// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/storagegroup
func handleStorageGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	sgID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetStorageGroupError {
			writeError(w, "Error retrieving Storage Group(s): induced error", http.StatusRequestTimeout)
			return
		}
		returnStorageGroup(w, sgID)

	case http.MethodPut:
		if InducedErrors.UpdateStorageGroupError {
			writeError(w, "Error updating Storage Group: induced error", http.StatusRequestTimeout)
			return
		}
		if sgID == "" {
			writeError(w, "storage group ID must be supplied", http.StatusBadRequest)
			return
		}
		decoder := json.NewDecoder(r.Body)
		updateSGPayload := &types.UpdateStorageGroupPayload{}
		err := decoder.Decode(updateSGPayload)
		if err != nil {
			writeError(w, "problem decoding PUT StorageGroup payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Printf("PUT StorageGroup payload: %#v\n", updateSGPayload)
		editPayload := updateSGPayload.EditStorageGroupActionParam
		if editPayload.ExpandStorageGroupParam != nil {
			expandPayload := editPayload.ExpandStorageGroupParam
			addVolumeParam := expandPayload.AddVolumeParam
			if addVolumeParam != nil {
				addVolumeToStorageGroupTest(w, addVolumeParam, sgID)
			}
			addSpecificVolumeParam := expandPayload.AddSpecificVolumeParam
			if addSpecificVolumeParam != nil {
				addSpecificVolumeToStorageGroup(w, addSpecificVolumeParam, sgID)
			}
		}
		if editPayload.RemoveVolumeParam != nil {
			removeVolumeFromStorageGroup(w, editPayload.RemoveVolumeParam, sgID)
		}

	case http.MethodPost:
		if InducedErrors.CreateStorageGroupError {
			writeError(w, "Error creating Storage Group: induced error", http.StatusRequestTimeout)
			return
		}
		if InducedErrors.StorageGroupAlreadyExists {
			writeError(w, "The requested storage group resource already exists", http.StatusConflict)
			return
		}
		decoder := json.NewDecoder(r.Body)
		createSGPayload := &types.CreateStorageGroupParam{}
		err := decoder.Decode(createSGPayload)
		if err != nil {
			writeError(w, "problem decoding POST StorageGroup payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Printf("POST StorageGroup payload: %#v\n", createSGPayload)
		sgID := createSGPayload.StorageGroupID
		// Data.StorageGroupIDToNVolumes[sgID] = 0
		// fmt.Println("SG Name: ", sgID)
		AddStorageGroupFromCreateParams(createSGPayload)
		returnStorageGroup(w, sgID)

	case http.MethodDelete:
		if InducedErrors.DeleteStorageGroupError {
			writeError(w, "Error deleting storage group: induced error", http.StatusRequestTimeout)
			return
		}
		RemoveStorageGroup(w, sgID)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/maskingview/{id}/connections
func handleMaskingViewConnections(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		queryParams := r.URL.Query()
		volID := queryParams.Get("volume_id")
		if InducedErrors.GetMaskingViewConnectionsError {
			writeError(w, "Error retrieving Masking View Connections: induced error", http.StatusRequestTimeout)
			return
		}
		replacements := make(map[string]string)
		replacements["__VOLUME_ID__"] = volID
		returnJSONFile(Data.JSONDir, "masking_view_connections_template.json", w, replacements)
	}
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/maskingview/{id}
// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/maskingview
func handleMaskingView(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	mvID := vars["mvID"]
	switch r.Method {
	case http.MethodGet:
		if InducedErrors.GetMaskingViewError {
			writeError(w, "Error retrieving Masking View(s): induced error", http.StatusRequestTimeout)
			return
		}
		returnMaskingView(w, mvID)

	case http.MethodPost:
		if InducedErrors.CreateMaskingViewError {
			writeError(w, "Failed to create masking view: induced error", http.StatusRequestTimeout)
			return
		} else if InducedErrors.MaskingViewAlreadyExists {
			writeError(w, "The requested masking view resource already exists", http.StatusConflict)
			return
		} else if InducedErrors.PortGroupNotFoundError {
			writeError(w, "Port Group on Symmetrix cannot be found", http.StatusInternalServerError)
		} else if InducedErrors.InitiatorGroupNotFoundError {
			writeError(w, "Initiator Group on Symmetrix cannot be found", http.StatusInternalServerError)
		} else if InducedErrors.StorageGroupNotFoundError {
			writeError(w, "Storage Group on Symmetrix cannot be found", http.StatusInternalServerError)
		}
		decoder := json.NewDecoder(r.Body)
		createMVPayload := &types.MaskingViewCreateParam{}
		err := decoder.Decode(createMVPayload)
		if err != nil {
			writeError(w, "problem decoding POST Masking View payload: "+err.Error(), http.StatusBadRequest)
			return
		}
		fmt.Printf("POST MaskingView payload: %#v\n", createMVPayload)
		mvID := createMVPayload.MaskingViewID
		//Data.StorageGroupIDToNVolumes[sgID] = 0
		fmt.Println("MV Name: ", mvID)
		addMaskingViewFromCreateParams(createMVPayload)
		returnMaskingView(w, mvID)

	case http.MethodDelete:
		if InducedErrors.DeleteMaskingViewError {
			writeError(w, "Error deleting Masking view: induced error", http.StatusRequestTimeout)
			return
		}
		RemoveMaskingView(w, mvID)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

func newStorageGroup(storageGroupID string, maskingViewID string, storageResourcePoolID string,
	serviceLevel string, numOfVolumes int) {
	numOfMaskingViews := 0
	if maskingViewID != "" {
		numOfMaskingViews = 1
	}
	childStorageGroups := []string{}
	maskingViews := make([]string, 0)
	if maskingViewID != "" {
		maskingViews = append(maskingViews, maskingViewID)
	}
	storageGroup := &types.StorageGroup{
		StorageGroupID:    storageGroupID,
		SLO:               serviceLevel,
		SRP:               storageResourcePoolID,
		Workload:          "None",
		SLOCompliance:     "STABLE",
		NumOfVolumes:      numOfVolumes,
		NumOfChildSGs:     0,
		NumOfParentSGs:    0,
		NumOfMaskingViews: numOfMaskingViews,
		NumOfSnapshots:    0,
		CapacityGB:        234.5,
		DeviceEmulation:   "FBA",
		Type:              "Standalone",
		Unprotected:       true,
		ChildStorageGroup: childStorageGroups,
		MaskingView:       maskingViews,
	}
	Data.StorageGroupIDToStorageGroup[storageGroupID] = storageGroup
	volumes := make([]string, 0)
	Data.StorageGroupIDToVolumes[storageGroupID] = volumes
}

func newMaskingView(maskingViewID string, storageGroupID string, hostID string, portGroupID string) {
	maskingView := &types.MaskingView{
		MaskingViewID:  maskingViewID,
		HostID:         hostID,
		HostGroupID:    "",
		PortGroupID:    portGroupID,
		StorageGroupID: storageGroupID,
	}
	Data.MaskingViewIDToMaskingView[maskingViewID] = maskingView
}

// AddStorageGroup - Adds a storage group to the mock data cache
func AddStorageGroup(storageGroupID string, storageResourcePoolID string,
	serviceLevel string) (*types.StorageGroup, error) {
	if _, ok := Data.StorageGroupIDToStorageGroup[storageGroupID]; ok {
		return nil, errors.New("The requested storage group resource already exists")
	}
	newStorageGroup(storageGroupID, "", storageResourcePoolID, serviceLevel, 0)
	return Data.StorageGroupIDToStorageGroup[storageGroupID], nil
}

// RemoveStorageGroup - Removes a storage group from the mock data cache
func RemoveStorageGroup(w http.ResponseWriter, storageGroupID string) {
	sg, ok := Data.StorageGroupIDToStorageGroup[storageGroupID]
	if !ok {
		fmt.Println("Storage Group " + storageGroupID + " doesn't exist")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if sg.NumOfMaskingViews != 0 {
		fmt.Println("Can't delete a storage group which is part of masking view")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	volumes := Data.StorageGroupIDToVolumes[storageGroupID]
	if len(volumes) > 0 {
		fmt.Println("Can't delete a storage group which has volumes")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	delete(Data.StorageGroupIDToStorageGroup, storageGroupID)
	delete(Data.StorageGroupIDToStorageGroup, storageGroupID)
}

func addMaskingViewFromCreateParams(createParams *types.MaskingViewCreateParam) {
	mvID := createParams.MaskingViewID
	hostID := ""
	hostGroupID := ""
	if createParams.HostOrHostGroupSelection.UseExistingHostParam != nil {
		hostID = createParams.HostOrHostGroupSelection.UseExistingHostParam.HostID
	} else if createParams.HostOrHostGroupSelection.UseExistingHostGroupParam != nil {
		hostGroupID = createParams.HostOrHostGroupSelection.UseExistingHostGroupParam.HostGroupID
	}
	portGroupID := createParams.PortGroupSelection.UseExistingPortGroupParam.PortGroupID
	sgID := createParams.StorageGroupSelection.UseExistingStorageGroupParam.StorageGroupID
	if hostID != "" {
		AddMaskingView(mvID, sgID, hostID, portGroupID)
	} else if hostGroupID != "" {
		AddMaskingView(mvID, sgID, hostGroupID, portGroupID)
	}
}

// AddMaskingView - Adds a masking view to the mock data cache
func AddMaskingView(maskingViewID string, storageGroupID string, hostID string, portGroupID string) (*types.MaskingView, error) {
	if _, ok := Data.MaskingViewIDToMaskingView[maskingViewID]; ok {
		return nil, errors.New("Error! Masking View already exists")
	}
	if _, ok := Data.StorageGroupIDToStorageGroup[storageGroupID]; !ok {
		return nil, errors.New("Storage Group doesn't exist")
	}
	/*if _, ok := Data.PortGroupIDToPortGroup[portGroupID]; !ok {
		return errors.New("Port Group doesn't exist")
	}*/
	if _, ok := Data.HostIDToHost[hostID]; !ok {
		return nil, errors.New("Host doesn't exist")
	}
	newMaskingView(maskingViewID, storageGroupID, hostID, portGroupID)
	// Update host
	Data.HostIDToHost[hostID].MaskingviewIDs = append(Data.HostIDToHost[hostID].MaskingviewIDs, maskingViewID)
	Data.HostIDToHost[hostID].NumberMaskingViews++
	// Update Storage Group
	currentMaskingViewIDs := Data.StorageGroupIDToStorageGroup[storageGroupID].MaskingView
	Data.StorageGroupIDToStorageGroup[storageGroupID].MaskingView = append(
		currentMaskingViewIDs, maskingViewID)
	Data.StorageGroupIDToStorageGroup[storageGroupID].NumOfMaskingViews++
	// Update the volume cache
	for _, volumeID := range Data.StorageGroupIDToVolumes[storageGroupID] {
		Data.VolumeIDToVolume[volumeID].NumberOfFrontEndPaths = 1
	}
	return Data.MaskingViewIDToMaskingView[maskingViewID], nil
}

// RemoveMaskingView - Removes a masking view from the mock data cache
func RemoveMaskingView(w http.ResponseWriter, maskingViewID string) {
	mv, ok := Data.MaskingViewIDToMaskingView[maskingViewID]
	if !ok {
		fmt.Println("Masking View " + maskingViewID + " doesn't exist")
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// Handle storage groups
	storageGroupID := mv.StorageGroupID
	Data.StorageGroupIDToStorageGroup[storageGroupID].NumOfMaskingViews--
	currentMaskingViewIDs := Data.StorageGroupIDToStorageGroup[storageGroupID].MaskingView
	newMaskingViewIDs := make([]string, 0)
	for _, mvID := range currentMaskingViewIDs {
		if mvID != maskingViewID {
			newMaskingViewIDs = append(newMaskingViewIDs, mvID)
		}
	}
	Data.StorageGroupIDToStorageGroup[storageGroupID].MaskingView = newMaskingViewIDs
	// Handle Hosts
	hostID := mv.HostID
	Data.HostIDToHost[hostID].NumberMaskingViews--
	currentMaskingViewIDs = Data.HostIDToHost[hostID].MaskingviewIDs
	newMaskingViewIDs = make([]string, 0)
	for _, mvID := range currentMaskingViewIDs {
		if mvID != maskingViewID {
			newMaskingViewIDs = append(newMaskingViewIDs, mvID)
		}
	}
	Data.HostIDToHost[hostID].MaskingviewIDs = newMaskingViewIDs
	// Check if we need to update the number of front end paths for volumes
	// Loop through volumes of this particular SG
	if volumeIDs, ok := Data.StorageGroupIDToVolumes[storageGroupID]; ok {
		// First construct a list of all SGs
		tempSGList := make([]string, 0)
		for _, volumeID := range volumeIDs {
			if vol, ok1 := Data.VolumeIDToVolume[volumeID]; ok1 {
				tempSGList = append(tempSGList, vol.StorageGroupIDList...)
			}
		}
		// Remove duplicates
		tempSGList = uniqueElements(tempSGList)
		// Filter out SGs in masking Views
		sgIDsInMaskingView := make([]string, 0)
		for _, sgID := range tempSGList {
			if sg, ok1 := Data.StorageGroupIDToStorageGroup[sgID]; ok1 {
				if sg.NumOfMaskingViews > 0 {
					sgIDsInMaskingView = append(sgIDsInMaskingView, sgID)
				}
			}
		}
		// Now Update the number of front end paths
		for _, volumeID := range volumeIDs {
			if vol, ok1 := Data.VolumeIDToVolume[volumeID]; ok1 {
				update := compareAndCheck(vol.StorageGroupIDList, sgIDsInMaskingView)
				if update {
					vol.NumberOfFrontEndPaths = 0
				}
			}
		}
	}
	delete(Data.StorageGroupIDToStorageGroup, maskingViewID)
}

// compareAndCheck - compares two string slices and returns true if the slices are equal or false if they aren't
func compareAndCheck(slice1 []string, slice2 []string) bool {
	for _, item := range slice1 {
		for _, item1 := range slice2 {
			if item == item1 {
				return false
			}
		}
	}
	return true
}

//uniqueElements - Removes duplicates from a string slice and returns a slice containing unique elements only
func uniqueElements(slice []string) []string {
	keys := make(map[string]bool)
	list := []string{}
	for _, entry := range slice {
		if _, value := keys[entry]; !value {
			keys[entry] = true
			list = append(list, entry)
		}
	}
	return list
}

// newVolume creates a new mock volume with the specified characteristics.
func newVolume(volumeID, volumeIdentifier string, size int, sgList []string) {
	volume := &types.Volume{
		VolumeID:              volumeID,
		Type:                  "TDEV",
		Emulation:             "FBA",
		SSID:                  "FFFFFFFF",
		AllocatedPercent:      0,
		CapacityGB:            0.0,
		FloatCapacityMB:       0.0,
		CapacityCYL:           size,
		Status:                "Ready",
		Reserved:              false,
		Pinned:                false,
		VolumeIdentifier:      volumeIdentifier,
		WWN:                   "60000970000197900046533030324538",
		Encapsulated:          false,
		NumberOfStorageGroups: 1,
		NumberOfFrontEndPaths: 0,
		StorageGroupIDList:    sgList,
		Success:               true,
		Message:               "message",
	}
	Data.VolumeIDToVolume[volumeID] = volume
}

// AddNewVolume - Add a volume to the mock data cache
func AddNewVolume(volumeID, volumeIdentifier string, size int, storageGroupID string) error {
	if _, ok := Data.VolumeIDToVolume[volumeID]; ok {
		return errors.New("The requested volume already exists")
	}
	if _, ok := Data.StorageGroupIDToStorageGroup[storageGroupID]; !ok {
		return errors.New("The requested storage group resource doesn't exist")
	}
	sgList := []string{storageGroupID}
	newVolume(volumeID, volumeIdentifier, size, sgList)
	Data.StorageGroupIDToStorageGroup[storageGroupID].NumOfVolumes++
	currentVolumes := Data.StorageGroupIDToVolumes[storageGroupID]
	newVolumes := append(currentVolumes, volumeID)
	Data.StorageGroupIDToVolumes[storageGroupID] = newVolumes
	return nil
}

func newInitiator(initiatorID string, initiatorName string, initiatorType string, dirPortKeys []types.PortKey, hostID string) {
	//maskingViewIDs := []string{}
	initiator := &types.Initiator{
		InitiatorID:          initiatorName,
		SymmetrixPortKey:     dirPortKeys,
		InitiatorType:        initiatorType,
		FCID:                 "0",
		IPAddress:            "192.168.1.175",
		HostID:               hostID,
		HostGroupIDs:         []string{},
		LoggedIn:             true,
		OnFabric:             true,
		FlagsInEffect:        "Common_Serial_Number(C), SCSI_3(SC3), SPC2_Protocol_Version(SPC2)",
		NumberVols:           1,
		NumberHostGroups:     0,
		NumberMaskingViews:   0,
		NumberPowerPathHosts: 0,
	}
	Data.InitiatorIDToInitiator[initiatorID] = initiator
}

// AddInitiator - Adds an initiator to the mock data cache
func AddInitiator(initiatorID string, initiatorName string, initiatorType string, dirPortKeys []string, hostID string) (*types.Initiator, error) {
	if _, ok := Data.InitiatorIDToInitiator[initiatorID]; ok {
		return nil, errors.New("Error! Initiator already exists")
	}
	// if host id is supplied, check for existence of host
	if hostID != "" {
		if _, ok := Data.HostIDToHost[hostID]; !ok {
			return nil, errors.New("Error! Host doesn't exist")
		}
	}
	portKeys := make([]types.PortKey, 0)
	for _, dirPortKey := range dirPortKeys {
		dirPortDetails := strings.Split(dirPortKey, ":")
		portKey := types.PortKey{
			DirectorID: dirPortDetails[0],
			PortID:     dirPortKey,
		}
		portKeys = append(portKeys, portKey)
	}
	newInitiator(initiatorID, initiatorName, initiatorType, portKeys, hostID)
	return Data.InitiatorIDToInitiator[initiatorID], nil
}

func returnInitiator(w http.ResponseWriter, initiatorID string) {
	if initiatorID != "" {
		if init, ok := Data.InitiatorIDToInitiator[initiatorID]; ok {
			writeJSON(w, init)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	} else {
		initIDs := make([]string, 0)
		for k := range Data.InitiatorIDToInitiator {
			initIDs = append(initIDs, k)
		}
		initiatorIDList := &types.InitiatorList{
			InitiatorIDs: initIDs,
		}
		writeJSON(w, initiatorIDList)
	}
}

func newHost(hostID string, hostType string, initiatorIDs []string) {
	maskingViewIDs := []string{}
	host := &types.Host{
		HostID:             hostID,
		NumberMaskingViews: 0,
		NumberInitiators:   int64(len(initiatorIDs)),
		NumberHostGroups:   0,
		PortFlagsOverride:  false,
		ConsistentLun:      false,
		EnabledFlags:       "",
		DisabledFlags:      "",
		HostType:           hostType,
		Initiators:         initiatorIDs,
		MaskingviewIDs:     maskingViewIDs,
		NumPowerPathHosts:  0,
	}
	Data.HostIDToHost[hostID] = host
}

// AddHost - Adds a host to the mock data cache
func AddHost(hostID string, hostType string, initiatorIDs []string) (*types.Host, error) {
	if _, ok := Data.HostIDToHost[hostID]; ok {
		return nil, errors.New("Error! Host already exists")
	}
	validInitiators := false
	// Check if initiators exist
	for _, initID := range initiatorIDs {
		for _, v := range Data.InitiatorIDToInitiator {
			if v.InitiatorID == initID {
				if v.HostID == "" {
					validInitiators = true
					break
				}
			}
		}
		if !validInitiators {
			break
		}
	}
	if !validInitiators {
		errormsg := "Error! Some initiators don't exist or are not valid"
		fmt.Println(errormsg)
		return nil, fmt.Errorf(errormsg)
	}
	newHost(hostID, hostType, initiatorIDs)
	//Update the initiators
	for _, initID := range initiatorIDs {
		for k, v := range Data.InitiatorIDToInitiator {
			if v.InitiatorID == initID {
				Data.InitiatorIDToInitiator[k].HostID = hostID
				break
			}
		}
	}
	fmt.Println(Data.HostIDToHost[hostID])
	return Data.HostIDToHost[hostID], nil
}

// removeHost - Remove a host from the mock data cache
func removeHost(hostID string) error {
	host, ok := Data.HostIDToHost[hostID]
	if !ok {
		return errors.New("Error! Host doesn't exist")
	}
	if host.NumberMaskingViews > 0 {
		return errors.New("Error! Host is part of a masking view")
	}
	Data.HostIDToHost[hostID] = nil
	return nil
}

func newPortGroup(portGroupID string, portGroupType string, portKeys []types.PortKey) {
	portGroup := &types.PortGroup{
		PortGroupID:        portGroupID,
		SymmetrixPortKey:   portKeys,
		NumberPorts:        int64(len(portKeys)),
		NumberMaskingViews: 0,
		PortGroupType:      portGroupType,
	}
	Data.PortGroupIDToPortGroup[portGroupID] = portGroup
}

// addPortGroup - Adds a port group to the mock data cache
func addPortGroup(portGroupID string, portGroupType string, portKeys []types.PortKey) (*types.PortGroup, error) {
	if _, ok := Data.PortGroupIDToPortGroup[portGroupID]; ok {
		return nil, errors.New("Error! Port Group already exists")
	}
	newPortGroup(portGroupID, portGroupType, portKeys)
	return Data.PortGroupIDToPortGroup[portGroupID], nil
}

// AddPortGroupFromCreateParams - Adds a storage group from create params
func AddPortGroupFromCreateParams(createParams *types.CreatePortGroupParams) {
	portGroupID := createParams.PortGroupID
	portKeys := createParams.SymmetrixPortKey
	addPortGroup(portGroupID, "Fibre", portKeys)
}

// AddPortGroup - Adds a port group to the mock data cache
func AddPortGroup(portGroupID string, portGroupType string, portIdentifiers []string) (*types.PortGroup, error) {
	portKeys := make([]types.PortKey, 0)
	for _, dirPortKey := range portIdentifiers {
		dirPortDetails := strings.Split(dirPortKey, ":")
		if len(dirPortDetails) != 2 {
			errormsg := fmt.Sprintf("Invalid dir port specified: %s", dirPortKey)
			log.Error(errormsg)
			return nil, fmt.Errorf(errormsg)
		}
		portKey := types.PortKey{
			DirectorID: dirPortDetails[0],
			PortID:     dirPortKey,
		}
		portKeys = append(portKeys, portKey)
	}
	if _, ok := Data.PortGroupIDToPortGroup[portGroupID]; ok {
		return nil, errors.New("Error! Port Group already exists")
	}
	newPortGroup(portGroupID, portGroupType, portKeys)
	return Data.PortGroupIDToPortGroup[portGroupID], nil
}

// AddStorageGroupFromCreateParams - Adds a storage group from create params
func AddStorageGroupFromCreateParams(createParams *types.CreateStorageGroupParam) {
	sgID := createParams.StorageGroupID
	srpID := createParams.SRPID
	serviceLevel := "None"
	if srpID != "None" {
		sloBasedParams := createParams.SLOBasedStorageGroupParam
		serviceLevel = sloBasedParams[0].SLOID
	} else {
		srpID = ""
	}
	AddStorageGroup(sgID, srpID, serviceLevel)
}

// keys - Return keys of the given map
func keys(m map[string]*types.StorageGroup) (keys []string) {
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func returnStorageGroup(w http.ResponseWriter, sgID string) {
	if sgID != "" {
		if sg, ok := Data.StorageGroupIDToStorageGroup[sgID]; ok {
			fmt.Printf("SG: %#v\n", sg)
			writeJSON(w, sg)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	} else {
		storageGroupIDs := keys(Data.StorageGroupIDToStorageGroup)
		storageGroupIDList := &types.StorageGroupIDList{
			StorageGroupIDs: storageGroupIDs,
		}
		writeJSON(w, storageGroupIDList)
	}
}

func returnMaskingView(w http.ResponseWriter, mvID string) {
	if mvID != "" {
		if mv, ok := Data.MaskingViewIDToMaskingView[mvID]; ok {
			writeJSON(w, mv)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	} else {
		maskingViewIDs := make([]string, 0)
		for k := range Data.MaskingViewIDToMaskingView {
			maskingViewIDs = append(maskingViewIDs, k)
		}
		maskingViewIDList := &types.MaskingViewList{
			MaskingViewIDs: maskingViewIDs,
		}
		writeJSON(w, maskingViewIDList)
	}
}

func writeJSON(w http.ResponseWriter, val interface{}) {
	if InducedErrors.InvalidResponse {
		fmt.Println("Inducing error")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		fmt.Println("error:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Printf("Couldn't write to ResponseWriter")
		w.WriteHeader(http.StatusInternalServerError)
	}
	return
}

// AddOneVolumeToStorageGroup - Adds volume to a storage group in the mock cache
func AddOneVolumeToStorageGroup(volumeID, volumeIdentifier, sgID string, size int) error {
	if _, ok := Data.StorageGroupIDToStorageGroup[sgID]; !ok {
		return errors.New("The requested storage group doesn't exist")
	}
	if _, ok := Data.VolumeIDToVolume[volumeID]; ok {
		// Found the volume in cache
		// We are adding it to another storage group
		if volumes, ok := Data.StorageGroupIDToVolumes[sgID]; ok {
			found := false
			for _, volume := range volumes {
				if strings.Contains(volume, volumeID) {
					found = true
					break
				}
			}
			if found {
				return errors.New("Volume is already a part of the SG")
			}
			// Update the volume cache
			currentStorageGroups := Data.VolumeIDToVolume[volumeID].StorageGroupIDList
			newStorageGroups := append(currentStorageGroups, sgID)
			Data.VolumeIDToVolume[volumeID].StorageGroupIDList = newStorageGroups

			// Update the Storage Group caches
			Data.StorageGroupIDToStorageGroup[sgID].NumOfVolumes++
			currentVolumes := Data.StorageGroupIDToVolumes[sgID]
			newVolumes := append(currentVolumes, volumeID)
			Data.StorageGroupIDToVolumes[sgID] = newVolumes

			// Check if the volume was added to a masking view
			if Data.StorageGroupIDToStorageGroup[sgID].NumOfMaskingViews > 0 {
				Data.VolumeIDToVolume[volumeID].NumberOfFrontEndPaths = 1
			}
			Data.VolumeIDToVolume[volumeID].NumberOfStorageGroups++
		}
	} else {
		// We are adding a new volume
		AddNewVolume(volumeID, volumeIdentifier, size, sgID)
	}
	return nil
}

func addVolumeToStorageGroupTest(w http.ResponseWriter, addVolumeParam *types.AddVolumeParam, sgID string) {
	name := addVolumeParam.VolumeIdentifier.IdentifierName
	size := addVolumeParam.VolumeAttribute.VolumeSize
	if name == "" || size == "" {
		writeError(w, "null name or size", http.StatusBadRequest)
	}
	id := strconv.Itoa(time.Now().Nanosecond())
	sizeInt, err := strconv.Atoi(size)
	if err != nil {
		writeError(w, "unable to convert size string to integer", http.StatusBadRequest)
	}
	if InducedErrors.VolumeNotCreatedError == false {
		AddOneVolumeToStorageGroup(id, name, sgID, sizeInt)
	}
	// Make a job to return
	resourceLink := fmt.Sprintf("sloprovisioning/system/%s/storagegroup/%s", DefaultSymmetrixID, sgID)
	if InducedErrors.JobFailedError {
		NewMockJob(id, types.JobStatusRunning, types.JobStatusFailed, resourceLink)
	} else {
		NewMockJob(id, types.JobStatusRunning, types.JobStatusSucceeded, resourceLink)
	}
	returnJobByID(w, id)
}

func addSpecificVolumeToStorageGroup(w http.ResponseWriter, addSpecificVolumeParam *types.AddSpecificVolumeParam, sgID string) {
	volumeIDs := addSpecificVolumeParam.VolumeIDs
	if len(volumeIDs) == 0 {
		writeError(w, "empty list", http.StatusBadRequest)
	}
	jobID := strconv.Itoa(time.Now().Nanosecond())
	if InducedErrors.VolumeNotAddedError {
		writeError(w, "Error adding volume to the SG", http.StatusRequestTimeout)
		return
	}
	for _, volumeID := range volumeIDs {
		AddOneVolumeToStorageGroup(volumeID, "TestVol", sgID, 0)
	}
	// Make a job to return
	resourceLink := fmt.Sprintf("sloprovisioning/system/%s/storagegroup/%s", DefaultSymmetrixID, sgID)
	if InducedErrors.JobFailedError {
		NewMockJob(jobID, types.JobStatusRunning, types.JobStatusFailed, resourceLink)
	} else {
		NewMockJob(jobID, types.JobStatusRunning, types.JobStatusSucceeded, resourceLink)
	}
	returnJobByID(w, jobID)
}

func removeOneVolumeFromStorageGroup(volumeID, storageGroupID string) error {
	if _, ok := Data.StorageGroupIDToStorageGroup[storageGroupID]; !ok {
		return errors.New("The requested storage group doesn't exist")
	}
	if _, ok := Data.StorageGroupIDToVolumes[storageGroupID]; !ok {
		return errors.New("Storage Group to volume mapping doesn't exist")
	}
	vol, ok := Data.VolumeIDToVolume[volumeID]
	if !ok {
		return errors.New("The requested volume doesn't exist")
	}
	// Remove SG from the volume's SG list
	currentSGList := vol.StorageGroupIDList
	newStorageGroupList := make([]string, 0)
	for _, sgID := range currentSGList {
		if sgID != storageGroupID {
			newStorageGroupList = append(newStorageGroupList, sgID)
		}
	}
	vol.StorageGroupIDList = newStorageGroupList
	vol.NumberOfStorageGroups--
	// Remove volume from the SG's volume list
	currentVolumeIDs := Data.StorageGroupIDToVolumes[storageGroupID]
	newVolumeIDList := make([]string, 0)
	for _, volID := range currentVolumeIDs {
		if volID != volumeID {
			newVolumeIDList = append(newVolumeIDList, volID)
		}
	}
	Data.StorageGroupIDToVolumes[storageGroupID] = newVolumeIDList
	// Update the count of volumes in SG
	Data.StorageGroupIDToStorageGroup[storageGroupID].NumOfVolumes--
	// Check if we need to update the number of front end paths for this particular volume
	update := true
	for _, sgID := range vol.StorageGroupIDList {
		if sg, ok := Data.StorageGroupIDToStorageGroup[sgID]; ok {
			if sg.NumOfMaskingViews > 1 {
				update = false
				break
			}
		}
	}
	if update {
		vol.NumberOfFrontEndPaths = 0
	}
	return nil
}

func removeVolumeFromStorageGroup(w http.ResponseWriter, removeVolumeParam *types.RemoveVolumeParam, sgID string) {
	for _, volID := range removeVolumeParam.VolumeIDs {
		fmt.Println("Volume ID: " + volID)
		removeOneVolumeFromStorageGroup(volID, sgID)
	}
	returnStorageGroup(w, sgID)
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/portgroup/{id}
// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/portgroup
func handlePortGroup(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	pgID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetPortGroupError {
			writeError(w, "Error retrieving Port Group(s): induced error", http.StatusRequestTimeout)
			return
		}
		returnPortGroup(w, pgID)

	case http.MethodPost:
		decoder := json.NewDecoder(r.Body)
		createPortGroupParams := &types.CreatePortGroupParams{}
		err := decoder.Decode(createPortGroupParams)
		if err != nil {
			writeError(w, "InvalidJson", http.StatusBadRequest)
			return
		}
		AddPortGroupFromCreateParams(createPortGroupParams)
		returnPortGroup(w, createPortGroupParams.PortGroupID)
	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

// /univmax/restapi/90/system/symmetrix/{symid}/director/{director}/port/{id}
// /univmax/restapi/90/system/symmetrix/{symid}/director/{director}/port
func handlePort(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	dID := vars["director"]
	pID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetPortError {
			writeError(w, "Error retrieving Port(s): induced error", http.StatusRequestTimeout)
			return
		}
		// if we asked for a specific Port, return those details
		if pID != "" {
			// Specific ports can be modeleted
			portName := dID + ":" + pID
			if Data.PortIDToSymmetrixPortType[portName] != nil {
				port := Data.PortIDToSymmetrixPortType[portName]
				if port == nil || port.Type == "" {
					writeError(w, "port not found", http.StatusNotFound)
				} else {
					symPort := &types.Port{
						SymmetrixPort: *port,
					}
					encoder := json.NewEncoder(w)
					encoder.Encode(symPort)
				}
				return
			}
			returnPort(w, dID, pID)
		}
		// return a list of Ports
		returnPortIDList(w, dID)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

// AddPort adds a port entry. Port type can either be "FibreChannel" or "GigE", or "" for a non existent port.
func AddPort(id, identifier, portType string) {
	port := &types.SymmetrixPortType{
		Type:       portType,
		Identifier: identifier,
	}
	Data.PortIDToSymmetrixPortType[id] = port
}

func returnPort(w http.ResponseWriter, dID, pID string) {
	replacements := make(map[string]string)
	replacements["__PORT_ID__"] = pID
	replacements["__DIRECTOR_ID__"] = dID
	returnJSONFile(Data.JSONDir, "port_template.json", w, replacements)
}

func returnPortIDList(w http.ResponseWriter, dID string) {
	replacements := make(map[string]string)
	replacements["__DIRECTOR_ID__"] = dID
	returnJSONFile(Data.JSONDir, "portIDList.json", w, replacements)
}

// /univmax/restapi/90/system/symmetrix/{symid}/director/{{id}
// /univmax/restapi/90/system/symmetrix/{symid}/director
func handleDirector(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	dID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetDirectorError {
			writeError(w, "Error retrieving Director(s): induced error", http.StatusRequestTimeout)
			return
		}
		// if we asked for a specific Director, return those details
		if dID != "" {
			returnDirector(w, dID)
		}
		// return a list of Directors
		returnDirectorIDList(w)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

func returnDirector(w http.ResponseWriter, dID string) {
	replacements := make(map[string]string)
	replacements["__DIRECTOR_ID__"] = dID
	returnJSONFile(Data.JSONDir, "director_template.json", w, replacements)
}

func returnDirectorIDList(w http.ResponseWriter) {
	replacements := make(map[string]string)
	returnJSONFile(Data.JSONDir, "directorIDList.json", w, replacements)
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/initiator/{id}
// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/initiator
func handleInitiator(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	initID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetInitiatorError {
			writeError(w, "Error retrieving Initiator(s): induced error", http.StatusRequestTimeout)
			return
		}
		if initID != "" {
			if InducedErrors.GetInitiatorByIDError {
				writeError(w, "Error retrieving Initiator By ID: induced error", http.StatusRequestTimeout)
				return
			}
		}
		returnInitiator(w, initID)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/host/{id}
// /univmax/restapi/90/sloprovisioning/symmetrix/{symid}/host
func handleHost(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	hostID := vars["id"]
	switch r.Method {

	case http.MethodGet:
		if InducedErrors.GetHostError {
			writeError(w, "Error retrieving Host(s): induced error", http.StatusRequestTimeout)
			return
		}
		returnHost(w, hostID)

	case http.MethodPost:
		if InducedErrors.CreateHostError {
			writeError(w, "Error creating Host: induced error", http.StatusRequestTimeout)
			return
		}
		decoder := json.NewDecoder(r.Body)
		createHostParam := &types.CreateHostParam{}
		err := decoder.Decode(createHostParam)
		if err != nil {
			writeError(w, "InvalidJson", http.StatusBadRequest)
			return
		}
		// Scan the initiators to see if there are any non iqn ones; then assume
		// host type Fibre.
		isFibre := false
		for _, initiator := range createHostParam.InitiatorIDs {
			if !strings.HasPrefix(initiator, "iqn.") {
				isFibre = true
			}
		}
		if isFibre {
			// Might need to add the Port information here
			AddHost(createHostParam.HostID, "Fibre", createHostParam.InitiatorIDs)
		} else {
			//initNode := make([]string, 0)
			//initNode = append(initNode, "iqn.1993-08.org.centos:01:5ae577b352a7")
			AddHost(createHostParam.HostID, "iSCSI", createHostParam.InitiatorIDs)
		}
		returnHost(w, createHostParam.HostID)

	case http.MethodPut:
		if hasError(&InducedErrors.UpdateHostError) {
			// if InducedErrors.UpdateHostError {
			writeError(w, "Error updating Host: induced error", http.StatusRequestTimeout)
			return
		}
		decoder := json.NewDecoder(r.Body)
		updateHostParam := &types.UpdateHostParam{}
		err := decoder.Decode(updateHostParam)
		if err != nil {
			writeError(w, "InvalidJson", http.StatusBadRequest)
			return
		}
		returnHost(w, hostID)

	case http.MethodDelete:
		if InducedErrors.DeleteHostError {
			writeError(w, "Error deleting Host: induced error", http.StatusRequestTimeout)
			return
		}
		removeHost(hostID)

	default:
		writeError(w, "Invalid Method", http.StatusBadRequest)
	}
}

func returnHost(w http.ResponseWriter, hostID string) {
	if hostID != "" {
		if host, ok := Data.HostIDToHost[hostID]; ok {
			writeJSON(w, host)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	} else {
		hostIDs := make([]string, 0)
		for k := range Data.HostIDToHost {
			hostIDs = append(hostIDs, k)
		}
		hostIDList := &types.HostList{
			HostIDs: hostIDs,
		}
		writeJSON(w, hostIDList)
	}
}

func returnPortGroup(w http.ResponseWriter, portGroupID string) {
	if portGroupID != "" {
		if pg, ok := Data.PortGroupIDToPortGroup[portGroupID]; ok {
			fmt.Printf("\n%v\n", pg)
			writeJSON(w, pg)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	} else {
		portGroupIDs := make([]string, 0)
		for k := range Data.PortGroupIDToPortGroup {
			portGroupIDs = append(portGroupIDs, k)
		}
		portGroupList := &types.PortGroupList{
			PortGroupIDs: portGroupIDs,
		}
		writeJSON(w, portGroupList)
	}
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, "URL not found: "+r.URL.String(), http.StatusNotFound)
}

// Write an error code to the response writer
func writeError(w http.ResponseWriter, message string, httpStatus int) {
	w.WriteHeader(httpStatus)
	resp := new(types.Error)
	resp.Message = message
	// The following aren't used by the hardware but could be used internally
	//resp.HTTPStatusCode = http.StatusNotFound
	//resp.ErrorCode = int(errorCode)
	encoder := json.NewEncoder(w)
	err := encoder.Encode(resp)
	if err != nil {
		log.Printf("error encoding json: %s\n", err.Error())
	}
}

// Return content from a JSON file. Arguments are:
//   directory, filename  of the file
//  wrriter ResponseWriter where data is output
// An optional replacement map. If supplied every instance of a key in the JSON file will be replaced with the corresponding value.
func returnJSONFile(directory, filename string, w http.ResponseWriter, replacements map[string]string) (jsonBytes []byte) {
	jsonBytes, err := ioutil.ReadFile(filepath.Join(directory, filename))
	if err != nil {
		log.Printf("Couldn't read %s/%s\n", directory, filename)
		if w != nil {
			w.WriteHeader(http.StatusNotFound)
		}
		return make([]byte, 0)
	}
	if replacements != nil {
		jsonString := string(jsonBytes)
		for key, value := range replacements {
			jsonString = strings.Replace(jsonString, key, value, -1)
		}
		if Debug {
			log.Printf("Edited payload:\n%s\n", jsonString)
		}
		jsonBytes = []byte(jsonString)
	}
	if Debug {
		log.Printf("jsonBytes:\n%s\n", jsonBytes)
	}
	if w != nil {
		_, err = w.Write(jsonBytes)
		if err != nil {
			log.Printf("Couldn't write to ResponseWriter")
			w.WriteHeader(http.StatusInternalServerError)
			return make([]byte, 0)
		}
	}
	return jsonBytes
}
