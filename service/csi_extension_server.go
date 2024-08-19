/*
 Copyright © 2023-2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dell/dell-csi-extensions/podmon"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OneHour is time used for metrics
const OneHour int64 = 3600000

var metricsQuery = []string{"HostMBs", "MBRead", "MBWritten", "IoRate", "Reads", "Writes", "ResponseTime"}

func (s *service) ValidateVolumeHostConnectivity(ctx context.Context, req *podmon.ValidateVolumeHostConnectivityRequest) (*podmon.ValidateVolumeHostConnectivityResponse, error) {
	log.Infof("ValidateVolumeHostConnectivity called %+v", req)
	rep := &podmon.ValidateVolumeHostConnectivityResponse{
		Messages: make([]string, 0),
	}

	if (len(req.GetVolumeIds()) == 0 || len(req.GetArrayId()) == 0) && len(req.GetNodeId()) == 0 {
		// This is a nop call just testing the interface is present
		rep.Messages = append(rep.Messages, "ValidateVolumeHostConnectivity is implemented")
		return rep, nil
	}

	if req.GetNodeId() == "" {
		return nil, fmt.Errorf("the NodeID is a required field")
	}
	// create the map of all the array with array's symID as key
	symIDs := make(map[string]bool)
	symID := req.GetArrayId()
	if symID == "" {
		if len(req.GetVolumeIds()) == 0 {
			log.Info("neither symID nor volumeID is present in request")
			// Create from the default array
			for _, arr := range s.opts.ManagedArrays {
				symIDs[arr] = true
			}
		}
		// for loop req.GetVolumeIds()
		for _, volID := range req.GetVolumeIds() {
			_, symID, _, _, _, err := s.parseCsiID(volID)
			if err != nil || symID == "" {
				log.Errorf("unable to retrieve array's symID after parsing volumeID")
				for _, arr := range s.opts.ManagedArrays {
					symIDs[arr] = true
				}
			} else {
				symIDs[symID] = true
			}
		}
	} else {
		symIDs[symID] = true
	}

	// Go through each of the symIDs
	for symID := range symIDs {
		// First - check if the array is visible from the node
		err := s.checkIfNodeIsConnected(ctx, symID, req.GetNodeId(), rep)
		if err != nil {
			return rep, err
		}

		// Check for IOinProgress only when volumes IDs are present in the request as the field is required only in the latter case also to reduce number of calls to the API making it efficient
		if len(req.GetVolumeIds()) > 0 {
			// Get array config
			for _, volID := range req.GetVolumeIds() {
				_, symIDForVol, devID, _, _, _ := s.parseCsiID(volID)
				if symIDForVol != symID {
					log.Errorf("Recived symID from podman is %s and retrieved from array is %s ", symID, symIDForVol)
					return nil, fmt.Errorf("invalid symID %s is provided", symID)
				}
				// check if any IO is inProgress for the current symID/array
				err := s.IsIOInProgress(ctx, devID, symIDForVol)
				if err == nil {
					rep.IosInProgress = true
					return rep, nil
				}
			}
		}
	}
	log.Infof("ValidateVolumeHostConnectivity reply %+v", rep)
	return rep, nil
}

// checkIfNodeIsConnected looks at the 'nodeId' to determine if there is connectivity to the 'arrayId' array.
// The 'rep' object will be filled with the results of the check.
func (s *service) checkIfNodeIsConnected(ctx context.Context, symID string, nodeID string, rep *podmon.ValidateVolumeHostConnectivityResponse) error {
	log.Infof("Checking if array %s is connected to node %s", symID, nodeID)
	var message string
	rep.Connected = false
	nodeIP := s.k8sUtils.GetNodeIPs(nodeID)
	if len(nodeIP) == 0 {
		log.Errorf("failed to parse node ID '%s'", nodeID)
		return fmt.Errorf("failed to parse node ID")
	}
	// form url to call array on node
	url := "http://" + nodeIP + s.opts.PodmonPort + ArrayStatus + "/" + symID
	connected, err := s.QueryArrayStatus(ctx, url)
	if err != nil {
		message = fmt.Sprintf("connectivity unknown for array %s to node %s due to %s", symID, nodeID, err)
		log.Error(message)
		rep.Messages = append(rep.Messages, message)
		log.Errorf("%s", err.Error())
	}

	if connected {
		rep.Connected = true
		message = fmt.Sprintf("array %s is connected to node %s", symID, nodeID)
	} else {
		message = fmt.Sprintf("array %s is not connected to node %s", symID, nodeID)
	}
	log.Info(message)
	rep.Messages = append(rep.Messages, message)
	return nil
}

// IsIOInProgress function check the IO operation status on array
func (s *service) IsIOInProgress(ctx context.Context, volID, symID string) (err error) {
	// Call PerformanceMetricsByVolume or PerformanceMetricsByFileSystem in gopowermax based on the volume type
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		log.Error(err.Error())
		return status.Error(codes.InvalidArgument, err.Error())
	}
	arrayKeys, err := pmaxClient.GetArrayPerfKeys(ctx)
	if err != nil {
		log.Error(err.Error())
		return status.Errorf(codes.Internal, "error %s getting keys", err.Error())
	}
	var endTime int64
	for _, info := range arrayKeys.ArrayInfos {
		if strings.Compare(info.SymmetrixID, symID) == 0 {
			endTime = info.LastAvailableDate
			break
		}
	}
	startTime := endTime - OneHour
	resp, err := pmaxClient.GetVolumesMetricsByID(ctx, symID, volID, metricsQuery, startTime, endTime)
	if err != nil {
		// nfs volume type logic volId may be fsID
		resp, err := pmaxClient.GetFileSystemMetricsByID(ctx, symID, volID, metricsQuery, startTime, endTime)
		if err != nil {
			log.Errorf("Error %v while checking IsIOInProgress for array having symID %s for volumeID/fileSystemID %s", err.Error(), symID, volID)
			return fmt.Errorf("error %v while while checking IsIOInProgress", err.Error())
		}
		// check last four entries status received in the response
		fileMetrics := resp.ResultList.Result
		for i := 0; i < len(fileMetrics); i++ {
			if fileMetrics[i].PercentBusy > 0.0 && checkIfEntryIsLatest(fileMetrics[i].Timestamp) {
				return nil
			}
		}
		return fmt.Errorf("no IOInProgress")
	}
	// check last four entries status received in the response
	for i := len(resp.ResultList.Result[0].VolumeResult) - 1; i >= (len(resp.ResultList.Result[0].VolumeResult)-4) && i >= 0; i-- {
		if resp.ResultList.Result[0].VolumeResult[i].IoRate > 0.0 && checkIfEntryIsLatest(resp.ResultList.Result[0].VolumeResult[i].Timestamp) {
			return nil
		}
	}
	return fmt.Errorf("no IOInProgress")
}

func checkIfEntryIsLatest(respTS int64) bool {
	timeFromResponse := time.Unix(respTS/1000, 0)
	log.Debugf("timestamp recieved from the response body is %v", timeFromResponse)
	currentTime := time.Now().UTC()
	log.Debugf("current time %v", currentTime)
	if currentTime.Sub(timeFromResponse).Seconds() < 60 {
		log.Debug("found a fresh metric")
		return true
	}
	return false
}
