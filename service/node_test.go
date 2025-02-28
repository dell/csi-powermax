/*
Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"errors"
	"fmt"
	"net"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/csi-powermax/v2/k8smock"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix"
	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gofsutil"
	"github.com/dell/goiscsi"

	gonvme "github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	gomock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
	gmock "go.uber.org/mock/gomock"
)

func TestGetNVMeTCPTargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		symID      string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		want       []NVMeTCPTargetInfo
		wantErr    bool
	}{
		{
			name:  "Successful case",
			symID: "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn1",
						PortalIPs: []string{"portal1"},
					},
					{
						NQN:       "nqn2",
						PortalIPs: []string{"portal2"},
					},
				}, nil)
				return c
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:  "No matching targets",
			symID: "array2",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array2").AnyTimes().Return([]pmax.NVMeTCPTarget{}, nil)
				return c
			},
			want:    nil,
			wantErr: false,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				nvmetcpClient: gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:   &sync.Map{},
				// Set the necessary fields for testing
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			got, err := s.getNVMeTCPTargets(context.Background(), tc.symID, tc.pmaxClient)
			if (err != nil) != tc.wantErr {
				t.Errorf("Expected error: %v, but got: %v", tc.wantErr, err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Expected: %v, but got: %v", tc.want, got)
			}
		})
	}
}

func TestGetAndConfigureArrayNVMeTCPTargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name         string
		symID        string
		arrayTargets []string
		getClient    func() *mocks.MockPmaxClient
		init         func()
		pmaxClient   pmax.Pmax
		want         []NVMeTCPTargetInfo
		wantErr      bool
	}{
		{
			name:         "Valid case with different cached targets and provided targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{}, fmt.Errorf("No matching targets"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.emc.mock:9992d5b871f1403E169D00001"},
					},
				})
			},
			want: nil,
		}, // This finding it in cache even after invalidating cache!!
		{
			name:         "Valid case",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Valid case with some cached targets",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching port",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching port group",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{}, fmt.Errorf("No matching portgroup"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching mv",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{}, fmt.Errorf("no matching mv"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Valid case : cache contains data but invalid for this case",
			arrayTargets: []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetNVMeTCPTargets(gmock.All(), "array1").AnyTimes().Return([]pmax.NVMeTCPTarget{
					{
						NQN:       "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
			}

			tc.init()
			got := s.getAndConfigureArrayNVMeTCPTargets(context.Background(), tc.arrayTargets, tc.symID, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestGetAndConfigureISCSITargets(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name         string
		symID        string
		arrayTargets []string
		getClient    func() *mocks.MockPmaxClient
		init         func()
		pmaxClient   pmax.Pmax
		want         []ISCSITargetInfo
		wantErr      bool
	}{
		{
			name:         "Valid case with different cached targets and provided targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{}, fmt.Errorf("No matching targets"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.emc.mock:9992d5b871f1403E169D00001"},
					},
				})
			},
			want: nil,
		},
		{
			name:         "Valid case",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Valid case with some cached targets",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001", "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00002"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)

				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"1.1.1.1"},
					},
				}, nil)
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()

				symToMaskingViewTargets.Store("array1", []maskingViewTargetInfo{
					{
						target: goiscsi.ISCSITarget{Portal: "1.1.1.1", Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []ISCSITargetInfo{
				{
					Target: "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		{
			name:         "Error case: no matching port",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching port group",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{}, fmt.Errorf("No matching portgroup"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Error case: no matching mv",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{}, fmt.Errorf("no matching mv"))
				return c
			},
			init: func() {
				s.InvalidateSymToMaskingViewTargets()
			},
			want: nil,
		},
		{
			name:         "Valid case: cache contains data but invalid for this case",
			arrayTargets: []string{"iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:        "array1",
			getClient: func() *mocks.MockPmaxClient {
				s.InvalidateSymToMaskingViewTargets()
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetISCSITargets(gmock.All(), "array1").AnyTimes().Return([]pmax.ISCSITarget{
					{
						IQN:       "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						PortalIPs: []string{"portal1"},
					},
				}, nil)
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv--").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv--",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			init: func() {
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{})
			},
			want: nil,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				iscsiClient:    goiscsi.NewMockISCSI(map[string]string{}),
				iscsiTargets:   map[string][]string{},
				loggedInArrays: map[string]bool{},
			}
			tc.init()
			got := s.getAndConfigureArrayISCSITargets(context.Background(), tc.arrayTargets, tc.symID, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestConnectRDMDevice(t *testing.T) {
	// Create a mock gobrick.FCConnector
	mockConnector := &mockFCGobrick{}
	// Create a mock context
	ctx := context.Background()

	// Create a mock publishContextData
	data := publishContextData{
		deviceWWN:        "mockWWN",
		volumeLUNAddress: "10",
		fcTargets: []FCTargetInfo{
			{
				WWPN: "mockWWPN",
			},
		},
	}
	// Create a mock service
	s := &service{
		fcConnector: mockConnector,
	}
	// Call the connectRDMDevice function
	device, err := s.connectRDMDevice(ctx, int(10), data)
	// Assert the expected result
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	// deviceWWN is hardcoded in gobrick mocks, check for that to be returned
	if device.WWN != "60000970000197900046533030300501" {
		t.Errorf("Expected WWN to be 'mockWWN', got '%s'", device.WWN)
	}
}

func TestGetHostForVsphere(t *testing.T) {
	ctx := context.Background()
	vsphereHostName := "vsphere-host"
	array := "array1"
	tests := []struct {
		name                 string
		hostResponse         *types.Host
		hostResponseError    error
		hostGroupResponse    *types.HostGroup
		expectedErr          error
		hostGroupResponseErr error
	}{
		{
			name:              "Host exists",
			hostResponse:      &types.Host{HostID: vsphereHostName, HostType: "FC"},
			hostGroupResponse: &types.HostGroup{},
			expectedErr:       nil,
		},
		{
			name:         "Host does not exist, HostGroup exists",
			hostResponse: nil,
			hostGroupResponse: &types.HostGroup{
				HostGroupID: "host-group-name",
				NumOfHosts:  1,
				Hosts: []types.HostSummary{
					{HostID: vsphereHostName},
				},
			},
			hostResponseError:    errors.New("cannot be found"),
			hostGroupResponseErr: nil,
			expectedErr:          nil,
		},
		{
			name:                 "Host and HostGroup do not exist",
			hostResponse:         nil,
			hostGroupResponse:    nil,
			hostResponseError:    errors.New("cannot be found"),
			hostGroupResponseErr: errors.New("cannot be found"),
			expectedErr:          errors.New("cannot be found"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pmaxClient := func() *mocks.MockPmaxClient {
				client := mocks.NewMockPmaxClient(gmock.NewController(t))
				client.EXPECT().GetHTTPClient().AnyTimes().Return(&http.Client{})
				client.EXPECT().GetHostByID(gmock.Any(), gmock.Any(), gmock.Any()).AnyTimes().Return(tt.hostResponse, tt.hostResponseError)
				client.EXPECT().GetHostGroupByID(gmock.Any(), gmock.Any(), gmock.Any()).AnyTimes().Return(tt.hostGroupResponse, tt.hostGroupResponseErr)
				return client
			}()
			s := &service{
				opts: Opts{
					VSphereHostName: vsphereHostName,
				},
			}
			err := s.getHostForVsphere(ctx, array, pmaxClient)
			if tt.expectedErr != nil {
				require.Error(t, err)
				require.True(t, strings.Contains(err.Error(), tt.expectedErr.Error()))
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestIsISCSIConnected(t *testing.T) {
	s := &service{}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"AuthFailed", errors.New("exit status 24"), true},
		{"SessionExists", errors.New("exit status 15"), true},
		{"OtherError", errors.New("exit status 1"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := s.isISCSIConnected(tt.err)
			if got != tt.want {
				t.Errorf("isISCSIConnected() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCreateOrUpdateNVMeTCPHost(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		array      string
		nodeName   string
		NQNs       []string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		want       *types.Host
		wantErr    bool
	}{
		{
			name:     "Valid case",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(&types.Host{
					HostID: "host1",
					Initiators: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			want: &types.Host{
				HostID: "host1",
				Initiators: []string{
					"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
				},
			},
			wantErr: false,
		},
		{
			name:     "Invalid host but created successfully",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			wantErr: false,
			want: &types.Host{
				HostID: "host1",
			},
		},
		{
			// This is really bizarre condition in the code to test, but it is what it is.
			name:     "create host failure",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(nil, errors.New("create host failed"))
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			wantErr: false,
			want:    &types.Host{},
		},
		{
			// This is really bizarre condition in the code to test, but it is what it is.
			// When createHost fails, it should return and error from the code, but instead returning nil host and no error.
			name:     "create host failure, with bad NQNs error",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(nil, errors.New("host not found"))
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(nil, errors.New("is not in the format of a valid NQN:HostID"))
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{
					InitiatorIDs: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", "host1", gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			wantErr: false,
			want:    nil,
		},
		{
			name:     "array empty case",
			array:    "",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    &types.Host{},
		},
		{
			name:     "nodename empty case",
			array:    "array1",
			nodeName: "",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    &types.Host{},
		},
		{
			name:     "len NQNs zero case",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			wantErr: true,
			want:    &types.Host{},
		},
		{
			name:     "Host exists, add new initiators",
			array:    "array1",
			nodeName: "host1",
			NQNs:     []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00222"},
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetHostByID(gmock.All(), "array1", "host1").AnyTimes().Return(&types.Host{
					HostID: "host1",
					Initiators: []string{
						"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				c.EXPECT().CreateHost(gmock.All(), "array1", "host1", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				c.EXPECT().GetInitiatorList(gmock.All(), "array1", "", false, false).AnyTimes().Return(&types.InitiatorList{}, nil)
				c.EXPECT().GetInitiatorByID(gmock.All(), "array1", "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001").AnyTimes().Return(
					&types.Initiator{InitiatorID: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"}, nil)
				c.EXPECT().UpdateHostInitiators(gmock.All(), "array1", gmock.All(), []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00222"}).AnyTimes().Return(&types.Host{HostID: "host1"}, nil)
				return c
			},
			want: &types.Host{
				HostID: "host1",
				Initiators: []string{
					"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
				},
			},
			wantErr: false,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
				pmaxTimeoutSeconds: 1,
			}
			got, err := s.createOrUpdateNVMeTCPHost(context.Background(), tc.array, tc.nodeName, tc.NQNs, tc.pmaxClient)
			if err == nil && tc.wantErr {
				t.Errorf("Expected: %v, but got no error", tc.wantErr)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Expected: %v, but got: %v", tc.want, got)
			}
		})
	}
}

func TestPerformNVMETCPLoginOnSymID(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name       string
		array      string
		mvName     string
		getClient  func() *mocks.MockPmaxClient
		pmaxClient pmax.Pmax
		initFunc   func()
		want       []NVMeTCPTargetInfo
		wantErr    bool
	}{
		{
			name:   "Successful case",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{
					MaskingViewID: "csi-mv---NVMETCP",
					PortGroupID:   "portgroup1",
				}, nil)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "GetNVMeTargets fails, GetMaskingViewByID returns error does not exist",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(nil,
					fmt.Errorf("Masking View %s does not exist for array %s, skipping login", "csi-mv---NVMETCP", "array1"))
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "GetMaskingViewByID returns error not found",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(nil,
					fmt.Errorf("Masking View %s not found for array %s, skipping login", "csi-mv---NVMETCP", "array1"))
				return c
			},
			initFunc: func() {
				s.InvalidateSymToMaskingViewTargets()
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: true,
		},
		{
			name:   "loginToNVMETargets succeeds, valid cache",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
			wantErr: false,
		},
		{
			name:   "loginToNVMETargets fails, valid cache",
			array:  "array1",
			mvName: "csi-mv---NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = true
				symToMaskingViewTargets.Store("array1", []maskingViewNVMeTargetInfo{
					{
						target: gonvme.NVMeTarget{Portal: "1.1.1.1", TargetNqn: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
					},
				})
			},
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn1",
					Portal: "portal1",
				},
				{
					Target: "nqn2",
					Portal: "portal2",
				},
			},
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					UseProxy: true,
				},
				loggedInNVMeArrays: map[string]bool{},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			tc.initFunc()
			err := s.performNVMETCPLoginOnSymID(context.Background(), tc.array, tc.mvName, tc.pmaxClient)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			} else if !tc.wantErr && err != nil {
				t.Errorf("Expected no error but got %v", err)
			}
		})
	}
}

func TestCreateTopologyMap(t *testing.T) {
	var listener net.Listener
	testCases := []struct {
		name                      string
		nodeName                  string
		getClient                 func() *mocks.MockPmaxClient
		pmaxClient                pmax.Pmax
		nvmeTCPClient             *gonvme.MockNVMe
		iscsiClient               *goiscsi.MockISCSI
		managedArrays             []string
		arrayTransportProtocolMap map[string]string
		initFunc                  func()
		loggedInNVMeArrays        map[string]bool
		loggedInArrays            map[string]bool
		portGroups                []string
		want                      *csi.NodeGetInfoRequest
		wantErr                   bool
	}{
		{
			name: "Success case NVME with logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name: "Success case NVME no logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
				gonvme.GONVMEMock.InduceDiscoveryError = false
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": false,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name: "Success case ISCSI, no logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array3").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array3", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array3", gmock.Any(), gmock.Any()).AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"127.0.0.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
						TCPPort:     9090,
					},
				}, nil)
				symmetrix.Initialize([]string{"array3"}, c)
				return c
			},
			managedArrays: []string{"array3"},
			arrayTransportProtocolMap: map[string]string{
				"array3": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
				listener, _ = net.Listen("tcp", "127.0.0.1:9090")
			},
			loggedInArrays: map[string]bool{
				"array3": false,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Success case ISCSI, logged in arrays",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays: map[string]bool{
				"array1": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Error case ISCSI, discover targets failed with no exit status",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "iqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": IscsiTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			iscsiClient:   goiscsi.NewMockISCSI(map[string]string{}),
			initFunc: func() {
				goiscsi.GOISCSIMock.InduceDiscoveryError = true
			},
			loggedInArrays: map[string]bool{
				"array1": true,
			},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Invalid case,FC selected",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": FcTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Invalid case,Vsphere selected",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": Vsphere,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            false,
		},
		{
			name: "Failure case, topolgy create fails",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			managedArrays:             []string{""},
			arrayTransportProtocolMap: map[string]string{},
			nvmeTCPClient:             gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            true,
		},
		{
			name: "Error case NVME invalid portgroup",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array2").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array2", "portgroup1").MaxTimes(1).Return(nil, errors.New("portgroup not found"))
				symmetrix.Initialize([]string{"array2"}, c)
				return c
			},
			managedArrays: []string{"array2"},
			arrayTransportProtocolMap: map[string]string{
				"array2": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() {
			},
			loggedInArrays:     map[string]bool{},
			loggedInNVMeArrays: map[string]bool{},
			portGroups:         []string{"portgroup1"},
			wantErr:            true,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					ManagedArrays: tc.managedArrays,
					PortGroups:    tc.portGroups,
				},
				arrayTransportProtocolMap: tc.arrayTransportProtocolMap,
				nvmetcpClient:             tc.nvmeTCPClient,
				iscsiClient:               tc.iscsiClient,
				loggedInArrays:            tc.loggedInArrays,
				loggedInNVMeArrays:        tc.loggedInNVMeArrays,
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			tc.initFunc()

			topo := s.createTopologyMap(context.Background(), tc.nodeName)
			if len(topo) != 0 && tc.wantErr {
				t.Errorf("Expected error but got none")
			} else if len(topo) == 0 && !tc.wantErr {
				t.Errorf("Expected no error but got no topology map")
			}
			if listener != nil {
				listener.Close()
			}
		})
	}
}

func TestNodeGetInfo(t *testing.T) {
	type test struct {
		name                      string
		nodeName                  string
		getClient                 func() *mocks.MockPmaxClient
		pmaxClient                pmax.Pmax
		nvmeTCPClient             *gonvme.MockNVMe
		iscsiClient               *goiscsi.MockISCSI
		managedArrays             []string
		arrayTransportProtocolMap map[string]string
		initFunc                  func() *k8smock.MockUtilsInterface
		loggedInNVMeArrays        map[string]bool
		loggedInArrays            map[string]bool
		portGroups                []string
		isVsphereEnabled          bool
		mockUtilsInterface        *k8smock.MockUtilsInterface
		maxVolumesPerNode         int64
		csiNodeGetInfoRequest     *csi.NodeGetInfoRequest
		want                      map[string]string
		wantErr                   bool
	}

	testCases := []test{
		{
			name:              "Success",
			nodeName:          "node1",
			maxVolumesPerNode: 1,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "1"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, node labels does not exist",
			nodeName:          "node1",
			maxVolumesPerNode: -1,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))

				// Set up expectations
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, vsphere enabled, node labels exists",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "0"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
		{
			name:              "Success, vsphere enabled, node labels parse error",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{"max-powermax-volumes-per-node": "one"}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    true,
		},
		{
			name:              "Failure case, no node name",
			nodeName:          "",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    true,
		},
		{
			name:              "Success, vsphere enabled, node labels does not exist",
			nodeName:          "node1",
			maxVolumesPerNode: 0,
			isVsphereEnabled:  true,
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().WithSymmetrixID("array1").AnyTimes().Return(c)
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				symmetrix.Initialize([]string{"array1"}, c)
				return c
			},
			managedArrays: []string{"array1"},
			arrayTransportProtocolMap: map[string]string{
				"array1": NvmeTCPTransportProtocol,
			},
			nvmeTCPClient: gonvme.NewMockNVMe(map[string]string{}),
			initFunc: func() *k8smock.MockUtilsInterface {
				mockUtilsInterface := k8smock.NewMockUtilsInterface(gomock.NewController(t))
				mockUtilsInterface.EXPECT().GetNodeLabels("node1").Return(map[string]string{}, nil)
				return mockUtilsInterface
			},
			loggedInArrays: map[string]bool{},
			loggedInNVMeArrays: map[string]bool{
				"array1": true,
			},
			portGroups: []string{"portgroup1"},
			wantErr:    false,
		},
	}
	// Run the tests
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a new service instance for testing
			s := &service{
				opts: Opts{
					NodeName:          tc.nodeName,
					NodeFullName:      tc.nodeName,
					ManagedArrays:     tc.managedArrays,
					PortGroups:        tc.portGroups,
					MaxVolumesPerNode: tc.maxVolumesPerNode,
					IsVsphereEnabled:  tc.isVsphereEnabled,
				},
				arrayTransportProtocolMap: tc.arrayTransportProtocolMap,
				nvmetcpClient:             tc.nvmeTCPClient,
				iscsiClient:               tc.iscsiClient,
				loggedInArrays:            tc.loggedInArrays,
				loggedInNVMeArrays:        tc.loggedInNVMeArrays,
				k8sUtils:                  tc.initFunc(),
			}
			tc.pmaxClient = tc.getClient()
			// Call the function and check the results
			_, err := s.NodeGetInfo(context.Background(), tc.csiNodeGetInfoRequest)
			if tc.wantErr && err == nil {
				t.Errorf("Expected error but got none")
			}
		})
	}
}

func TestDisconnectVolume(t *testing.T) {
	type tests struct {
		name                      string
		reqID                     string
		symID                     string
		devID                     string
		volumeWWN                 string
		arrayTransportProtocolMap map[string]string
		initMocksFunc             func(tc tests)
		expectedErr               bool
		expectedLogOut            string
	}

	testCases := []tests{
		{
			name:                      "successful disconnect ISCSI",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "iscsi-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "ISCSI"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect FC",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "fc-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "FC"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect NVME",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "nvme-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "NVMETCP"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:                      "successful disconnect Vsphere",
			reqID:                     "reqID1",
			symID:                     "symID1",
			devID:                     "vsphere-devID1",
			volumeWWN:                 "60000970000197900046533030300501",
			arrayTransportProtocolMap: map[string]string{"symID1": "VSPHERE"},
			initMocksFunc: func(tc tests) {
				gofsutil.UseMockFS()
				gofsutil.GOFSWWNPath = nodePublishSymlinkDir
				gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
				gofsutil.GOFSMockWWNToDevice = map[string]string{tc.volumeWWN: tc.devID}
				mnt := gofsutil.Info{
					Device: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
					Path:   nodePublishPrivateDir + "/" + volume1,
					Source: nodePublishSymlinkDir + "/wwn-0x" + tc.volumeWWN,
				}
				gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstageVolume disconnectVolume\n",
		},
		{
			name:      "failed to find device path",
			reqID:     "reqID2",
			symID:     "symID2",
			devID:     "devID2",
			volumeWWN: "60000970000197900046533030300005",
			initMocksFunc: func(_ tests) {
				gofsutil.UseMockFS()
			},
			expectedErr:    false,
			expectedLogOut: "NodeUnstage: Didn't find device path for volume wwn2\n",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				arrayTransportProtocolMap: tt.arrayTransportProtocolMap,
			}
			// setup mocks
			s.iscsiConnector = &mockISCSIGobrick{}
			s.fcConnector = &mockFCGobrick{}
			s.nvmeTCPConnector = &mockNVMeTCPConnector{}
			tt.initMocksFunc(tt)

			// Call the function under test
			err := s.disconnectVolume(tt.reqID, tt.symID, tt.devID, tt.volumeWWN)

			if tt.expectedErr && err == nil {
				t.Errorf("disconnectVolume() error = %v, expectedErr %v", err, tt.expectedErr)
			}

			if !tt.expectedErr && err != nil {
				t.Errorf("disconnectVolume() error = %v, expectedErr %v", err, tt.expectedErr)
			}
		})
	}
}

func TestCheckIfArrayProtocolValid(t *testing.T) {
	tests := []struct {
		name                     string
		nodeName                 string
		array                    string
		protocol                 string
		isTopologyControlEnabled bool
		allowedList              map[string][]string
		deniedList               map[string][]string
		want                     bool
	}{
		{
			name:                     "allowed list contains the key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"node1": {
					"array1.protocol1",
				},
			},
			deniedList: map[string][]string{},
			want:       true,
		},
		{
			name:                     "allowed list does not contain key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"node1": {
					"array1.protocol2",
				},
			},
			deniedList: map[string][]string{},
			want:       false,
		},
		{
			name:                     "allowed list contains the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"*": {
					"array1.protocol1",
				},
			},
			deniedList: map[string][]string{},
			want:       true,
		},
		{
			name:                     "allowed list does not contain the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList: map[string][]string{
				"*": {
					"array1.protocol2",
				},
			},
			deniedList: map[string][]string{},
			want:       false,
		},
		{
			name:                     "denied list contains the key",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList:              map[string][]string{},
			deniedList: map[string][]string{
				"node1": {
					"array1.protocol1",
				},
			},
			want: false,
		},
		{
			name:                     "denied list contains the key with wildcard",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: true,
			allowedList:              map[string][]string{},
			deniedList: map[string][]string{
				"*": {
					"array1.protocol1",
				},
			},
			want: false,
		},
		{
			name:                     "topology control is disabled",
			nodeName:                 "node1",
			array:                    "array1",
			protocol:                 "protocol1",
			isTopologyControlEnabled: false,
			allowedList:              map[string][]string{},
			deniedList:               map[string][]string{},
			want:                     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					IsTopologyControlEnabled: tt.isTopologyControlEnabled,
				},
				allowedTopologyKeys: tt.allowedList,
				deniedTopologyKeys:  tt.deniedList,
			}
			// Call the function under test
			got := s.checkIfArrayProtocolValid(tt.nodeName, tt.array, tt.protocol)

			// Assert the results
			if got != tt.want {
				t.Errorf("checkIfArrayProtocolValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetIPIntefaces(t *testing.T) {
	// Define test cases
	testCases := []struct {
		name              string
		symID             string
		arrayTargets      []string
		getClient         func() *mocks.MockPmaxClient
		portGroups        []string
		transportProtocol string
		init              func()
		pmaxClient        pmax.Pmax
		want              []string
		wantErr           bool
	}{
		{
			name:              "Valid case",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					PortGroupType: "NVMETCP",
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{
					SymmetrixPort: types.SymmetrixPortType{
						IPAddresses: []string{"1.1.1.1"},
						Identifier:  "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					},
				}, nil)
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: []string{"1.1.1.1"},
		},
		{
			name:              "Error case, get port group error",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(nil, errors.New("port group not found"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: nil,
		},
		{
			name:              "Error case, get port error",
			arrayTargets:      []string{"nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001"},
			symID:             "array1",
			portGroups:        []string{"portgroup1"},
			transportProtocol: "NVMETCP",
			getClient: func() *mocks.MockPmaxClient {
				c := mocks.NewMockPmaxClient(gmock.NewController(t))
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{
					PortGroupType: "NVMETCP",
					SymmetrixPortKey: []types.PortKey{
						{
							DirectorID: "director1",
							PortID:     "port1",
						},
					},
				}, nil)
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(nil, errors.New("port not found"))
				return c
			},
			init: func() {
				symToAllNVMeTCPTargets.Clear()
			},
			want: nil,
		},
	}

	// Run the tests
	for _, tc := range testCases {
		tc.pmaxClient = tc.getClient()
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				opts: Opts{
					UseProxy:          true,
					TransportProtocol: tc.transportProtocol,
				},
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
			}

			tc.init()
			got, _ := s.getIPInterfaces(context.Background(), tc.symID, tc.portGroups, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}

func TestGetVolumeStats(t *testing.T) {
	gofsutil.UseMockFS()

	wwn := "1234567890ABCDEF"
	gofsutil.GOFSMockMounts = make([]gofsutil.Info, 0)
	gofsutil.GOFSMockWWNToDevice = map[string]string{wwn: "deviceID1"}
	mnt := gofsutil.Info{
		Device: nodePublishSymlinkDir + "/wwn-0x" + wwn,
		Path:   nodePublishPrivateDir + "/" + volume1,
		Source: nodePublishSymlinkDir + "/wwn-0x" + wwn,
	}
	gofsutil.GOFSMockMounts = append(gofsutil.GOFSMockMounts, mnt)

	// success case
	_, _, _, _, _, _, err := getVolumeStats(context.Background(), mnt.Path)
	if err != nil {
		t.Errorf("Expected: success, but got: %v", err)
	}

	// Error case
	gofsutil.GOFSMock.InduceFilesystemInfoError = true
	_, _, _, _, _, _, err = getVolumeStats(context.Background(), mnt.Path)
	if err == nil {
		t.Errorf("Expected: error, but got: %v", err)
	}
}

func TestReachableEndPoint(t *testing.T) {
	type args struct {
		endpoint string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"Unreachable IP", args{endpoint: "10.255.1.2:100"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.reachableEndPoint(tt.args.endpoint); got != tt.want {
				t.Errorf("reachableEndPoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
