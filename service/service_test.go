/*
Copyright © 2021-2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"fmt"
	"net/http"
	_ "net/http/pprof" // #nosec G108
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/cucumber/godog"
	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	pmax "github.com/dell/gopowermax/v2"
	gomock "github.com/golang/mock/gomock"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	gmock "go.uber.org/mock/gomock"
)

var (
	testStatus    int
	testStartTime time.Time
)

func TestMain(m *testing.M) {
	testStatus = 0
	testStartTime = time.Now()

	go http.ListenAndServe("localhost:6060", nil) // #nosec G114

	if st := m.Run(); st > testStatus {
		testStatus = st
	}

	fmt.Printf("status %d\n", testStatus)

	os.Exit(testStatus)
}

func TestGoDog(t *testing.T) {
	fmt.Printf("starting godog...\n")
	runOptions := godog.Options{
		Format: "pretty",
		Paths:  []string{"features"},
		Tags:   "v1.0.0, v1.1.0, v1.2.0, v1.3.0, v1.4.0, v1.5.0, v1.6.0, v2.2.0, v2.3.0, v2.4.0, v2.5.0, v2.6.0, v2.7.0, v2.8.0, v2.9.0, v2.11.0, v2.12.0, v2.13.0, v2.14.0",
		// Tags:   "wip",
		// Tags: "resiliency", // uncomment to run all node resiliency related tests,
	}
	testStatus = godog.TestSuite{
		Name:                "CSI Powermax Unit Test",
		ScenarioInitializer: FeatureContext,
		Options:             &runOptions,
	}.Run()

	fmt.Printf("godog finished\n")
	if testStatus != 0 {
		t.Error("Error encountered in godog testing")
	}
}

func TestGetStorageArrays(t *testing.T) {
	tests := []struct {
		name         string
		secretParams *viper.Viper
		expectedOpts Opts
		expectedLog  string
	}{
		{
			name: "No storage arrays declared",
			secretParams: func() *viper.Viper {
				v := viper.New()
				return v
			}(),
			expectedOpts: Opts{StorageArrays: make(map[string]StorageArrayConfig)},
			expectedLog:  "No storage array declared.",
		},
		{
			name: "No storage arrays found",
			secretParams: func() *viper.Viper {
				v := viper.New()
				v.Set("storagearrays", []interface{}{})
				return v
			}(),
			expectedOpts: Opts{StorageArrays: make(map[string]StorageArrayConfig)},
			expectedLog:  "No storage arrays found.",
		},
		{
			name: "Storage arrays with labels and parameters",
			secretParams: func() *viper.Viper {
				v := viper.New()
				v.Set("storagearrays", []interface{}{
					map[string]interface{}{
						"storagearrayid": "array1",
						"labels":         map[string]interface{}{"label1": "value1"},
						"parameters":     map[string]interface{}{"param1": "value1"},
					},
				})
				return v
			}(),
			expectedOpts: Opts{StorageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels:     map[string]interface{}{"label1": "value1"},
					Parameters: map[string]interface{}{"param1": "value1"},
				},
			}},
			expectedLog: "",
		},
		{
			name: "Storage arrays with labels and valid parameters",
			secretParams: func() *viper.Viper {
				v := viper.New()
				v.Set("storagearrays", []interface{}{
					map[string]interface{}{
						"storagearrayid": "array1",
						"labels":         map[string]interface{}{"label1": "value1"},
						"parameters":     map[string]interface{}{"SRP": "srp_1", "ServiceLevel": "Optimized", "ApplicationPrefix": "powermax", "HostLimitName": "limitset", "HostIOLimitMBSec": "1000", "HostIOLimitIOSec": "1001", "DynamicDistribution": "Always"},
					},
				})
				return v
			}(),
			expectedOpts: Opts{StorageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels:     map[string]interface{}{"label1": "value1"},
					Parameters: map[string]interface{}{"SRP": "srp_1", "ServiceLevel": "Optimized", "ApplicationPrefix": "powermax", "HostLimitName": "limitset", "HostIOLimitMBSec": "1000", "HostIOLimitIOSec": "1001", "DynamicDistribution": "Always"},
				},
			}},
			expectedLog: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Opts{StorageArrays: make(map[string]StorageArrayConfig)}
			GetStorageArrays(tt.secretParams, opts)

			if len(opts.StorageArrays) != len(tt.expectedOpts.StorageArrays) {
				t.Errorf("expected %v, got %v", tt.expectedOpts.StorageArrays, opts.StorageArrays)
			}

			for id, config := range tt.expectedOpts.StorageArrays {
				if opts.StorageArrays[id].Labels["label1"] != config.Labels["label1"] {
					t.Errorf("expected label %v, got %v", config.Labels["label1"], opts.StorageArrays[id].Labels["label1"])
				}
				if opts.StorageArrays[id].Parameters["param1"] != config.Parameters["param1"] {
					t.Errorf("expected parameter %v, got %v", config.Parameters["param1"], opts.StorageArrays[id].Parameters["param1"])
				}
			}
		})
	}
}

func TestFilterArraysByZoneInfo(t *testing.T) {
	testCases := []struct {
		name           string
		pmaxClient     pmax.Pmax
		secretParams   *viper.Viper
		getClient      func() *mocks.MockPmaxClient
		opts           Opts
		expectedArrays []string
		storageArrays  map[string]StorageArrayConfig
		initFunc       func() *k8smock.MockUtilsInterface
	}{
		{
			name: "Storage array and node labels match",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"topology.kubernetes.io/zone": "Z1"}, nil)
				return mockUtilsInterface
			},
			storageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels: map[string]interface{}{"topology.kubernetes.io/zone": "Z1"},
				},
			},
			expectedArrays: []string{"array1"},
		},
		{
			name: "Storage array and node labels do not match",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"topology.kubernetes.io/region": "R1"}, nil)
				return mockUtilsInterface
			},
			storageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels: map[string]interface{}{"differentlabel1": "differentvalue1"},
				},
			},
			expectedArrays: []string{},
		},
		{
			name: "Storage array and node do not have zone info",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil)
				return mockUtilsInterface
			},
			storageArrays: map[string]StorageArrayConfig{
				"array1": {
					Labels: map[string]interface{}{},
				},
			},
			expectedArrays: []string{"array1"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					NodeName:     "node1",
					NodeFullName: "node1",
				},
				k8sUtils: tc.initFunc(),
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			filteredArrays, _ := s.filterArraysByZoneInfo(tc.storageArrays)
			log.Infof("Filtered arrays: %v", filteredArrays)
			if !reflect.DeepEqual(filteredArrays, tc.expectedArrays) {
				t.Errorf("Expected %v, got %v", tc.expectedArrays, filteredArrays)
			}
		})
	}
}
