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
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dell/csmlog"
	"github.com/dell/gonvme"
	pmax "github.com/dell/gopowermax/v2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// NVMeTCPConnModeDriver is the default NVMe/TCP connectivity mode. The driver
	// discovers targets and establishes fabric sessions itself, which is the
	// behaviour of every release before host-managed mode existed.
	NVMeTCPConnModeDriver = "driver"

	// NVMeTCPConnModeHost places ownership of NVMe/TCP fabric sessions with the
	// host. The driver establishes no session and instead uses the sessions the
	// host has already created, failing the operation when none is eligible.
	NVMeTCPConnModeHost = "host"

	// NVMeTCPConnModeDefault is the mode applied when the environment variable is
	// unset, keeping upgrades zero-touch.
	NVMeTCPConnModeDefault = NVMeTCPConnModeDriver

	// nvmeTCPDefaultPortSuffix is appended to a bare IPv4 portal before comparing
	// it with a session portal. gonvme reports IPv4 session portals as
	// "address:port" and IPv6 session portals bare, and gobrick applies the same
	// rule, so both sides of the comparison must normalize identically.
	nvmeTCPDefaultPortSuffix = ":4420"

	// nvmeTCPDegradedWarningInterval bounds how often a degraded path is reported
	// for one (array, target NQN) pair. A node retries staging every few seconds
	// while a path is down, and an unbounded warning would bury every other line
	// in the log. Fixed rather than configurable: the ER treats the interval as a
	// supportability constant, not a tuning knob.
	nvmeTCPDegradedWarningInterval = 15 * time.Minute

	// nvmeTCPSessionCountWarnThreshold is the per-node session count above which
	// the driver warns. It is advisory only — no operation is refused because of
	// it, since the host, not the driver, decides how many sessions to establish.
	nvmeTCPSessionCountWarnThreshold = 64
)

// nvmeTCPNow is the clock used for degraded-warning rate limiting, replaced in
// tests so the 15-minute window can be exercised without waiting.
var nvmeTCPNow = time.Now

// nvmeTCPConnectivityTracker remembers, per (array, target NQN), when a degraded
// path was last reported and whether it is currently degraded. It exists so that
// the driver can say "this path came back" — which needs memory of the failure —
// while keeping the failure itself from flooding the log.
type nvmeTCPConnectivityTracker struct {
	mu         sync.Mutex
	lastWarned map[string]time.Time
	degraded   map[string]bool
}

func newNVMeTCPConnectivityTracker() *nvmeTCPConnectivityTracker {
	return &nvmeTCPConnectivityTracker{
		lastWarned: map[string]time.Time{},
		degraded:   map[string]bool{},
	}
}

// nvmeTCPConnectivity is process-wide because the rate limit is per node, not
// per operation: every staging attempt on this node shares one warning budget.
var nvmeTCPConnectivity = newNVMeTCPConnectivityTracker()

func nvmeTCPConnectivityKey(array, targetNQN string) string {
	return array + "\x00" + targetNQN
}

// shouldWarnDegraded reports whether a degraded-path warning is due for this
// (array, target NQN) pair, and records the decision.
func (t *nvmeTCPConnectivityTracker) shouldWarnDegraded(array, targetNQN string, now time.Time) bool {
	key := nvmeTCPConnectivityKey(array, targetNQN)

	t.mu.Lock()
	defer t.mu.Unlock()
	t.degraded[key] = true

	last, seen := t.lastWarned[key]
	if seen && now.Sub(last) < nvmeTCPDegradedWarningInterval {
		return false
	}
	t.lastWarned[key] = now
	return true
}

// markRestored reports whether this path has just recovered, which is true only
// on the first healthy observation after a degraded one. Clearing lastWarned
// means a later failure is reported immediately rather than waiting out the
// window from the previous episode — an operator watching a flapping path wants
// to see each episode begin.
func (t *nvmeTCPConnectivityTracker) markRestored(array, targetNQN string) bool {
	key := nvmeTCPConnectivityKey(array, targetNQN)

	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.degraded[key] {
		return false
	}
	delete(t.degraded, key)
	delete(t.lastWarned, key)
	return true
}

// nvmeTCPSessionCountExceedsThreshold reports whether the node carries more
// sessions than the advisory threshold.
func nvmeTCPSessionCountExceedsThreshold(count int) bool {
	return count > nvmeTCPSessionCountWarnThreshold
}

