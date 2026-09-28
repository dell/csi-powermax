/*
 *
 * Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *      http://www.apache.org/licenses/LICENSE-2.0
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix/mocks"
	"github.com/dell/gobrick"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
	gmock "github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestResolveNVMeTCPConnMode covers FR-1, FR-2 and AC-3: the mode defaults to
// "driver" when unset, accepts "driver" and "host" in any case, and rejects
// anything else with an error naming both accepted values.
func TestResolveNVMeTCPConnMode(t *testing.T) {
	tests := []struct {
		value       string
		expectMode  string
		expectError string
	}{
		{value: "", expectMode: NVMeTCPConnModeDriver},
		{value: "driver", expectMode: NVMeTCPConnModeDriver},
		{value: "host", expectMode: NVMeTCPConnModeHost},
		{value: "HOST", expectMode: NVMeTCPConnModeHost},
		{value: "Driver", expectMode: NVMeTCPConnModeDriver},
		{value: " host ", expectMode: NVMeTCPConnModeHost},
		{value: "hostmanaged", expectError: "invalid nvmeTcpConnMode"},
		{value: "true", expectError: "invalid nvmeTcpConnMode"},
	}
	for _, tc := range tests {
		t.Run("mode="+tc.value, func(t *testing.T) {
			mode, err := resolveNVMeTCPConnMode(tc.value)
			if tc.expectError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				assert.Contains(t, err.Error(), EnvNVMeTCPConnMode)
				assert.Contains(t, err.Error(), NVMeTCPConnModeDriver)
				assert.Contains(t, err.Error(), NVMeTCPConnModeHost)
				assert.Empty(t, mode)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectMode, mode)
		})
	}
}

// TestNVMeTCPConnModeDefaultPreservesDriverBehavior covers AC-1: a service with no
// mode configured must report driver-managed connectivity, so an upgrade that never
// sets the parameter behaves exactly as before.
func TestNVMeTCPConnModeDefaultPreservesDriverBehavior(t *testing.T) {
	s := &service{}
	assert.False(t, s.isNVMeTCPHostManaged(),
		"an unset mode must not enable host-managed connectivity")

	mode, err := resolveNVMeTCPConnMode("")
	assert.NoError(t, err)
	s.opts.NVMeTCPConnMode = mode
	assert.False(t, s.isNVMeTCPHostManaged(),
		"the resolved default must keep driver-managed connectivity")
}

// TestIsNVMeTCPHostManaged covers FR-3: the mode is read from a single
// deployment-wide option, with no per-array override.
func TestIsNVMeTCPHostManaged(t *testing.T) {
	tests := []struct {
		mode   string
		expect bool
	}{
		{mode: "", expect: false},
		{mode: NVMeTCPConnModeDriver, expect: false},
		{mode: NVMeTCPConnModeHost, expect: true},
	}
	for _, tc := range tests {
		t.Run("mode="+tc.mode, func(t *testing.T) {
			s := &service{}
			s.opts.NVMeTCPConnMode = tc.mode
			assert.Equal(t, tc.expect, s.isNVMeTCPHostManaged())
		})
	}
}

// TestNVMeTCPConnModeIsPlatformAgnostic covers FR-13 and AC-15. The story supersedes
// ER Q25: host-managed mode carries no platform gate, so resolution and the
// host-managed predicate behave identically whatever the platform looks like. The
// test asserts the resolver depends on nothing but its argument.
func TestNVMeTCPConnModeIsPlatformAgnostic(t *testing.T) {
	// Values that would distinguish an OpenShift cluster from any other Kubernetes
	// distribution are deliberately absent: the resolver takes no such input.
	for _, value := range []string{"", NVMeTCPConnModeDriver, NVMeTCPConnModeHost} {
		first, errFirst := resolveNVMeTCPConnMode(value)
		second, errSecond := resolveNVMeTCPConnMode(value)
		assert.NoError(t, errFirst)
		assert.NoError(t, errSecond)
		assert.Equal(t, first, second,
			"resolution must be a pure function of the configured value")
	}

	s := &service{}
	s.opts.NVMeTCPConnMode = NVMeTCPConnModeHost
	assert.True(t, s.isNVMeTCPHostManaged(),
		"host-managed mode must be accepted without any platform qualification")
}

// TestNVMeTCPSessionMatchesTarget covers FR-5 and AC-5: a session is eligible only
// when state, transport, target NQN and normalized portal all match. Every partial
// match is rejected.
func TestNVMeTCPSessionMatchesTarget(t *testing.T) {
	const nqn = "nqn.1988-11.com.dell:mock:00:array1"
	live := func(target, portal string) gonvme.NVMESession {
		return gonvme.NVMESession{
			Target:            target,
			Portal:            portal,
			NVMESessionState:  gonvme.NVMESessionStateLive,
			NVMETransportName: gonvme.NVMeTransportTypeTCP,
		}
	}

	tests := []struct {
		name    string
		session gonvme.NVMESession
		target  NVMeTCPTargetInfo
		expect  bool
	}{
		{
			name:    "live tcp session matching NQN and portal",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.1"},
			expect:  true,
		},
		{
			name:    "target portal already carries the default port",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.1:4420"},
			expect:  true,
		},
		{
			name:    "IPv6 portals match in bare form",
			session: live(nqn, "2001:db8::1"),
			target:  NVMeTCPTargetInfo{Target: nqn, Portal: "2001:db8::1"},
			expect:  true,
		},
		{
			// The host reports the base subsystem NQN from nvme list-subsys, while
			// Unisphere returns per-port target identifiers that append ":<dir><port>".
			// The session is eligible when the expected target is that subsystem on a
			// specific port.
			name:    "session subsystem NQN matches per-port target identifier",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn + ":OR1C112", Portal: "10.0.0.1"},
			expect:  true,
		},
		{
			// The ":" separator guard prevents a different subsystem that merely
			// shares a textual prefix from being accepted.
			name:    "subsystem NQN that is only a textual prefix is rejected",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn + "37:OR1C112", Portal: "10.0.0.1"},
			expect:  false,
		},
		{
			name:    "NQN matches but portal does not",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.2"},
			expect:  false,
		},
		{
			name:    "portal matches but NQN does not",
			session: live(nqn, "10.0.0.1:4420"),
			target:  NVMeTCPTargetInfo{Target: nqn + "-other", Portal: "10.0.0.1"},
			expect:  false,
		},
		{
			name:    "same address but a different port",
			session: live(nqn, "10.0.0.1:4421"),
			target:  NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.1"},
			expect:  false,
		},
		{
			name: "transport is not tcp",
			session: gonvme.NVMESession{
				Target:            nqn,
				Portal:            "10.0.0.1:4420",
				NVMESessionState:  gonvme.NVMESessionStateLive,
				NVMETransportName: gonvme.NVMeTransportTypeFC,
			},
			target: NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.1"},
			expect: false,
		},
		{
			name: "session is not live",
			session: gonvme.NVMESession{
				Target:            nqn,
				Portal:            "10.0.0.1:4420",
				NVMESessionState:  gonvme.NVMESessionStateDeleting,
				NVMETransportName: gonvme.NVMeTransportTypeTCP,
			},
			target: NVMeTCPTargetInfo{Target: nqn, Portal: "10.0.0.1"},
			expect: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expect, nvmeTCPSessionMatchesTarget(tc.session, tc.target))
		})
	}
}

// TestGetEligibleNVMeTCPSessions covers FR-5 session selection and the
// GetSessions failure path.
func TestGetEligibleNVMeTCPSessions(t *testing.T) {
	const nqn = "nqn.1988-11.com.dell:mock:00:array1"
	targets := []NVMeTCPTargetInfo{
		{Target: nqn, Portal: "10.0.0.1"},
		{Target: nqn, Portal: "10.0.0.2"},
	}

	t.Run("returns only the sessions matching a target", func(t *testing.T) {
		s := &service{nvmetcpClient: &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) {
				return []gonvme.NVMESession{
					{
						Target: nqn, Portal: "10.0.0.1:4420",
						NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
					},
					{
						Target: nqn, Portal: "10.0.0.9:4420",
						NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
					},
				}, nil
			},
		}}
		sessions, err := s.getEligibleNVMeTCPSessions(context.Background(), targets)
		assert.NoError(t, err)
		assert.Len(t, sessions, 1)
		assert.Equal(t, "10.0.0.1:4420", sessions[0].Portal)
	})

	t.Run("no eligible session yields an empty result, not an error", func(t *testing.T) {
		s := &service{nvmetcpClient: &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, nil },
		}}
		sessions, err := s.getEligibleNVMeTCPSessions(context.Background(), targets)
		assert.NoError(t, err)
		assert.Empty(t, sessions)
	})

	t.Run("a GetSessions failure is reported", func(t *testing.T) {
		s := &service{nvmetcpClient: &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, errors.New("nvme-cli unavailable") },
		}}
		_, err := s.getEligibleNVMeTCPSessions(context.Background(), targets)
		assert.Error(t, err)
	})
}

// TestLoginIntoNVMeTCPTargetsHostManaged covers FR-4 and AC-2: in host-managed mode
// the login path issues no connect call, and adopts an eligible existing session.
func TestLoginIntoNVMeTCPTargetsHostManaged(t *testing.T) {
	const (
		array = "000000000001"
		nqn   = "nqn.1988-11.com.dell:mock:00:array1"
	)
	mvTargets := []maskingViewNVMeTargetInfo{
		{target: gonvme.NVMeTarget{TargetNqn: nqn, Portal: "10.0.0.1"}},
	}

	t.Run("eligible session is adopted without connecting", func(t *testing.T) {
		connects := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getConnectError: func(_ gonvme.NVMeTarget) error {
				connects++
				return nil
			},
			getSessions: func() ([]gonvme.NVMESession, error) {
				return []gonvme.NVMESession{{
					Target: nqn, Portal: "10.0.0.1:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				}}, nil
			},
		})

		err := s.loginIntoNVMeTCPTargets(array, mvTargets)
		assert.NoError(t, err)
		assert.Zero(t, connects, "host-managed mode must not issue a connect")
		isLoggedIn, ok := s.GetLoggedInNVMeArrays(array)
		assert.True(t, ok)
		assert.True(t, isLoggedIn, "an adopted session marks the array as connected")
	})

	t.Run("no eligible session fails without connecting", func(t *testing.T) {
		connects := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getConnectError: func(_ gonvme.NVMeTarget) error {
				connects++
				return nil
			},
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, nil },
		})

		err := s.loginIntoNVMeTCPTargets(array, mvTargets)
		assert.Error(t, err)
		assert.Zero(t, connects, "a missing session must not trigger a connect attempt")
		isLoggedIn, _ := s.GetLoggedInNVMeArrays(array)
		assert.False(t, isLoggedIn)
	})

	t.Run("driver mode still connects", func(t *testing.T) {
		connects := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeDriver, &nvmeClientMock{
			getConnectError: func(_ gonvme.NVMeTarget) error {
				connects++
				return nil
			},
		})

		err := s.loginIntoNVMeTCPTargets(array, mvTargets)
		assert.NoError(t, err)
		assert.Equal(t, 1, connects, "driver-managed behavior must be unchanged")
	})
}

// TestSetupNVMeTCPTargetDiscoveryHostManaged covers FR-4 and FR-6: the discovery
// path issues no discovery and no connect in host-managed mode, and takes its
// expected targets from the existing symToAllNVMeTCPTargets cache.
func TestSetupNVMeTCPTargetDiscoveryHostManaged(t *testing.T) {
	const (
		array = "000000000002"
		nqn   = "nqn.1988-11.com.dell:mock:00:array2"
	)
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.5"}})
	defer symToAllNVMeTCPTargets.Delete(array)

	t.Run("eligible session satisfies discovery with no fabric calls", func(t *testing.T) {
		connects, discoveries := 0, 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getConnectError: func(_ gonvme.NVMeTarget) error {
				connects++
				return nil
			},
			discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
				discoveries++
				return nil, nil
			},
			getSessions: func() ([]gonvme.NVMESession, error) {
				return []gonvme.NVMESession{{
					Target: nqn, Portal: "10.0.0.5:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				}}, nil
			},
		})

		err := s.setupNVMeTCPTargetDiscovery(context.Background(), array, nil)
		assert.NoError(t, err)
		assert.Zero(t, connects, "host-managed mode must not connect")
		assert.Zero(t, discoveries, "host-managed mode must not run discovery")
	})

	t.Run("no eligible session fails without fabric calls", func(t *testing.T) {
		connects, discoveries := 0, 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getConnectError: func(_ gonvme.NVMeTarget) error {
				connects++
				return nil
			},
			discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
				discoveries++
				return nil, nil
			},
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, nil },
		})

		err := s.setupNVMeTCPTargetDiscovery(context.Background(), array, nil)
		assert.Error(t, err)
		assert.Zero(t, connects)
		assert.Zero(t, discoveries)
	})
}

func TestSetupNVMeTCPTargetDiscoveryHostManagedScopesToPortGroups(t *testing.T) {
	const array = "000000000018"
	inScopeNQN := "nqn.1988-11.com.dell:mock:00:array18:OR1C112"
	outOfScopeNQN := "nqn.1988-11.com.dell:mock:00:array18:OR1C113"
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{
		{Target: inScopeNQN, Portal: "10.0.0.5"},
		{Target: outOfScopeNQN, Portal: "10.0.0.6"},
	})
	defer symToAllNVMeTCPTargets.Delete(array)

	originalGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{"10.0.0.5": 4420}, nil
	}
	defer func() { getIPInterfaces = originalGetIPInterfaces }()

	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) {
			return []gonvme.NVMESession{
				{Target: inScopeNQN, Portal: "10.0.0.5:4420", NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP},
				{Target: outOfScopeNQN, Portal: "10.0.0.6:4420", NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP},
			}, nil
		},
	})
	s.opts.PortGroups = []string{"pg1"}

	err := s.setupNVMeTCPTargetDiscovery(context.Background(), array, nil)
	assert.NoError(t, err)
	targets, ok := s.nvmeTargets.Load(array)
	assert.True(t, ok)
	assert.Equal(t, []string{inScopeNQN}, targets)
}

func TestExpectedHostManagedNVMeTCPTargetsScopesToPortGroups(t *testing.T) {
	const array = "000000000019"
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{
		{Target: "nqn.test:in-scope", Portal: "10.0.0.7"},
		{Target: "nqn.test:out-of-scope", Portal: "10.0.0.8"},
	})
	defer symToAllNVMeTCPTargets.Delete(array)

	originalGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{"10.0.0.7": 4420}, nil
	}
	defer func() { getIPInterfaces = originalGetIPInterfaces }()

	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{})
	s.opts.PortGroups = []string{"pg1"}
	targets, err := s.expectedHostManagedNVMeTCPTargets(context.Background(), array, nil)
	assert.NoError(t, err)
	assert.Equal(t, []NVMeTCPTargetInfo{{Target: "nqn.test:in-scope", Portal: "10.0.0.7"}}, targets)
}

// TestHostManagedNVMeTCPArrayReachable covers the topology path: array reachability
// is decided by eligible sessions, never by discovery.
func TestHostManagedNVMeTCPArrayReachable(t *testing.T) {
	const (
		array = "000000000003"
		nqn   = "nqn.1988-11.com.dell:mock:00:array3"
	)
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.7"}})
	defer symToAllNVMeTCPTargets.Delete(array)

	eligible := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) {
			return []gonvme.NVMESession{{
				Target: nqn, Portal: "10.0.0.7:4420",
				NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
			}}, nil
		},
	})
	assert.True(t, eligible.hostManagedNVMeTCPArrayReachable(context.Background(), array, nil))

	none := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) { return nil, nil },
	})
	assert.False(t, none.hostManagedNVMeTCPArrayReachable(context.Background(), array, nil))
}

// newTestServiceForNVMeTCPConnMode builds a service wired with the caches the
// NVMe/TCP paths touch, so tests exercise the real login and discovery code.
func newTestServiceForNVMeTCPConnMode(mode string, client gonvme.NVMEinterface) *service {
	s := &service{
		nvmetcpClient:      client,
		nvmeTargets:        new(sync.Map),
		loggedInNVMeArrays: map[string]bool{},
	}
	s.opts.NVMeTCPConnMode = mode
	return s
}

// TestHostManagedNVMeTCPArrayReachableFailurePaths covers the two ways the
// topology probe can fail to prove reachability: Unisphere is unreachable on a
// cache miss (FR-7), and the host session list cannot be read.
func TestHostManagedNVMeTCPArrayReachableFailurePaths(t *testing.T) {
	const (
		array = "000000000004"
		nqn   = "nqn.1988-11.com.dell:mock:00:array4"
	)

	t.Run("Unisphere unreachable on a cache miss", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gmock.NewController(t))
		pmaxClient.EXPECT().GetNVMeTCPTargets(gmock.All(), array).AnyTimes().
			Return(nil, errors.New("unisphere unreachable"))

		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) {
				t.Fatal("sessions must not be read when the expected targets are unknown")
				return nil, nil
			},
		})
		assert.False(t, s.hostManagedNVMeTCPArrayReachable(context.Background(), array, pmaxClient))
	})

	t.Run("host session list cannot be read", func(t *testing.T) {
		symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.8"}})
		defer symToAllNVMeTCPTargets.Delete(array)

		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, errors.New("nvme-cli unavailable") },
		})
		assert.False(t, s.hostManagedNVMeTCPArrayReachable(context.Background(), array, nil))
	})
}

// TestGetNVMeTCPTargetsFromPortalsHostManaged covers FR-4 at the single choke
// point every discovery path funnels through. In host-managed mode the expected
// targets come from the array, filtered to the requested portals, and nvme
// discover is never invoked.
func TestGetNVMeTCPTargetsFromPortalsHostManaged(t *testing.T) {
	const (
		array = "000000000005"
		nqn   = "nqn.1988-11.com.dell:mock:00:array5"
	)
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{
		{Target: nqn, Portal: "10.0.0.1"},
		{Target: nqn, Portal: "10.0.0.2"},
		{Target: nqn, Portal: "10.0.0.3"},
	})
	defer symToAllNVMeTCPTargets.Delete(array)

	// Only two of the three array portals are in the requested port group.
	portals := map[string]int32{"10.0.0.1": 4420, "10.0.0.3": 4420}

	t.Run("returns array targets scoped to the portals, without discovery", func(t *testing.T) {
		discoveries := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
				discoveries++
				return nil, nil
			},
		})

		targets, err := s.getNVMeTCPTargetsFromPortals(context.Background(), array, portals, nil)
		assert.NoError(t, err)
		assert.Zero(t, discoveries, "host-managed mode must never run nvme discover")
		assert.Len(t, targets, 2)
		for _, target := range targets {
			assert.Equal(t, nqn, target.TargetNqn)
			assert.Contains(t, []string{"10.0.0.1", "10.0.0.3"}, target.Portal)
		}
	})

	t.Run("no array target in the requested portals is an error", func(t *testing.T) {
		discoveries := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
				discoveries++
				return nil, nil
			},
		})

		_, err := s.getNVMeTCPTargetsFromPortals(context.Background(), array,
			map[string]int32{"10.9.9.9": 4420}, nil)
		assert.Error(t, err)
		assert.Zero(t, discoveries)
	})

	t.Run("driver mode still discovers", func(t *testing.T) {
		discoveries := 0
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeDriver, &nvmeClientMock{
			discoverTargets: func(portal string) ([]gonvme.NVMeTarget, error) {
				discoveries++
				return []gonvme.NVMeTarget{{TargetNqn: nqn, Portal: portal}}, nil
			},
		})

		targets, err := s.getNVMeTCPTargetsFromPortals(context.Background(), array, portals, nil)
		assert.NoError(t, err)
		assert.Equal(t, 2, discoveries, "driver-managed behavior must be unchanged")
		assert.Len(t, targets, 2)
	})
}

// TestGetNVMeTCPTargetsForMaskingViewHostManagedSkipsDiscovery covers the
// masking-view path, which reaches discovery through the same choke point.
func TestGetNVMeTCPTargetsForMaskingViewHostManagedSkipsDiscovery(t *testing.T) {
	const (
		array = "000000000006"
		nqn   = "nqn.1988-11.com.dell:mock:00:array6"
	)
	symToAllNVMeTCPTargets.Store(array, []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.4"}})
	defer symToAllNVMeTCPTargets.Delete(array)
	defer symToMaskingViewTargets.Delete(array)

	originalGetIPInterfaces := getIPInterfaces
	getIPInterfaces = func(_ context.Context, _ string, _ []string, _ pmax.Pmax) (map[string]int32, error) {
		return map[string]int32{"10.0.0.4": 4420}, nil
	}
	defer func() { getIPInterfaces = originalGetIPInterfaces }()

	discoveries := 0
	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		discoverTargets: func(_ string) ([]gonvme.NVMeTarget, error) {
			discoveries++
			return nil, nil
		},
	})

	targets, err := s.getNVMeTCPTargetsForMaskingView(context.Background(), array,
		&types.MaskingView{MaskingViewID: "mv1", PortGroupID: "pg1"}, nil)
	assert.NoError(t, err)
	assert.Zero(t, discoveries, "the masking-view path must not discover in host-managed mode")
	assert.Len(t, targets, 1)
	assert.Equal(t, nqn, targets[0].target.TargetNqn)
}

// TestHostManagedNVMeTCPErrorClassification covers FR-9 and AC-6: a missing
// session and an invisible device are both FailedPrecondition, carry distinct
// messages, and never surface as Internal.
func TestHostManagedNVMeTCPErrorClassification(t *testing.T) {
	const (
		array = "000000000007"
		nqn   = "nqn.1988-11.com.dell:mock:00:array7"
	)
	targets := []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.1"}}

	missing := hostManagedMissingSessionError(array, targets)
	notVisible := hostManagedDeviceNotVisibleError(array, "60000970000120001965533030394238",
		errors.New("device not found after 30s"))

	for name, err := range map[string]error{"missing session": missing, "device not visible": notVisible} {
		t.Run(name, func(t *testing.T) {
			st, ok := status.FromError(err)
			assert.True(t, ok, "the error must carry a gRPC status")
			assert.Equal(t, codes.FailedPrecondition, st.Code())
			assert.NotEqual(t, codes.Internal, st.Code())
			assert.Contains(t, st.Message(), array, "the operator must see which array failed")
		})
	}

	assert.NotEqual(t, status.Convert(missing).Message(), status.Convert(notVisible).Message(),
		"the two conditions must be distinguishable by message")
	assert.Contains(t, status.Convert(missing).Message(), "no eligible")
	assert.Contains(t, status.Convert(notVisible).Message(), "not visible")

	// FR-16: the host NQN identifies the node and is never logged or returned.
	for _, err := range []error{missing, notVisible} {
		assert.NotContains(t, strings.ToLower(status.Convert(err).Message()), "hostnqn")
	}
}

// TestHostManagedNVMeTCPMetroRequiresBothArrays covers FR-8: a Metro volume whose
// remote array has no eligible session fails, and the error names that array.
func TestHostManagedNVMeTCPMetroRequiresBothArrays(t *testing.T) {
	const (
		localArray  = "000000000008"
		remoteArray = "000000000009"
		localNQN    = "nqn.1988-11.com.dell:mock:00:array8"
		remoteNQN   = "nqn.1988-11.com.dell:mock:00:array9"
	)
	symToAllNVMeTCPTargets.Store(localArray, []NVMeTCPTargetInfo{{Target: localNQN, Portal: "10.0.0.1"}})
	symToAllNVMeTCPTargets.Store(remoteArray, []NVMeTCPTargetInfo{{Target: remoteNQN, Portal: "10.0.0.2"}})
	defer symToAllNVMeTCPTargets.Delete(localArray)
	defer symToAllNVMeTCPTargets.Delete(remoteArray)

	// Only the local array has a live session.
	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) {
			return []gonvme.NVMESession{{
				Target: localNQN, Portal: "10.0.0.1:4420",
				NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
			}}, nil
		},
	})

	err := s.verifyHostManagedNVMeTCPMetro(context.Background(), localArray, remoteArray, nil)
	assert.Error(t, err)
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))
	assert.Contains(t, status.Convert(err).Message(), remoteArray,
		"the error must name the array that cannot be reached")
	assert.NotContains(t, status.Convert(err).Message(), localArray+" ",
		"the reachable array must not be reported as the failure")

	// Both sides eligible: the check passes.
	both := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) {
			return []gonvme.NVMESession{
				{
					Target: localNQN, Portal: "10.0.0.1:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				},
				{
					Target: remoteNQN, Portal: "10.0.0.2:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				},
			}, nil
		},
	})
	assert.NoError(t, both.verifyHostManagedNVMeTCPMetro(context.Background(), localArray, remoteArray, nil))
}

// TestNVMeTCPDegradedWarningRateLimit covers FR-10 and AC-8: one warning per
// (array, target NQN) per 15 minutes, and a restored log when the path recovers.
func TestNVMeTCPDegradedWarningRateLimit(t *testing.T) {
	const (
		array = "000000000010"
		nqn   = "nqn.1988-11.com.dell:mock:00:array10"
	)
	tracker := newNVMeTCPConnectivityTracker()
	base := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

	assert.True(t, tracker.shouldWarnDegraded(array, nqn, base),
		"the first degraded observation warns")
	assert.False(t, tracker.shouldWarnDegraded(array, nqn, base.Add(time.Minute)),
		"a repeat inside the window is suppressed")
	assert.False(t, tracker.shouldWarnDegraded(array, nqn, base.Add(14*time.Minute)),
		"still suppressed just before the window closes")
	assert.True(t, tracker.shouldWarnDegraded(array, nqn, base.Add(15*time.Minute)),
		"the window reopens after 15 minutes")

	// A different target NQN keeps its own budget.
	assert.True(t, tracker.shouldWarnDegraded(array, nqn+"-other", base.Add(time.Minute)))
	// So does the same NQN on a different array.
	assert.True(t, tracker.shouldWarnDegraded("000000000011", nqn, base.Add(time.Minute)))

	// Recovery reports once, then stays quiet.
	assert.True(t, tracker.markRestored(array, nqn), "recovery from degraded is reported")
	assert.False(t, tracker.markRestored(array, nqn), "a still-healthy path is not reported again")

	// After recovery the next degradation warns immediately, rather than waiting
	// out the window from the previous episode.
	assert.True(t, tracker.shouldWarnDegraded(array, nqn, base.Add(16*time.Minute)))
}

// TestNVMeTCPSessionCountWarning covers FR-12: a node above 64 sessions is warned
// about, with no hard cap applied.
func TestNVMeTCPSessionCountWarning(t *testing.T) {
	assert.False(t, nvmeTCPSessionCountExceedsThreshold(64), "the threshold itself does not warn")
	assert.True(t, nvmeTCPSessionCountExceedsThreshold(65), "above the threshold warns")

	const nqn = "nqn.1988-11.com.dell:mock:00:array12"
	sessions := make([]gonvme.NVMESession, 0, 100)
	for i := 0; i < 100; i++ {
		sessions = append(sessions, gonvme.NVMESession{
			Target: nqn, Portal: "10.0.0.1:4420",
			NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
		})
	}
	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) { return sessions, nil },
	})

	// No hard cap: all 100 sessions are still usable.
	eligible, err := s.getEligibleNVMeTCPSessions(context.Background(),
		[]NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.1"}})
	assert.NoError(t, err)
	assert.Len(t, eligible, 100, "the warning must not turn into a limit")
}

// stubNVMeTCPConnector records ConnectVolume calls so tests can assert the
// driver reaches — or does not reach — gobrick.
type stubNVMeTCPConnector struct {
	calls  int
	device gobrick.Device
	err    error
}

func (c *stubNVMeTCPConnector) ConnectVolume(_ context.Context, _ gobrick.NVMeVolumeInfo, _ bool) (gobrick.Device, error) {
	c.calls++
	return c.device, c.err
}

func (c *stubNVMeTCPConnector) DisconnectVolumeByDeviceName(_ context.Context, _ string) error {
	return nil
}

func (c *stubNVMeTCPConnector) GetInitiatorName(_ context.Context) ([]string, error) {
	return nil, nil
}

// TestConnectNVMeTCPDeviceHostManaged covers AC-4 and AC-6 on the staging path:
// staging over an eligible session reaches gobrick and creates no session, a
// missing session fails before gobrick is called, and a gobrick failure with a
// live session is reported as device-not-visible.
func TestConnectNVMeTCPDeviceHostManaged(t *testing.T) {
	const (
		array = "000000000013"
		nqn   = "nqn.1988-11.com.dell:mock:00:array13"
		wwn   = "60000970000120001965533030394238"
	)
	data := publishContextData{
		deviceWWN:      wwn,
		array:          array,
		nvmetcpTargets: []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.1"}},
	}
	liveSession := func() ([]gonvme.NVMESession, error) {
		return []gonvme.NVMESession{{
			Target: nqn, Portal: "10.0.0.1:4420",
			NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
		}}, nil
	}

	t.Run("eligible session stages over the existing session", func(t *testing.T) {
		connector := &stubNVMeTCPConnector{device: gobrick.Device{Name: "nvme0n1"}}
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{getSessions: liveSession})
		s.nvmeTCPConnector = connector

		device, err := s.connectNVMeTCPDevice(context.Background(), data)
		assert.NoError(t, err)
		assert.Equal(t, "nvme0n1", device.Name)
		assert.Equal(t, 1, connector.calls)
	})

	t.Run("missing session fails before reaching gobrick", func(t *testing.T) {
		connector := &stubNVMeTCPConnector{}
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, nil },
		})
		s.nvmeTCPConnector = connector

		_, err := s.connectNVMeTCPDevice(context.Background(), data)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "no eligible")
		assert.Zero(t, connector.calls, "staging must not reach gobrick without a session")
	})

	t.Run("live session but no device is reported as device-not-visible", func(t *testing.T) {
		connector := &stubNVMeTCPConnector{err: errors.New("device not found")}
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{getSessions: liveSession})
		s.nvmeTCPConnector = connector

		_, err := s.connectNVMeTCPDevice(context.Background(), data)
		assert.Equal(t, codes.FailedPrecondition, status.Code(err))
		assert.Contains(t, status.Convert(err).Message(), "not visible")
		assert.Equal(t, 1, connector.calls)
	})

	t.Run("driver mode passes the gobrick error through unchanged", func(t *testing.T) {
		cause := errors.New("can't find active nvme session")
		connector := &stubNVMeTCPConnector{err: cause}
		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeDriver, &nvmeClientMock{})
		s.nvmeTCPConnector = connector

		_, err := s.connectNVMeTCPDevice(context.Background(), data)
		assert.Equal(t, cause, err, "driver-managed error handling must be unchanged")
		assert.Equal(t, 1, connector.calls)
	})
}

// TestVerifyHostManagedNVMeTCPMetroFailurePaths covers the error branches of the
// Metro check: Unisphere unreachable for either array, and an unreadable host
// session list. Neither may be reported as Internal.
func TestVerifyHostManagedNVMeTCPMetroFailurePaths(t *testing.T) {
	const (
		localArray  = "000000000014"
		remoteArray = "000000000015"
		localNQN    = "nqn.1988-11.com.dell:mock:00:array14"
	)

	t.Run("Unisphere unreachable for the local array", func(t *testing.T) {
		pmaxClient := mocks.NewMockPmaxClient(gmock.NewController(t))
		pmaxClient.EXPECT().GetNVMeTCPTargets(gmock.All(), gmock.Any()).AnyTimes().
			Return(nil, errors.New("unisphere unreachable"))

		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{})
		err := s.verifyHostManagedNVMeTCPMetro(context.Background(), localArray, remoteArray, pmaxClient)
		assert.Error(t, err)
	})

	t.Run("host session list cannot be read", func(t *testing.T) {
		symToAllNVMeTCPTargets.Store(localArray, []NVMeTCPTargetInfo{{Target: localNQN, Portal: "10.0.0.1"}})
		defer symToAllNVMeTCPTargets.Delete(localArray)

		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) { return nil, errors.New("nvme-cli unavailable") },
		})
		err := s.verifyHostManagedNVMeTCPMetro(context.Background(), localArray, remoteArray, nil)
		assert.Error(t, err)
		assert.NotEqual(t, codes.Internal, status.Code(err))
	})

	t.Run("an empty remote array id is skipped", func(t *testing.T) {
		symToAllNVMeTCPTargets.Store(localArray, []NVMeTCPTargetInfo{{Target: localNQN, Portal: "10.0.0.1"}})
		defer symToAllNVMeTCPTargets.Delete(localArray)

		s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
			getSessions: func() ([]gonvme.NVMESession, error) {
				return []gonvme.NVMESession{{
					Target: localNQN, Portal: "10.0.0.1:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				}}, nil
			},
		})
		assert.NoError(t, s.verifyHostManagedNVMeTCPMetro(context.Background(), localArray, "", nil))
	})
}

func TestEvaluateHostManagedNVMeTCPTargetsMatchesPerPortIdentifiers(t *testing.T) {
	const (
		array        = "000000000017"
		subsystemNQN = "nqn.1988-11.com.dell:PowerMax_2500:00:000120002337"
	)
	targets := []NVMeTCPTargetInfo{
		{Target: subsystemNQN + ":OR1C112", Portal: "192.168.53.91"},
		{Target: subsystemNQN + ":OR1C113", Portal: "192.168.54.91"},
	}

	originalTracker := nvmeTCPConnectivity
	nvmeTCPConnectivity = newNVMeTCPConnectivityTracker()
	defer func() { nvmeTCPConnectivity = originalTracker }()

	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) {
			return []gonvme.NVMESession{
				{
					Target: subsystemNQN, Portal: "192.168.53.91:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				},
				{
					Target: subsystemNQN, Portal: "192.168.54.91:4420",
					NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
				},
			}, nil
		},
	})

	eligible, err := s.evaluateHostManagedNVMeTCPTargets(context.Background(), array, targets)
	assert.NoError(t, err)
	assert.Len(t, eligible, 2)
	for _, target := range targets {
		assert.False(t, nvmeTCPConnectivity.degraded[nvmeTCPConnectivityKey(array, target.Target)],
			"an eligible base subsystem session must not mark the per-port target degraded")
	}
}

// TestEvaluateHostManagedNVMeTCPTargetsReportsDegradedAndRestored covers FR-10
// and FR-11 end to end: a target with no session is reported degraded, and the
// same target reports restored once a session appears.
func TestEvaluateHostManagedNVMeTCPTargetsReportsDegradedAndRestored(t *testing.T) {
	const (
		array = "000000000016"
		nqn   = "nqn.1988-11.com.dell:mock:00:array16"
	)
	targets := []NVMeTCPTargetInfo{{Target: nqn, Portal: "10.0.0.1"}}

	// Isolate this test from the process-wide tracker.
	originalTracker := nvmeTCPConnectivity
	nvmeTCPConnectivity = newNVMeTCPConnectivityTracker()
	defer func() { nvmeTCPConnectivity = originalTracker }()

	var sessions []gonvme.NVMESession
	s := newTestServiceForNVMeTCPConnMode(NVMeTCPConnModeHost, &nvmeClientMock{
		getSessions: func() ([]gonvme.NVMESession, error) { return sessions, nil },
	})

	eligible, err := s.evaluateHostManagedNVMeTCPTargets(context.Background(), array, targets)
	assert.NoError(t, err)
	assert.Empty(t, eligible)
	assert.True(t, nvmeTCPConnectivity.degraded[nvmeTCPConnectivityKey(array, nqn)],
		"the target must be recorded as degraded")

	sessions = []gonvme.NVMESession{{
		Target: nqn, Portal: "10.0.0.1:4420",
		NVMESessionState: gonvme.NVMESessionStateLive, NVMETransportName: gonvme.NVMeTransportTypeTCP,
	}}
	eligible, err = s.evaluateHostManagedNVMeTCPTargets(context.Background(), array, targets)
	assert.NoError(t, err)
	assert.Len(t, eligible, 1)
	assert.False(t, nvmeTCPConnectivity.degraded[nvmeTCPConnectivityKey(array, nqn)],
		"recovery must clear the degraded state so the next failure is reported")
}
