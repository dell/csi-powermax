/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"fmt"
	"sync"
	"time"

	pmax "github.com/dell/gopowermax"
	"github.com/dell/gopowermax/types/v90"
)

const (
	CSIPrefix              = "csi"
	MaxVolIdentifierLength = 64
)

var (
	SRPCacheValidity         = 24 * time.Hour
	SnapLicenseCacheValidity = 24 * time.Hour
	validSLO                 = [...]string{"Diamond", "Platinum", "Gold", "Silver", "Bronze", "Optimized", "None"}
)

// We need to implement look through caches so that the caller
// is not bothered with updating the values in the cache

type CacheTime struct {
	CreationTime  time.Time
	CacheValidity time.Duration
}

func (c *CacheTime) IsValid() bool {
	// Cache not initialized
	if c.CreationTime.IsZero() {
		return false
	}
	// Is cache still valid
	if time.Now().Sub(c.CreationTime) < c.CacheValidity {
		return true
	}
	return false
}

func (c *CacheTime) Set(validity time.Duration) {
	c.CreationTime = time.Now()
	c.CacheValidity = validity
}

type ReplicationCapabilitiesCache struct {
	cap  *types.SymmetrixCapability
	time CacheTime
}

func (rep *ReplicationCapabilitiesCache) update(cap *types.SymmetrixCapability) {
	rep.cap = cap
	rep.time.Set(SnapLicenseCacheValidity)
}

func (rep *ReplicationCapabilitiesCache) Get(ctx context.Context, client pmax.Pmax, symID string) (*types.SymmetrixCapability, error) {
	if rep.time.IsValid() {
		return rep.cap, nil
	}
	symRepCapabilities, err := client.GetReplicationCapabilities(ctx)
	if err != nil {
		return nil, err
	}
	for _, symCapability := range symRepCapabilities.SymmetrixCapability {
		if symCapability.SymmetrixID == symID {
			rep.update(&symCapability)
			return &symCapability, nil
		}
	}
	return nil, fmt.Errorf("couldn't find sym id: %s in response", symID)
}

type SRPCache struct {
	identifiers []string
	time        CacheTime
}

func (s *SRPCache) Get(ctx context.Context, client pmax.Pmax, symID string) ([]string, error) {
	if s.time.IsValid() {
		return s.identifiers, nil
	}
	list, err := client.GetStoragePoolList(ctx, symID)
	if err != nil {
		return nil, err
	}
	s.update(list.StoragePoolIDs)
	return list.StoragePoolIDs, nil
}

func (s *SRPCache) update(srpList []string) {
	s.identifiers = srpList
	s.time.Set(SRPCacheValidity)
}

type PowerMax struct {
	SymID                    string
	ClusterPrefix            string
	TempSnapPrefix           string
	DelSnapPrefix            string
	client                   pmax.Pmax
	lock                     sync.Mutex
	storageResourcePoolCache SRPCache
	repCapabilitiesCache     ReplicationCapabilitiesCache
}

func (p *PowerMax) GetSRPs(ctx context.Context) ([]string, error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	return p.storageResourcePoolCache.Get(ctx, p.client, p.SymID)
}

func (p *PowerMax) GetServiceLevels() ([]string, error) {
	return validSLO[:], nil
}

func (p *PowerMax) GetDefaultServiceLevel() string {
	return "Optimized"
}

func (p *PowerMax) GetVolumeIdentifier(volumeName string) string {
	maxLength := MaxVolIdentifierLength - len(p.ClusterPrefix) - len(CSIPrefix) - 1
	//First get the short volume name
	shortVolumeName := truncateString(volumeName, maxLength)
	//Form the volume identifier using short volume name
	return fmt.Sprintf("%s-%s-%s", CSIPrefix, p.ClusterPrefix, shortVolumeName)
}

func (p *PowerMax) GetSGName(applicationPrefix, serviceLevel, storageResourcePool string) string {
	var storageGroupName string
	// Storage Group is required to be derived from the parameters (such as service level and storage resource pool which are supplied in parameters)
	if applicationPrefix == "" {
		storageGroupName = fmt.Sprintf("%s-%s-%s-%s-SG", CSIPrefix, p.ClusterPrefix,
			serviceLevel, storageResourcePool)
	} else {
		storageGroupName = fmt.Sprintf("%s-%s-%s-%s-%s-SG", CSIPrefix, p.ClusterPrefix,
			applicationPrefix, serviceLevel, storageResourcePool)
	}
	return storageGroupName
}

func (p *PowerMax) getClient() pmax.Pmax {
	return p.client.WithSymmetrixID(p.SymID)
}

func (p *PowerMax) GetClient() pmax.Pmax {
	return p.getClient()
}

type StorageArrays struct {
	StorageArrays *sync.Map
}

var storageArrays *StorageArrays

func (arrays *StorageArrays) AddPowerMax(symID string, client pmax.Pmax) error {
	_, ok := arrays.StorageArrays.Load(symID)
	if ok {
		return fmt.Errorf("PowerMax: %s already added to the configuration", symID)
	} else {
		powermax := PowerMax{
			SymID:  symID,
			client: client,
		}
		arrays.StorageArrays.LoadOrStore(symID, &powermax)
	}
	return nil
}

func getPowerMax(symID string) (*PowerMax, error) {
	val, ok := storageArrays.StorageArrays.Load(symID)
	if ok {
		return val.(*PowerMax), nil
	}
	return nil, fmt.Errorf("array: %s not found", symID)
}

func GetPowerMax(symID string) (*PowerMax, error) {
	return getPowerMax(symID)
}

func GetPowerMaxClient(primaryArray string, arrays ...string) (pmax.Pmax, error) {
	primaryPowermax, err := getPowerMax(primaryArray)
	if err != nil {
		return nil, err
	}

	// Check if a secondary array is specified,
	// and managed by the driver.
	if len(arrays) > 0 {
		_, err := getPowerMax(arrays[0])
		if err != nil {
			return nil, err
		}
		client := &metroClient{
			primaryArray:   primaryArray,
			secondaryArray: arrays[0],
			activeArray:    primaryArray,
		}
		c, _ := metroClients.LoadOrStore(client.getIdentifier(), client)
		return c.(*metroClient).getPowerMaxClient()
	}

	return primaryPowermax.getClient(), nil
}

func Initialize(symIDList []string, client pmax.Pmax) error {
	for _, symID := range symIDList {
		err := storageArrays.AddPowerMax(symID, client)
		if err != nil {
			return err
		}
	}
	return nil
}

func init() {
	storageArrays = new(StorageArrays)
	storageArrays.StorageArrays = &sync.Map{}
}

func truncateString(str string, maxLength int) string {
	truncatedString := str
	newLength := 0
	if len(str) > maxLength {
		if maxLength%2 != 0 {
			newLength = len(str) - maxLength/2 - 1
		} else {
			newLength = len(str) - maxLength/2
		}
		truncatedString = str[0:maxLength/2] + str[newLength:]
	}
	return truncatedString
}