// hostManagedMissingSessionError reports that the host has not established a
// usable session. FailedPrecondition, not Internal: nothing is broken in the
// driver, a precondition the host owns is simply not met yet, and the CSI retry
// will succeed once host automation catches up (FR-9).
func hostManagedMissingSessionError(array string, targets []NVMeTCPTargetInfo) error {
	nqns := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, target := range targets {
		if _, ok := seen[target.Target]; ok {
			continue
		}
		seen[target.Target] = struct{}{}
		nqns = append(nqns, target.Target)
	}
	return status.Errorf(codes.FailedPrecondition,
		"host-managed NVMe/TCP: no eligible session to array %s; the host has established no live "+
			"NVMe/TCP session to any expected target (%s). Verify the host connectivity automation ran on this node",
		array, strings.Join(nqns, ", "))
}

// hostManagedDeviceNotVisibleError reports that a usable session exists but the
// volume's device did not appear on it. This is the other half of AC-5: the
// operator needs to tell "the host never connected" apart from "the host is
// connected but the array is not presenting this volume", because the two have
// different owners and different fixes.
func hostManagedDeviceNotVisibleError(array, wwn string, cause error) error {
	return status.Errorf(codes.FailedPrecondition,
		"host-managed NVMe/TCP: device for volume %s is not visible on array %s over the existing "+
			"host session; the session is live but the device did not appear: %v",
		wwn, array, cause)
}

// resolveNVMeTCPConnMode validates the configured NVMe/TCP connectivity mode,
// defaulting to "driver" when unset so that deployments which never set the
// parameter keep their current behaviour (FR-1, FR-2).
//
// The mode carries no platform qualification. The driver only observes sessions,
// and how the host established them is the same question on OpenShift as on any
// other Kubernetes distribution, so no platform check gates this value (FR-13).
func resolveNVMeTCPConnMode(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return NVMeTCPConnModeDefault, nil
	}
	switch mode := strings.ToLower(trimmed); mode {
	case NVMeTCPConnModeDriver, NVMeTCPConnModeHost:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid nvmeTcpConnMode %q for %s; valid values are %q and %q",
			value, EnvNVMeTCPConnMode, NVMeTCPConnModeDriver, NVMeTCPConnModeHost)
	}
}

// isNVMeTCPHostManaged reports whether the host owns NVMe/TCP fabric sessions for
// this deployment. The mode is deployment-wide: it applies to every array in
// X_CSI_MANAGED_ARRAYS, with no per-array override (FR-3).
func (s *service) isNVMeTCPHostManaged() bool {
	return s.opts.NVMeTCPConnMode == NVMeTCPConnModeHost
}

// normalizeNVMeTCPPortal renders a portal in the form gonvme reports for a live
// session: IPv4 addresses carry the port, IPv6 addresses stay bare. A portal that
// already contains ":" is returned untouched, which covers both an IPv4 portal
// that already names its port and every IPv6 form.
func normalizeNVMeTCPPortal(portal string) string {
	if portal == "" || strings.Contains(portal, ":") {
		return portal
	}
	return portal + nvmeTCPDefaultPortSuffix
}

// nvmeTCPSessionMatchesTarget reports whether an existing host session is eligible
// to carry traffic for the given target (FR-5).
//
// All four conditions must hold: the session is live, it is NVMe/TCP, its target
// NQN matches, and its normalized portal matches including the port. A partial
// match is rejected — a session to the right NQN on the wrong portal is a session
// to a different path, and using it would silently change which fabric path the
// volume travels.
//
// The NQN comparison is prefix-aware, not strict equality: `nvme list-subsys`
// reports the base subsystem NQN (e.g. "...:000120002337"), while Unisphere's
// GetNVMeTCPTargets returns per-port target identifiers that append ":<dir><port>"
// (e.g. "...:000120002337:OR1C112"). nvmeTargetMatchesPublishIdentifier applies the
// same relationship the driver-managed path already relies on, and its trailing
// ":" guard keeps a different subsystem that shares a textual prefix from matching.
func nvmeTCPSessionMatchesTarget(session gonvme.NVMESession, target NVMeTCPTargetInfo) bool {
	if session.NVMESessionState != gonvme.NVMESessionStateLive {
		return false
	}
	if string(session.NVMETransportName) != gonvme.NVMeTransportTypeTCP {
		return false
	}
	if !nvmeTargetMatchesPublishIdentifier(target.Target, session.Target) {
		return false
	}
	return normalizeNVMeTCPPortal(session.Portal) == normalizeNVMeTCPPortal(target.Portal)
}

