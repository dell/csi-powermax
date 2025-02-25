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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/net/context"
)

// TestQueryArrayStatus tests the query array status
func TestQueryArrayStatus(t *testing.T) {
	testCases := []struct {
		name   string
		server *httptest.Server
	}{
		{
			name: "Failed: Context Timeout",
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(10 * time.Second)
				w.WriteHeader(http.StatusBadRequest)
			})),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			s := &service{
				mode: "node",
			}

			ctx, cancelCtx := context.WithDeadline(context.Background(), time.Now().Add(10*time.Millisecond))
			defer cancelCtx()

			_, err := s.QueryArrayStatus(ctx, tc.server.URL)
			assert.Error(t, err)
		})
	}
}

func fakeServer(t *testing.T, h http.Handler) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(func() {
		s.Close()
	})
	return s
}
