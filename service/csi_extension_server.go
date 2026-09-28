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

	"github.com/dell/csmlog"

	"github.com/dell/dell-csi-extensions/podmon"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// OneHour is time used for metrics
const OneHour int64 = 3600000

// FiveMinutes is the SRDF performance metrics query window in milliseconds.
// The Unisphere performance API collects SRDF metrics at 5-minute granularity,
// so a 5-minute window returns exactly one (the most recent) sample.
const FiveMinutes int64 = 300000

var metricsQuery = []string{"HostMBs", "MBRead", "MBWritten", "IoRate", "Reads", "Writes", "ResponseTime"}

func (s *service) ValidateVolumeHostConnectivity(ctx context.Context, req *podmon.ValidateVolumeHostConnectivityRequest) (*podmon.ValidateVolumeHostConnectivityResponse, error) {
	csmlog.WithContext(ctx).WithFields(csmlog.Fields{
		csmlog.FieldOperation: "ValidateVolumeHostConnectivity",
		csmlog.FieldProtocol:  s.opts.TransportProtocol,
	}).Info("ValidateVolumeHostConnectivity called")

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
	// isMetroVolume tracks whether any volume in the request is a Metro volume.
	// For Metro volumes in non-uniform mode, a node may only reach one array in the
	// pair, so connectivity to either array should be treated as connected.
	isMetroVolume := false
	symID := req.GetArrayId()
	if symID == "" {
		if len(req.GetVolumeIds()) == 0 {
			csmlog.WithContext(ctx).Info("neither symID nor volumeID is present in request")
			// When neither symID nor volumeID is provided, return connectivity unknown
			rep.Messages = append(rep.Messages, "connectivity unknown for array")
			return rep, nil
		}
		// for loop req.GetVolumeIds()
		for _, volID := range req.GetVolumeIds() {
			_, symID, _, remoteSymID, _, err := s.parseCsiID(volID)
			if err != nil || symID == "" {
				csmlog.WithContext(ctx).Errorf("unable to retrieve array's symID after parsing volumeID")
				for _, arr := range s.opts.ManagedArrays {
					symIDs[arr] = true
				}
			} else {
				symIDs[symID] = true
				// For Metro volumes, add the remote array so connectivity can
				// be validated against both sides of the SRDF pair.
				if remoteSymID != "" {
					symIDs[remoteSymID] = true
					isMetroVolume = true
				}
			}
		}
	} else {
		symIDs[symID] = true
	}

	// Go through each of the symIDs and check connectivity.
	// For Metro volumes in non-uniform mode, a node is expected to reach only
	// one of the two arrays in the SRDF pair.  We therefore check every array
	// and, for Metro volumes, treat the node as connected if it can reach at
	// least one array in the pair.
	anyConnected := false
	for symID := range symIDs {
		// Check if the array is visible from the node
		perArrayRep := &podmon.ValidateVolumeHostConnectivityResponse{
			Messages: make([]string, 0),
		}
		err := s.checkIfNodeIsConnected(ctx, symID, req.GetNodeId(), perArrayRep)
		if err != nil {
			rep.Messages = append(rep.Messages, perArrayRep.Messages...)
			return rep, err
		}
		rep.Messages = append(rep.Messages, perArrayRep.Messages...)
		if perArrayRep.Connected {
			anyConnected = true
		}
	}

	// For Metro volumes in non-uniform mode, a node only reaches one array in
	// the SRDF pair, so connectivity to either array is sufficient.
	// For non-Metro volumes there is only one symID, so this is equivalent to
	// the original behavior of checking that single array.
	rep.Connected = anyConnected
	if isMetroVolume && !anyConnected {
		csmlog.Infof("Metro volume: node %s has no connectivity to either array in the SRDF pair", req.GetNodeId())
	}

	// Check for IOinProgress only when volume IDs are present in the request
	if len(req.GetVolumeIds()) > 0 {
		for _, volID := range req.GetVolumeIds() {
			_, symIDForVol, devID, remoteSymID, remoteDevID, _ := s.parseCsiID(volID)
			// Validate that the volume belongs to one of the arrays we expect
			if !symIDs[symIDForVol] && (remoteSymID == "" || !symIDs[remoteSymID]) {
				csmlog.WithContext(ctx).Errorf("Received symID from podmon but volume belongs to unexpected array %s", symIDForVol)
				return nil, fmt.Errorf("invalid symID %s is provided", symIDForVol)
			}
			// Check IO on the primary array/device
			err := s.IsIOInProgress(ctx, devID, symIDForVol)
			if err == nil {
				rep.IosInProgress = true
				return rep, nil
			}
			// For Metro volumes, also check IO on the remote array/device
			if remoteSymID != "" && remoteDevID != "" {
				err = s.IsIOInProgress(ctx, remoteDevID, remoteSymID)
				if err == nil {
					rep.IosInProgress = true
					csmlog.Infof("IO in progress detected on remote Metro array %s device %s", remoteSymID, remoteDevID)
					return rep, nil
				}
			}
		}
	}
	csmlog.WithContext(ctx).Infof("ValidateVolumeHostConnectivity reply %+v", rep)
	return rep, nil
}