// getEligibleNVMeTCPSessions returns the host-established sessions that satisfy
// nvmeTCPSessionMatchesTarget for at least one of the expected targets.
//
// An empty result is not an error: it means the host has not established the
// session yet, which the caller reports as a precondition failure so that the
// CSI retry can succeed once host automation catches up.
func (s *service) getEligibleNVMeTCPSessions(ctx context.Context, targets []NVMeTCPTargetInfo) ([]gonvme.NVMESession, error) {
	sessions, err := s.nvmetcpClient.GetSessions()
	if err != nil {
		csmlog.WithContext(ctx).Errorf("host-managed NVMe/TCP: unable to read host sessions: %s", err.Error())
		return nil, err
	}

	if nvmeTCPSessionCountExceedsThreshold(len(sessions)) {
		// Advisory only. The host decides how many sessions to establish, so the
		// driver reports the count and continues rather than refusing to work.
		csmlog.WithContext(ctx).Warnf(
			"host-managed NVMe/TCP: node carries %d NVMe/TCP sessions, above the advisory threshold of %d; "+
				"no limit is enforced", len(sessions), nvmeTCPSessionCountWarnThreshold,
		)
	}

	var eligible []gonvme.NVMESession
	for _, session := range sessions {
		for _, target := range targets {
			if nvmeTCPSessionMatchesTarget(session, target) {
				eligible = append(eligible, session)
				break
			}
		}
	}
	return eligible, nil
}

// expectedNVMeTCPTargets returns the targets the array expects the host to be
// connected to, reusing the existing symToAllNVMeTCPTargets cache and its
// invalidate-on-miss behaviour (FR-6). On a cache miss the lookup reaches
// Unisphere, and if Unisphere is unreachable the error is returned so the
// operation fails rather than proceeding on incomplete information (FR-7).
func (s *service) expectedNVMeTCPTargets(ctx context.Context, array string, pmaxClient pmax.Pmax) ([]NVMeTCPTargetInfo, error) {
	return s.getNVMeTCPTargets(ctx, array, pmaxClient)
}

func (s *service) expectedHostManagedNVMeTCPTargets(ctx context.Context, array string, pmaxClient pmax.Pmax) ([]NVMeTCPTargetInfo, error) {
	expected, err := s.expectedNVMeTCPTargets(ctx, array, pmaxClient)
	if err != nil || len(s.opts.PortGroups) == 0 {
		return expected, err
	}

	portals, err := getIPInterfaces(ctx, array, s.opts.PortGroups, pmaxClient)
	if err != nil {
		return nil, err
	}

	scoped := make([]NVMeTCPTargetInfo, 0, len(expected))
	for _, target := range expected {
		if _, ok := portals[target.Portal]; ok {
			scoped = append(scoped, target)
		}
	}
	if len(scoped) == 0 {
		return nil, fmt.Errorf("no NVMe targets for symid %s on the configured portals", array)
	}
	return scoped, nil
}

// hostManagedNVMeTCPTargetsForPortals answers the question discovery would have
// answered — which targets are reachable on these portals — using the array's own
// view instead of an NVMe fabric operation.
//
// The expected targets come from the same Unisphere-backed cache the rest of the
// host-managed path uses, filtered to the requested portals so the result stays
// scoped to the configured port groups exactly as discovery was.
func (s *service) hostManagedNVMeTCPTargetsForPortals(ctx context.Context, symID string, portals map[string]int32, pmaxClient pmax.Pmax) ([]gonvme.NVMeTarget, error) {
	expected, err := s.expectedHostManagedNVMeTCPTargets(ctx, symID, pmaxClient)
	if err != nil {
		return nil, err
	}

	var targets []gonvme.NVMeTarget
	seen := make(map[string]struct{})
	for _, target := range expected {
		if _, ok := portals[target.Portal]; !ok {
			continue
		}
		key := target.Target + "\x00" + target.Portal
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		targets = append(targets, gonvme.NVMeTarget{
			TargetNqn: target.Target,
			Portal:    target.Portal,
		})
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no NVMe targets for symid %s on the configured portals", symID)
	}
	csmlog.WithContext(ctx).Infof(
		"host-managed NVMe/TCP: array %s reports %d target(s) on the configured portals; discovery skipped",
		symID, len(targets),
	)
	return targets, nil
}

