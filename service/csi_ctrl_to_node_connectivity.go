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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/dell/csmlog"
)

// ArrayConnectivityStatus Status of the array probe
type ArrayConnectivityStatus struct {
	LastSuccess int64 `json:"lastSuccess"` // connectivity status
	LastAttempt int64 `json:"lastAttempt"` // last timestamp attempted to check connectivity
}

const (
	// Timeout for making http requests
	Timeout = time.Second * 5
)

// QueryArrayStatus make API call to the specified url to retrieve connection status
func (s *service) QueryArrayStatus(ctx context.Context, url string) (bool, error) {
	defer func() {
		if err := recover(); err != nil {
			csmlog.Infof("panic occurred in queryStatus: %v", err)
		}
	}()
	client := http.Client{
		Timeout: Timeout,
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to build array status request for %s: %v", url, err)
		return false, err
	}
	if PodmonAPIToken != "" {
		req.Header.Set("Authorization", "Bearer "+PodmonAPIToken)
	}
	resp, err := client.Do(req)

	csmlog.WithContext(ctx).Debugf("Received response %+v for url %s", resp, url)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("failed to call API %s due to %s ", url, err.Error())
		return false, err
	}
	defer resp.Body.Close() // #nosec G307
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("failed to read API response due to %s ", err.Error())
		return false, err
	}
	if resp.StatusCode != 200 {
		csmlog.WithContext(ctx).Errorf("Found unexpected response from the server while fetching array status %d ", resp.StatusCode)
		return false, fmt.Errorf("unexpected response from the server")
	}
	var statusResponse ArrayConnectivityStatus
	err = json.Unmarshal(bodyBytes, &statusResponse)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("unable to unmarshal and determine connectivity due to %s ", err)
		return false, err
	}
	csmlog.WithContext(ctx).Infof("API Response received is %+v\n", statusResponse)
	// responseObject has last success and last attempt timestamp in Unix format
	timeDiff := statusResponse.LastAttempt - statusResponse.LastSuccess
	tolerance := s.SetPollingFrequency(ctx)
	currTime := time.Now().Unix()
	// checking if the status response is stale and connectivity test is still running
	// since nodeProbe is run at frequency tolerance/2, ideally below check should never be true
	if (currTime - statusResponse.LastAttempt) > tolerance*2 {
		csmlog.WithContext(ctx).Errorf("seems like connectivity test is not being run, current time is %d and last run was at %d", currTime, statusResponse.LastAttempt)
		// considering connectivity is broken
		return false, nil
	}
	csmlog.WithContext(ctx).Debugf("last connectivity was  %d sec back, tolerance is %d sec", timeDiff, tolerance)
	// give 2s leeway for tolerance check
	if timeDiff <= tolerance+2 {
		return true, nil
	}
	return false, nil
}
