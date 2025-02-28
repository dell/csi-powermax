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

package cache

import (
	"testing"
	"time"
)

func TestCache(t *testing.T) {
	// Test case 1: Testing the Set method
	testCases := []struct {
		key   string
		value interface{}
	}{
		{"key1", "value1"},
		{"key2", "value2"},
		{"key3", "value3"},
	}

	for _, tc := range testCases {
		c := New("test", time.Second)
		c.Set(tc.key, tc.value)
		if v, ok := c.Get(tc.key); !ok || v != tc.value {
			t.Errorf("Expected value %v, got %v", tc.value, v)
		}
	}

	// Test case 2: Testing the Get method
	testCases = []struct {
		key   string
		value interface{}
	}{
		{"key1", "value1"},
		{"key2", "value2"},
		{"key3", "value3"},
	}

	for _, tc := range testCases {
		c := New("test", time.Second)
		c.Set(tc.key, tc.value)
		if v, ok := c.Get(tc.key); !ok || v != tc.value {
			t.Errorf("Expected value %v, got %v", tc.value, v)
		}
	}

	// Test case 3: Testing the Remove method
	testCases = []struct {
		key   string
		value interface{}
	}{
		{"key1", "value1"},
		{"key2", "value2"},
		{"key3", "value3"},
	}

	for _, tc := range testCases {
		c := New("test", time.Second)
		c.Set(tc.key, tc.value)
		c.Remove(tc.key)
		if v, ok := c.Get(tc.key); ok || v != nil {
			t.Errorf("Expected nil, got %v", v)
		}
	}
}

func TestCleanupCallback2(t *testing.T) {
	tests := []struct {
		name           string
		initialSet     bool
		cleanupKey     string
		expectedExists bool
	}{
		{
			name:           "Key does not exist before cleanup",
			initialSet:     false,
			cleanupKey:     "testKey",
			expectedExists: false,
		},
		{
			name:           "Key exists and is removed",
			initialSet:     true,
			cleanupKey:     "testKey",
			expectedExists: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &cache{
				name:  "test",
				store: map[string]data{},
			}

			if tt.initialSet {
				c.Set(tt.cleanupKey, "testValue")
			}

			cleanup := c.cleanupCallback(tt.cleanupKey)
			cleanup()

			c.cmu.Lock()
			defer c.cmu.Unlock()
			_, exists := c.store[tt.cleanupKey]
			if exists != tt.expectedExists {
				t.Fatalf("Expected key existence: %v, got: %v", tt.expectedExists, exists)
			}
		})
	}
}
