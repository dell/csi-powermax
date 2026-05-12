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
package service

import (
	"context"
	"fmt"
	"sync"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
)

// versionCache caches VersionDetails and Symmetrix info per Unisphere client instance.
// Cache is populated once and lives for the lifetime of the driver process (no TTL).
// Keys are formed from the client's underlying HTTP client pointer to isolate per-Unisphere
// connections, ensuring multi-array, multi-Unisphere, and backup-Unisphere safety.
// sync.Map provides concurrent-safe reads with no starvation or deadlocks.
type versionCache struct {
	// versionEntries: key = fmt.Sprintf("%p", httpClient) -> *types.VersionDetails
	versionEntries sync.Map
	// symmetrixEntries: key = fmt.Sprintf("%p:%s", httpClient, symID) -> *types.Symmetrix
	symmetrixEntries sync.Map
}

func newVersionCache() *versionCache {
	return &versionCache{}
}

func clientKey(pmaxClient pmax.Pmax) string {
	return fmt.Sprintf("%p", pmaxClient.GetHTTPClient())
}

func symmetrixKey(pmaxClient pmax.Pmax, symID string) string {
	return fmt.Sprintf("%p:%s", pmaxClient.GetHTTPClient(), symID)
}

// getOrFetchVersionDetails returns cached VersionDetails, or fetches and caches it.
// Errors are never cached — a subsequent call will retry the fetch.
// Version is cached per client+SymID to ensure multi-array safety when arrays share a client.
func (c *versionCache) getOrFetchVersionDetails(ctx context.Context, symID string, pmaxClient pmax.Pmax) (*types.VersionDetails, error) {
	key := symmetrixKey(pmaxClient, symID)
	if v, ok := c.versionEntries.Load(key); ok {
		return v.(*types.VersionDetails), nil
	}
	details, err := pmaxClient.GetVersionDetails(ctx)
	if err != nil {
		return nil, err
	}
	// Store a copy to avoid callers mutating the cached value.
	cached := *details
	c.versionEntries.Store(key, &cached)
	return details, nil
}

// getOrFetchSymmetrix returns cached Symmetrix, or fetches and caches it.
// Errors are never cached — a subsequent call will retry the fetch.
func (c *versionCache) getOrFetchSymmetrix(ctx context.Context, symID string, pmaxClient pmax.Pmax) (*types.Symmetrix, error) {
	key := symmetrixKey(pmaxClient, symID)
	if v, ok := c.symmetrixEntries.Load(key); ok {
		return v.(*types.Symmetrix), nil
	}
	sym, err := pmaxClient.GetSymmetrixByID(ctx, symID)
	if err != nil {
		return nil, err
	}
	// Store a copy to avoid callers mutating the cached value.
	cached := *sym
	c.symmetrixEntries.Store(key, &cached)
	return sym, nil
}
