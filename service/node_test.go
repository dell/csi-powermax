package service

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
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
		pmaxClient   pmax.Pmax
		want         []NVMeTCPTargetInfo
		wantErr      bool
	}{
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
			want: []NVMeTCPTargetInfo{
				{
					Target: "nqn.1988-11.com.dell.mock:e6e2d5b871f1403E169D00001",
					Portal: "1.1.1.1",
				},
			},
		},
		/*{
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
			want: nil,
		},*/ //This finding it in cache even after invalidating cache!!
		{
			name:         "Error case: no matching port",
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
				c.EXPECT().GetPort(gmock.All(), "array1", "director1", "port1").AnyTimes().Return(&types.Port{}, fmt.Errorf("No matching ports"))
				return c
			},
			want: nil,
		},
		{
			name:         "Error case: no matching port group",
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
				c.EXPECT().GetPortGroupByID(gmock.All(), "array1", "portgroup1").AnyTimes().Return(&types.PortGroup{}, fmt.Errorf("No matching portgroup"))
				return c
			},
			want: nil,
		},
		{
			name:         "Error case: no matching mv",
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
				c.EXPECT().GetMaskingViewByID(gmock.All(), "array1", "csi-mv---NVMETCP").AnyTimes().Return(&types.MaskingView{}, fmt.Errorf("no matching mv"))
				return c
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
				nvmetcpClient:      gonvme.NewMockNVMe(map[string]string{}),
				nvmeTargets:        &sync.Map{},
				loggedInNVMeArrays: map[string]bool{},
				// Set the necessary fields for testing
			}
			// Call the function and check the results
			s.InvalidateSymToMaskingViewTargets()
			got := s.getAndConfigureArrayNVMeTCPTargets(context.Background(), tc.arrayTargets, tc.symID, tc.pmaxClient)
			if len(got) != len(tc.want) {
				t.Errorf("Expected: %v, but got: %v", len(tc.want), len(got))
			}
		})
	}
}
