/*
 Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package symmetrix

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/dell/csmlog"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
)

var (
	metroClients       sync.Map
	httpHealthHandlers sync.Map
)

const (
	failoverThreshold    int32         = 5
	failureTimeThreshold time.Duration = 2 * time.Minute
)

// RoundTripperInterface is an interface for http.RoundTripper
//
//go:generate mockgen -destination=mocks/roundtripper.go -package=mocks github.com/dell/csi-powermax/v2/pkg/symmetrix RoundTripperInterface
type RoundTripperInterface interface {
	http.RoundTripper
}

func init() {
	metroClients = sync.Map{}
}

type metroClient struct {
	primaryArray   string
	secondaryArray string
	activeArray    string
	failureCount   int32
	lastFailure    time.Time
	witnessWinner  string // winner designated by CheckMetroState (Witness/Bias); empty = use legacy failover
	mx             sync.Mutex
}

func (m *metroClient) getActiveArray() string {
	m.mx.Lock()
	defer m.mx.Unlock()
	// Prefer Witness/Bias winner designation from CheckMetroState (AC-002/AC-003).
	if m.witnessWinner != "" && (m.witnessWinner == m.primaryArray || m.witnessWinner == m.secondaryArray) {
		if m.activeArray != m.witnessWinner {
			csmlog.Infof("Routing to Witness/Bias winner array: %s (was %s)", m.witnessWinner, m.activeArray)
			m.activeArray = m.witnessWinner
			m.failureCount = 0
		}
		return m.activeArray
	}
	// Legacy failure-count threshold failover when no Witness/Bias winner is set.
	if m.failureCount >= failoverThreshold {
		if m.activeArray == m.primaryArray {
			m.activeArray = m.secondaryArray
		} else {
			m.activeArray = m.primaryArray
		}
		m.failureCount = 0
		csmlog.Infof("Failing over to the array: %s", m.activeArray)
	}
	return m.activeArray
}

func (m *metroClient) setErrorWatcher(powermaxClient pmax.Pmax) {
	client := powermaxClient.GetHTTPClient()
	entry, ok := httpHealthHandlers.Load(client)
	if !ok {
		return
	}
	entry.(*sync.Map).Store(m.getActiveArray(), m.healthHandler)
}

// InstallMetroHealthWatchers installs transport wrappers during client setup,
// before concurrent CSI operations can start using the HTTP clients.
func InstallMetroHealthWatchers(symIDs []string, client pmax.Pmax) {
	for _, symID := range symIDs {
		installHTTPHealthWatcher(symID, client)
	}
}

func installHTTPHealthWatcher(symID string, powermaxClient pmax.Pmax) {
	client := powermaxClient.GetHTTPClient()
	entry, loaded := httpHealthHandlers.LoadOrStore(client, &sync.Map{})
	if loaded {
		return
	}
	client.Transport = &transport{
		RoundTripper: client.Transport,
		healthHandler: func(weight int32) {
			entry.(*sync.Map).Range(func(_, value interface{}) bool {
				value.(func(int32))(weight)
				return true
			})
		},
	}
	_ = symID
}

func (m *metroClient) healthHandler(failureWeight int32) {
	m.mx.Lock()
	defer m.mx.Unlock()
	timeSinceLastFailure := time.Since(m.lastFailure)
	m.lastFailure = time.Now()
	if timeSinceLastFailure > failureTimeThreshold && m.failureCount != 0 {
		csmlog.Infof("Last failure was more than %f minutes ago; reseting the failure count", failureTimeThreshold.Minutes())
		m.failureCount = 1
	} else {
		m.failureCount += failureWeight
	}
}

func (m *metroClient) getPowerMaxClient() (pmax.Pmax, error) {
	powermax, err := getPowerMax(m.getActiveArray())
	if err != nil {
		return nil, err
	}
	client := powermax.getClient()
	m.setErrorWatcher(client)
	return client, nil
}

func (m *metroClient) getIdentifier() string {
	return fmt.Sprintf("%s-%s", m.primaryArray, m.secondaryArray)
}

// setWinner updates the Witness/Bias winner designation. Pass "" to clear.
func (m *metroClient) setWinner(winnerSymID string) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.witnessWinner = winnerSymID
}

// GetMetroPartner returns the partner array ID for the given array ID by
// looking up the registered Metro clients. If the array is not part of any
// known Metro pair the function returns an empty string. This is used by
// metrics helpers to emit per-pair Prometheus labels (WARN-2).
func GetMetroPartner(arrayID string) string {
	partner := ""
	metroClients.Range(func(_, value interface{}) bool {
		mc := value.(*metroClient)
		mc.mx.Lock()
		primary := mc.primaryArray
		secondary := mc.secondaryArray
		mc.mx.Unlock()
		if primary == arrayID {
			partner = secondary
			return false // stop iteration
		}
		if secondary == arrayID {
			partner = primary
			return false // stop iteration
		}
		return true
	})
	return partner
}

// SetMetroWinner updates the Witness/Bias winner designation on the
// metroClient for the given pair of arrays. This is called by
// logMetroStateCheck when CheckMetroState determines a winner, so that
// subsequent CSI operations route to the surviving site (AC-002/AC-003).
// Pass winnerSymID="" to clear the designation (both arrays healthy).
func SetMetroWinner(localSymID, remoteSymID, winnerSymID string) {
	id := fmt.Sprintf("%s-%s", localSymID, remoteSymID)
	if v, ok := metroClients.Load(id); ok {
		v.(*metroClient).setWinner(winnerSymID)
		return
	}
	// Try reversed order.
	id = fmt.Sprintf("%s-%s", remoteSymID, localSymID)
	if v, ok := metroClients.Load(id); ok {
		v.(*metroClient).setWinner(winnerSymID)
	}
}

type transport struct {
	http.RoundTripper
	healthHandler func(int32)
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		t.healthHandler(2)
	} else if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.healthHandler(99)
	} else if int(resp.StatusCode/100) == 5 {
		t.healthHandler(1)
	}
	return resp, err
}

// ErrBothArraysUnreachable is returned when neither the local nor remote
// array can be queried for RDF group state.
var ErrBothArraysUnreachable = errors.New("both local and remote arrays are unreachable")

// ErrNoMetroRDFGroup is returned by CheckMetroState when no SRDF/Metro RDF
// group number can be identified for the given array pair — neither supplied
// by the caller nor resolved via GetRDFGroupList. Callers must treat this as
// "not a Metro volume or Metro not yet configured" rather than a site failure
var ErrNoMetroRDFGroup = errors.New("no SRDF/Metro RDF group found for array pair")

// ErrDeviceBiasR1Failure is returned when Device Bias is configured but the
// R1 (local) side has failed and the bias winner (R2) cannot take effect automatically.
// This is a hard-failure condition requiring manual R1 restoration.
var ErrDeviceBiasR1Failure = errors.New("Device Bias R1-side failure: manual R1 restoration required")

// MetroState captures the result of an SRDF/Metro site-failure state check.
type MetroState struct {
	WinnerSymID         string
	LoserSymID          string
	WitnessConfigured   bool
	WitnessEffective    bool
	BiasConfigured      bool
	BiasEffective       bool
	DeviceBiasR1Failure bool
	LoserUnreachable    bool // true only when the loser array is actually unreachable (remoteErr != nil)
}

// resolveRDFGroupNo returns rdfGroupNo unchanged when non-empty. When empty it
// queries the surviving client (local preferred) for the first Metro-mode RDF
// group associated with the given array pair. This allows callers that do not
// have the RDF group in-hand (e.g. ControllerUnpublishVolume) to still invoke
// CheckMetroState correctly.
func resolveRDFGroupNo(ctx context.Context, localClient, remoteClient PmaxClient, localSymID, remoteSymID, rdfGroupNo string) string {
	if rdfGroupNo != "" {
		return rdfGroupNo
	}
	// Check cache first to avoid full list scan on every call.
	// Cache key is (localSymID, remoteSymID) only — used exclusively when
	// rdfGroupNo is unknown, so there is no ambiguity about which group.
	if cached, ok := globalRDFGroupCache.get(localSymID, remoteSymID, ""); ok {
		return cached
	}
	// Try local first, then remote.
	for _, pair := range []struct {
		client PmaxClient
		symID  string
	}{
		{localClient, localSymID},
		{remoteClient, remoteSymID},
	} {
		if pair.client == nil {
			continue
		}
		list, err := pair.client.GetRDFGroupList(ctx, pair.symID, nil)
		if err != nil || list == nil {
			continue
		}
		for _, g := range list.RDFGroupIDs {
			if g.GroupType == "Metro" || g.GroupType == "METRO" {
				result := fmt.Sprintf("%d", g.RDFGNumber)
				csmlog.Infof("CheckMetroState: resolved rdfGroupNo=%s for %s/%s via list", result, localSymID, remoteSymID)
				globalRDFGroupCache.put(localSymID, remoteSymID, "", result)
				return result
			}
		}
	}
	return ""
}

// CheckMetroState queries the RDF group on both the local and remote arrays
// to determine which side is the current winner after a Metro site failure.
// Winner determination follows FR-1.2: WitnessEffective/BiasEffective fields
// take precedence over connectivity order when both arrays are reachable.
// A nil client is treated the same as an unreachable array.
// If rdfGroupNo is empty, the function auto-resolves it via GetRDFGroupList.
func CheckMetroState(ctx context.Context, localClient, remoteClient PmaxClient, localSymID, remoteSymID, rdfGroupNo string) (*MetroState, error) {
	return CheckMetroStateWithRDFGroups(ctx, localClient, remoteClient, localSymID, remoteSymID, rdfGroupNo, rdfGroupNo)
}

func CheckMetroStateWithRDFGroups(ctx context.Context, localClient, remoteClient PmaxClient, localSymID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo string) (*MetroState, error) {
	return CheckMetroStateWithRDFGroupsAndTimeout(ctx, localClient, remoteClient, localSymID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo, 15*time.Second)
}

// CheckMetroStateWithRDFGroupsAndTimeout checks both RDF groups using the
// supplied timeout for each Unisphere call.
func CheckMetroStateWithRDFGroupsAndTimeout(ctx context.Context, localClient, remoteClient PmaxClient, localSymID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo string, perQueryTimeout time.Duration) (*MetroState, error) {
	localRDFGroupNo = resolveRDFGroupNo(ctx, localClient, remoteClient, localSymID, remoteSymID, localRDFGroupNo)
	if localRDFGroupNo == "" {
		return nil, ErrNoMetroRDFGroup
	}
	if remoteRDFGroupNo == "" {
		remoteRDFGroupNo = localRDFGroupNo
	}

	// Query both arrays concurrently with separately bounded child contexts
	// to ensure one slow/unreachable endpoint doesn't prevent the other from completing.
	if perQueryTimeout <= 0 {
		perQueryTimeout = 15 * time.Second
	}
	var localRDFGroup, remoteRDFGroup *types.RDFGroup
	var localErr, remoteErr error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		if localClient == nil {
			localErr = errors.New("local client is nil")
			return
		}
		localCtx, localCancel := context.WithTimeout(ctx, perQueryTimeout)
		defer localCancel()
		localRDFGroup, localErr = localClient.GetRDFGroupByID(localCtx, localSymID, localRDFGroupNo)
	}()
	go func() {
		defer wg.Done()
		if remoteClient == nil {
			remoteErr = errors.New("remote client is nil")
			return
		}
		remoteCtx, remoteCancel := context.WithTimeout(ctx, perQueryTimeout)
		defer remoteCancel()
		remoteRDFGroup, remoteErr = remoteClient.GetRDFGroupByID(remoteCtx, remoteSymID, remoteRDFGroupNo)
	}()
	wg.Wait()

	// Both unreachable — hard error.
	if localErr != nil && remoteErr != nil {
		return nil, fmt.Errorf("%w: local: %v, remote: %v", ErrBothArraysUnreachable, localErr, remoteErr)
	}

	// Determine winner using Witness/Bias arbitration fields (FR-1.2).
	// Priority:
	//   1. If only one array responded, it is the winner.
	//   2. If both responded and one has WitnessEffective=true, it is winner.
	//   3. If both responded and one has BiasEffective=true, it is winner.
	//   4. If both responded with neither arbitration flag set (normal
	//      active-active), prefer local as the serving side.
	var rdfGroup *types.RDFGroup
	var winnerSymID, loserSymID string

	switch {
	case localErr != nil:
		// Only remote responded.
		rdfGroup = remoteRDFGroup
		winnerSymID = remoteSymID
		loserSymID = localSymID

	case remoteErr != nil:
		// Only local responded.
		rdfGroup = localRDFGroup
		winnerSymID = localSymID
		loserSymID = remoteSymID

	default:
		// Both responded — apply Witness-first winner determination (FR-1.2).
		// Use DevicePolarity to identify R1/R2 sides, then apply arbitration flags.
		// DevicePolarity indicates which array is the R1 (preferred) side.
		localIsR1 := localRDFGroup.DevicePolarity == localSymID
		remoteIsR1 := remoteRDFGroup.DevicePolarity == remoteSymID

		localWitness := localRDFGroup.WitnessEffective || localRDFGroup.BiasEffective
		remoteWitness := remoteRDFGroup.WitnessEffective || remoteRDFGroup.BiasEffective

		switch {
		case localWitness && !remoteWitness:
			// Local is arbitration winner (regardless of R1/R2).
			rdfGroup = localRDFGroup
			winnerSymID = localSymID
			loserSymID = remoteSymID
		case remoteWitness && !localWitness:
			// Remote is arbitration winner (regardless of R1/R2).
			rdfGroup = remoteRDFGroup
			winnerSymID = remoteSymID
			loserSymID = localSymID
		case localIsR1 && !remoteIsR1:
			// No arbitration flags set, but local is R1 (preferred side).
			// Default to R1 when no Witness/Bias guidance exists.
			rdfGroup = localRDFGroup
			winnerSymID = localSymID
			loserSymID = remoteSymID
		case remoteIsR1 && !localIsR1:
			// No arbitration flags set, but remote is R1 (preferred side).
			// Default to R1 when no Witness/Bias guidance exists.
			rdfGroup = remoteRDFGroup
			winnerSymID = remoteSymID
			loserSymID = localSymID
		default:
			// Normal active-active with no clear preference - default to local.
			rdfGroup = localRDFGroup
			winnerSymID = localSymID
			loserSymID = remoteSymID
		}
	}

	state := &MetroState{
		WinnerSymID:       winnerSymID,
		LoserSymID:        loserSymID,
		WitnessConfigured: rdfGroup.WitnessConfigured,
		WitnessEffective:  rdfGroup.WitnessEffective,
		BiasConfigured:    rdfGroup.BiasConfigured,
		BiasEffective:     rdfGroup.BiasEffective,
		LoserUnreachable:  localErr != nil || remoteErr != nil, // true only when one array is actually unreachable
	}

	// Device Bias R1 failure detection
	// When Device Bias is configured but not effective AND the winner is NOT the R1 (preferred) side,
	// this indicates R1 has failed and cannot automatically take over. This is a hard error
	// because routing to R2 under Device Bias is prohibited by the storage array.
	// We determine R1 by checking DevicePolarity field.
	if rdfGroup.BiasConfigured && !rdfGroup.BiasEffective {
		// Determine which array is R1 based on DevicePolarity
		r1SymID := rdfGroup.DevicePolarity
		if winnerSymID != r1SymID {
			// Winner is not R1 - this is a Device Bias R1 failure
			state.DeviceBiasR1Failure = true
			return nil, ErrDeviceBiasR1Failure
		}
	}

	return state, nil
}

// MetroStateCache provides a TTL cache for CheckMetroState results to avoid
// querying both arrays on every CSI call. Keyed by (localSymID, remoteSymID).
type MetroStateCache struct {
	mu      sync.RWMutex
	entries map[string]metroStateCacheEntry
	ttl     time.Duration
}

type metroStateCacheEntry struct {
	state     *MetroState
	err       error
	expiresAt time.Time
}

// MetroStateCacheTTL is the default TTL for cached CheckMetroState results.
const MetroStateCacheTTL = 30 * time.Second

// MetroStateErrorCacheTTL is the TTL used when caching error results.
// Shorter than MetroStateCacheTTL so the driver re-checks for recovery
// without blocking every sequential CSI call for a full 15-second timeout
const MetroStateErrorCacheTTL = 5 * time.Second

// NewMetroStateCache creates a new cache with the given TTL.
func NewMetroStateCache(ttl time.Duration) *MetroStateCache {
	if ttl <= 0 {
		ttl = MetroStateCacheTTL
	}
	return &MetroStateCache{
		entries: make(map[string]metroStateCacheEntry),
		ttl:     ttl,
	}
}

func metroStateCacheKey(localSymID, remoteSymID string) string {
	return localSymID + ":" + remoteSymID
}

// Get returns a cached result if available and not expired.
func (c *MetroStateCache) Get(localSymID, remoteSymID string) (*MetroState, bool, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[metroStateCacheKey(localSymID, remoteSymID)]
	if !ok || time.Now().After(e.expiresAt) {
		return nil, false, nil
	}
	return e.state, true, e.err
}

// Put stores a result in the cache with the normal TTL.
func (c *MetroStateCache) Put(localSymID, remoteSymID string, state *MetroState, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[metroStateCacheKey(localSymID, remoteSymID)] = metroStateCacheEntry{
		state:     state,
		err:       err,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// PutError stores an error result in the cache with the short error TTL
// (MetroStateErrorCacheTTL). This prevents a sequential CSI-call latency storm
// when both arrays are unreachable: instead of immediately invalidating and
// re-firing a 15-second Unisphere call on every RPC, callers wait at most
// MetroStateErrorCacheTTL before the next live check
func (c *MetroStateCache) PutError(localSymID, remoteSymID string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[metroStateCacheKey(localSymID, remoteSymID)] = metroStateCacheEntry{
		state:     nil,
		err:       err,
		expiresAt: time.Now().Add(MetroStateErrorCacheTTL),
	}
}

// rdfGroupCache caches GetRDFGroupList results per array to avoid redundant
// full-list scans on every unpublish call.
type rdfGroupCache struct {
	mu      sync.RWMutex
	entries map[string]rdfGroupCacheEntry
	ttl     time.Duration
}

type rdfGroupCacheEntry struct {
	result    string // resolved RDF group number
	expiresAt time.Time
}

const rdfGroupCacheTTL = 60 * time.Second

var globalRDFGroupCache = &rdfGroupCache{
	entries: make(map[string]rdfGroupCacheEntry),
	ttl:     rdfGroupCacheTTL,
}

// rdfGroupCacheKey builds a cache key from the array pair and an optional
// rdfGroupNo. When rdfGroupNo is empty the key represents the auto-resolved
// group for that array pair (used by resolveRDFGroupNo).
func rdfGroupCacheKey(localSymID, remoteSymID, rdfGroupNo string) string {
	return localSymID + ":" + remoteSymID + ":" + rdfGroupNo
}

func (c *rdfGroupCache) get(localSymID, remoteSymID, rdfGroupNo string) (string, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	e, ok := c.entries[rdfGroupCacheKey(localSymID, remoteSymID, rdfGroupNo)]
	if !ok || time.Now().After(e.expiresAt) {
		return "", false
	}
	return e.result, true
}

func (c *rdfGroupCache) put(localSymID, remoteSymID, rdfGroupNo, result string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[rdfGroupCacheKey(localSymID, remoteSymID, rdfGroupNo)] = rdfGroupCacheEntry{
		result:    result,
		expiresAt: time.Now().Add(c.ttl),
	}
}

// ResetRDFGroupCache clears the cached RDF group lookups. Exported for testing.
func ResetRDFGroupCache() {
	globalRDFGroupCache.mu.Lock()
	defer globalRDFGroupCache.mu.Unlock()
	globalRDFGroupCache.entries = make(map[string]rdfGroupCacheEntry)
}

// Invalidate removes the cached entry for the given array pair so the next
// call will perform a fresh CheckMetroState query.  Used to force a recheck
// after a cached error.
func (c *MetroStateCache) Invalidate(localSymID, remoteSymID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, metroStateCacheKey(localSymID, remoteSymID))
}