// adoptHostManagedNVMeTCPSessions records the arrays and targets reachable over
// host-established sessions, without creating, replacing or terminating any
// session (FR-14, NFR-6). Sessions the driver created in an earlier
// driver-managed run are indistinguishable from host-created ones here, which is
// exactly the adoption behaviour the ER requires.
func (s *service) adoptHostManagedNVMeTCPSessions(ctx context.Context, array string, targets []NVMeTCPTargetInfo) error {
	// Routed through the evaluating variant so that login-time observations feed
	// the degraded and restored reporting, the same as staging-time ones.
	eligible, err := s.evaluateHostManagedNVMeTCPTargets(ctx, array, targets)
	if err != nil {
		return err
	}
	if len(eligible) == 0 {
		return fmt.Errorf("no eligible host-managed NVMe/TCP session for array %s; "+
			"the host has not established a live session to any expected target", array)
	}

	for _, session := range eligible {
		nvmeTgts, ok := s.nvmeTargets.Load(array)
		if !ok {
			nvmeTgts = []string{}
		}
		s.nvmeTargets.Store(array, append(nvmeTgts.([]string), session.Target))
		csmlog.WithContext(ctx).Infof(
			"host-managed NVMe/TCP: using existing session to target %s on portal %s (source address %q)",
			session.Target, session.Portal, session.SourceAddr,
		)
	}
	s.UpdateLoggedInNVMeArrays(array, true)
	csmlog.WithContext(ctx).Infof("host-managed NVMe/TCP: array %s reachable over %d existing session(s)",
		array, len(eligible))
	return nil
}

// evaluateHostManagedNVMeTCPTargets returns the eligible sessions for the given
// targets and, as a side effect, keeps the operator informed: each target NQN
// without a live session produces a rate-limited degraded warning, and each one
// that recovers produces a single restored message (FR-10, FR-11).
//
// Degradation is tracked per target NQN rather than per portal because a target
// reachable on one portal and not another is still serving I/O; what an operator
// needs to know is that the path count dropped.
func (s *service) evaluateHostManagedNVMeTCPTargets(ctx context.Context, array string, targets []NVMeTCPTargetInfo) ([]gonvme.NVMESession, error) {
	eligible, err := s.getEligibleNVMeTCPSessions(ctx, targets)
	if err != nil {
		return nil, err
	}

	reported := map[string]struct{}{}
	for _, target := range targets {
		if _, done := reported[target.Target]; done {
			continue
		}
		reported[target.Target] = struct{}{}

		var session gonvme.NVMESession
		matched := false
		for _, candidate := range eligible {
			if nvmeTCPSessionMatchesTarget(candidate, target) {
				session = candidate
				matched = true
				break
			}
		}
		if !matched {
			if nvmeTCPConnectivity.shouldWarnDegraded(array, target.Target, nvmeTCPNow()) {
				csmlog.WithContext(ctx).Warnf(
					"host-managed NVMe/TCP: degraded connectivity to array %s target %s; "+
						"no live host session on the expected portals", array, target.Target,
				)
			}
			continue
		}
		if nvmeTCPConnectivity.markRestored(array, target.Target) {
			csmlog.WithContext(ctx).Infof(
				"host-managed NVMe/TCP: connectivity restored to array %s target %s on portal %s (source address %q)",
				array, target.Target, session.Portal, session.SourceAddr,
			)
		}
	}
	return eligible, nil
}

// verifyHostManagedNVMeTCPMetro enforces the Metro rule: a Metro volume is only
// usable when the host has eligible sessions to both arrays (FR-8).
//
// Staging a Metro volume with one side unreachable would appear to succeed and
// silently leave the workload without its second leg, so the operation fails
// instead, naming the array that cannot be reached.
func (s *service) verifyHostManagedNVMeTCPMetro(ctx context.Context, localArray, remoteArray string, pmaxClient pmax.Pmax) error {
	for _, array := range []string{localArray, remoteArray} {
		if array == "" {
			continue
		}
		targets, err := s.expectedHostManagedNVMeTCPTargets(ctx, array, pmaxClient)
		if err != nil {
			return err
		}
		eligible, err := s.evaluateHostManagedNVMeTCPTargets(ctx, array, targets)
		if err != nil {
			return err
		}
		if len(eligible) == 0 {
			csmlog.WithContext(ctx).Errorf(
				"host-managed NVMe/TCP: Metro volume requires eligible sessions to both arrays; array %s is unreachable",
				array,
			)
			return hostManagedMissingSessionError(array, targets)
		}
	}
	return nil
}

// hostManagedNVMeTCPArrayReachable reports whether the node can reach the array
// over an existing host session. Topology generation uses it in place of the
// discovery-and-connect probe, which host-managed mode forbids.
func (s *service) hostManagedNVMeTCPArrayReachable(ctx context.Context, array string, pmaxClient pmax.Pmax) bool {
	targets, err := s.expectedHostManagedNVMeTCPTargets(ctx, array, pmaxClient)
	if err != nil {
		csmlog.WithContext(ctx).Errorf("host-managed NVMe/TCP: unable to determine expected targets for array %s: %s",
			array, err.Error())
		return false
	}
	eligible, err := s.getEligibleNVMeTCPSessions(ctx, targets)
	if err != nil {
		return false
	}
	return len(eligible) > 0
}