// checkIfNodeIsConnected looks at the 'nodeId' to determine if there is connectivity to the 'arrayId' array.
// The 'rep' object will be filled with the results of the check.
func (s *service) checkIfNodeIsConnected(ctx context.Context, symID string, nodeID string, rep *podmon.ValidateVolumeHostConnectivityResponse) error {
	csmlog.WithContext(ctx).Infof("Checking if array %s is connected to node %s", symID, nodeID)
	var message string
	rep.Connected = false
	nodeIP := s.k8sUtils.GetNodeIPs(nodeID)
	if len(nodeIP) == 0 {
		csmlog.WithContext(ctx).Errorf("failed to parse node ID '%s'", nodeID)
		return fmt.Errorf("failed to parse node ID")
	}
	// form url to call array on node
	url := "http://" + nodeIP + s.opts.PodmonPort + ArrayStatus + "/" + symID
	connected, err := s.QueryArrayStatus(ctx, url)
	if err != nil {
		message = fmt.Sprintf("connectivity unknown for array %s to node %s due to %s", symID, nodeID, err)
		csmlog.WithContext(ctx).Error(message)
		rep.Messages = append(rep.Messages, message)
		csmlog.WithContext(ctx).Errorf("%s", err.Error())
	}

	if connected {
		rep.Connected = true
		message = fmt.Sprintf("array %s is connected to node %s", symID, nodeID)
	} else {
		message = fmt.Sprintf("array %s is not connected to node %s", symID, nodeID)
	}
	csmlog.WithContext(ctx).Info(message)
	rep.Messages = append(rep.Messages, message)
	return nil
}

// IsIOInProgress function check the IO operation status on array
func (s *service) IsIOInProgress(ctx context.Context, volID, symID string) (err error) {
	// Call PerformanceMetricsByVolume or PerformanceMetricsByFileSystem in gopowermax based on the volume type
	pmaxClient, err := s.GetPowerMaxClient(symID)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
		return status.Error(codes.InvalidArgument, err.Error())
	}
	arrayKeys, err := pmaxClient.GetArrayPerfKeys(ctx)
	if err != nil {
		csmlog.WithContext(ctx).Error(err.Error())
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
			csmlog.WithContext(ctx).Errorf("Error %v while checking IsIOInProgress for array having symID %s for volumeID/fileSystemID %s", err.Error(), symID, volID)
			return fmt.Errorf("error %v while while checking IsIOInProgress", err.Error())
		}
		if resp == nil || len(resp.ResultList.Result) == 0 {
			return fmt.Errorf("no IOInProgress - no performance results returned for FileSystem (NFS) volume %s on array %s", volID, symID)
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
	if resp == nil || len(resp.ResultList.Result) == 0 {
		return fmt.Errorf("no IOInProgress - no performance results returned for volume %s on array %s", volID, symID)
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
	csmlog.Debugf("timestamp recieved from the response body is %v", timeFromResponse)
	currentTime := time.Now().UTC()
	csmlog.Debugf("current time %v", currentTime)
	if currentTime.Sub(timeFromResponse).Seconds() < 60 {
		csmlog.Debug("found a fresh metric")
		return true
	}
	return false
}
