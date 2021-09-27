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

package cache

import (
	log "github.com/sirupsen/logrus"
	"sync"
	"time"
)

// Cache is the interface for a timed key-value store
type Cache interface {
	Set(string, interface{})
	Get(string) (interface{}, bool)
	Remove(string)
}

var _ Cache = new(cache)

// New creates instance of timed key-value store.
func New(name string, ttl time.Duration) Cache {
	return &cache{
		name:  name,
		ttl:   ttl,
		store: make(map[string]data),
		cmu:   sync.RWMutex{},
		mu:    sync.Mutex{},
	}
}

type data struct {
	value        interface{}
	cleanupTimer *time.Timer
}

type cache struct {
	name  string
	ttl   time.Duration
	store map[string]data
	cmu   sync.RWMutex
	mu    sync.Mutex
}

// Set sets a new value
func (c *cache) Set(key string, value interface{}) {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	c.mu.Lock()
	c.store[key] = data{
		value:        value,
		cleanupTimer: time.AfterFunc(c.ttl, c.cleanupCallback(key)),
	}
	c.mu.Unlock()
}

// Get gets the value from the store
func (c *cache) Get(key string) (interface{}, bool) {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	c.mu.Lock()
	data, ok := c.store[key]
	c.mu.Unlock()
	if ok {
		return data.value, ok
	}
	return nil, false
}

// Remove deletes the value from the store
func (c *cache) Remove(key string) {
	c.cmu.RLock()
	defer c.cmu.RUnlock()
	c.mu.Lock()
	if data, ok := c.store[key]; ok {
		data.cleanupTimer.Stop()
		delete(c.store, key)
	}
	c.mu.Unlock()
}

func (c *cache) cleanupCallback(key string) func() {
	return func() {
		c.cmu.Lock()
		defer c.cmu.Unlock()
		delete(c.store, key)
		log.Debugf("Removed %s from store: %s\n", key, c.name)
	}
}
