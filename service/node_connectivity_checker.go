package service

/*
 *
 * Copyright © 2022-2023 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/mux"
	log "github.com/sirupsen/logrus"
)

// pollingFrequency in seconds
var pollingFrequencyInSeconds int64

// probeStatus map[string]ArrayConnectivityStatus
var probeStatus *sync.Map

// startAPIService reads nodes to array status periodically
func (s *service) startAPIService(ctx context.Context) {
	if !s.opts.IsPodmonEnabled {
		log.Info("podmon is not enabled")
		return
	}
	pollingFrequencyInSeconds = s.SetPollingFrequency(ctx)
	s.startNodeToArrayConnectivityCheck(ctx)
	s.apiRouter(ctx)
}

// apiRouter serves http requests
func (s *service) apiRouter(_ context.Context) {
	log.Infof("starting http server on port %s", s.opts.PodmonPort)
	// create a new mux router
	router := mux.NewRouter()
	// route to connectivity status
	// connectivityStatus is the handlers
	router.HandleFunc(ArrayStatus, connectivityStatus).Methods("GET")
	router.HandleFunc(ArrayStatus+"/"+"{symID}", getArrayConnectivityStatus).Methods("GET")
	// start http server to serve requests
	server := &http.Server{
		Addr:         s.opts.PodmonPort,
		Handler:      router,
		ReadTimeout:  Timeout,
		WriteTimeout: Timeout,
	}
	err := server.ListenAndServe()
	if err != nil {
		log.Errorf("unable to start http server to serve status requests due to %s", err)
	}
	log.Infof("started http server to serve status requests at %s", s.opts.PodmonPort)
}

// connectivityStatus handler returns array connectivity status
func connectivityStatus(w http.ResponseWriter, _ *http.Request) {
	log.Infof("connectivityStatus called, status is %v \n", probeStatus)
	// w.Header().Set("Content-Type", "application/json")
	if probeStatus == nil {
		log.Error("error probeStatus map in cache is empty")
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		return
	}

	// convert struct to JSON
	log.Debugf("ProbeStatus fetched from the cache has %+v", probeStatus)

	jsonResponse, err := MarshalSyncMapToJSON(probeStatus)
	if err != nil {
		log.Errorf("error %s during marshaling to json", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		return
	}
	log.Info("sending connectivityStatus for all arrays ")
	w.Header().Set("Content-Type", "application/json")
	_, err = w.Write(jsonResponse)
	if err != nil {
		log.Errorf("unable to write response %s", err)
	}
}

// MarshalSyncMapToJSON marshal the sync Map to Json
func MarshalSyncMapToJSON(m *sync.Map) ([]byte, error) {
	tmpMap := make(map[string]ArrayConnectivityStatus)
	m.Range(func(k, value interface{}) bool {
		// this check is not necessary but just in case is someone in future play around this
		switch value.(type) {
		case ArrayConnectivityStatus:
			tmpMap[k.(string)] = value.(ArrayConnectivityStatus)
			return true
		default:
			log.Error("invalid data is stored in cache")
			return false
		}
	})
	log.Debugf("map value is %+v", tmpMap)
	if len(tmpMap) == 0 {
		return nil, errors.New("invalid data is stored in cache")
	}
	return json.Marshal(tmpMap)
}

// getArrayConnectivityStatus handler lists status of the requested array
func getArrayConnectivityStatus(w http.ResponseWriter, r *http.Request) {
	symID := mux.Vars(r)["symID"]
	log.Infof("GetArrayConnectivityStatus called for array %s \n", symID)
	status, found := probeStatus.Load(symID)
	if !found {
		// specify status code
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("Content-Type", "application/json")
		// update response writer
		fmt.Fprintf(w, "array %s not found \n", symID)
		return
	}
	// convert status struct to JSON
	jsonResponse, err := json.Marshal(status)
	if err != nil {
		log.Errorf("error %s during marshaling to json", err)
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Set("Content-Type", "application/json")
		return
	}
	log.Infof("sending response %+v for array %s \n", status, symID)
	// update response
	_, err = w.Write(jsonResponse)
	if err != nil {
		log.Errorf("unable to write response %s", err)
	}
}

// startNodeToArrayConnectivityCheck starts connectivityTest as one goroutine for each array
func (s *service) startNodeToArrayConnectivityCheck(ctx context.Context) {
	log.Debug("startNodeToArrayConnectivityCheck called")
	probeStatus = new(sync.Map)
	pMaxArrays, err := s.retryableGetSymmetrixIDList()
	if err != nil {
		log.Errorf("startNodeToArrayConnectivityCheck failed to get symID list %s ", err.Error())
	} else {
		for _, arr := range pMaxArrays.SymmetrixIDs {
			go s.testConnectivityAndUpdateStatus(ctx, arr, Timeout)
		}
		log.Infof("startNodeToArrayConnectivityCheck is running probes at pollingFrequency %d ", pollingFrequencyInSeconds/2)
	}
}

// testConnectivityAndUpdateStatus runs probe to test connectivity from node to array
// updates probeStatus map[array]ArrayConnectivityStatus
func (s *service) testConnectivityAndUpdateStatus(ctx context.Context, symID string, timeout time.Duration) {
	defer func() {
		if err := recover(); err != nil {
			log.Errorf("panic occurred in testConnectivityAndUpdateStatus: %s", err)
		}
		// if panic occurs restart new goroutine
		go s.testConnectivityAndUpdateStatus(ctx, symID, timeout)
	}()
	var status ArrayConnectivityStatus
	for {
		// add timeout to context
		timeOutCtx, cancel := context.WithTimeout(ctx, timeout)
		log.Debugf("Running probe for array %s at time %v \n", symID, time.Now())
		if existingStatus, ok := probeStatus.Load(symID); !ok {
			log.Debugf("%s not in probeStatus ", symID)
		} else {
			if status, ok = existingStatus.(ArrayConnectivityStatus); !ok {
				log.Errorf("failed to extract ArrayConnectivityStatus for array '%s'", symID)
			}
		}
		// for the first time status will not be there.
		log.Debugf("array %s , status is %+v", symID, status)
		// run nodeProbe to test connectivity
		err := s.nodeProbeBySymID(timeOutCtx, symID)
		if err == nil {
			log.Debugf("Probe successful for %s", symID)
			status.LastSuccess = time.Now().Unix()
		} else {
			log.Debugf("Probe failed for array '%s' error:'%s'", symID, err)
		}
		status.LastAttempt = time.Now().Unix()
		log.Debugf("array %s , storing status %+v", symID, status)
		probeStatus.Store(symID, status)
		cancel()
		// sleep for half the pollingFrequency and run check again
		time.Sleep(time.Second * time.Duration(pollingFrequencyInSeconds/2))
	}
}
