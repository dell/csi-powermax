/*
 Copyright © 2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package servermock

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"
)

func TestGetHandler(t *testing.T) {
	handler := GetHandler()
	tests := []struct {
		name       string
		url        string
		wantStatus int
		wantBody   string
	}{
		{"Version Endpoint", utils.Prefix + "/version", http.StatusOK, `{ "version": "V9.1.0.2" }`},
		{"Symmetrix Endpoint", utils.Prefix + "/v1/system/symmetrix", http.StatusOK, `{"symmetrixId": [ "000197802104", "000197900046", "000197900047" ]}`},
		{"Auth Endpoint", authenticationEndpoint, http.StatusUnauthorized, ""},
		{"Timeout Endpoint", timeoutEndpoint, http.StatusServiceUnavailable, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("got status %d, want %d", w.Code, tt.wantStatus)
			}

			if tt.wantBody != "" && w.Body.String() != tt.wantBody {
				t.Errorf("got body %s, want %s", w.Body.String(), tt.wantBody)
			}
		})
	}
}
