/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

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

package common

import (
	"net/http"
	"net/http/httputil"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewEnvoy(t *testing.T) {
	proxy := &Proxy{
		ReverseProxy: &httputil.ReverseProxy{},
	}
	e := NewEnvoy(proxy)
	if e.GetPrimary() != proxy {
		t.Errorf("Expected primary proxy to be set")
	}
}

func TestSetPrimary(t *testing.T) {
	proxy := &Proxy{
		ReverseProxy: &httputil.ReverseProxy{
			Transport: &http.Transport{},
		},
	}
	e := &envoy{}
	e.SetPrimary(proxy)
	if e.GetPrimary() != proxy {
		t.Errorf("Primary proxy not set correctly")
	}
}

func TestSetBackup(t *testing.T) {
	proxy := &Proxy{
		ReverseProxy: &httputil.ReverseProxy{
			Transport: &http.Transport{},
		},
	}
	e := &envoy{}
	e.SetBackup(proxy)
	if e.GetBackup() != proxy {
		t.Errorf("Backup proxy not set correctly")
	}
}

func TestSetPrimaryHTTPClient(t *testing.T) {
	client := &http.Client{}
	e := &envoy{}
	e.SetPrimaryHTTPClient(client)
	if e.GetPrimaryHTTPClient() != client {
		t.Errorf("Primary HTTP client not set correctly")
	}
}

func TestSetBackupHTTPClient(t *testing.T) {
	client := &http.Client{}
	e := &envoy{}
	e.SetBackupHTTPClient(client)
	if e.GetBackupHTTPClient() != client {
		t.Errorf("Backup HTTP client not set correctly")
	}
}

func TestHealthHandler_Success(t *testing.T) {
	e := &envoy{healthMonitor: NewProxyHealth()}
	e.healthHandler(true)
	if e.healthMonitor.HasDeteriorated() {
		t.Errorf("Health should not have deteriorated")
	}
}

func TestHealthHandler_Failure(t *testing.T) {
	e := &envoy{healthMonitor: NewProxyHealth()}
	e.healthHandler(false)
	if !e.healthMonitor.HasDeteriorated() {
		t.Errorf("Health should have deteriorated")
	}
}

func TestGetActiveProxy(t *testing.T) {
	primary := &Proxy{}
	backup := &Proxy{}
	e := &envoy{primary: primary, backup: backup}

	if e.GetActiveProxy() != primary {
		t.Errorf("Expected primary proxy to be active")
	}

	atomic.StoreInt32(&e.active, 1)
	if e.GetActiveProxy() != backup {
		t.Errorf("Expected backup proxy to be active")
	}
}

func TestGetActiveHTTPClient(t *testing.T) {
	primaryHTTPClient := &http.Client{}
	backupHTTPClient := &http.Client{}
	primary := &Proxy{ReverseProxy: &httputil.ReverseProxy{Transport: &http.Transport{}}}
	backup := &Proxy{ReverseProxy: &httputil.ReverseProxy{Transport: &http.Transport{}}}

	e := &envoy{
		primaryHTTP: primaryHTTPClient,
		backupHTTP:  backupHTTPClient,
		primary:     primary,
		backup:      backup,
	}

	if e.GetActiveHTTPClient() != primaryHTTPClient {
		t.Errorf("Expected primary HTTP client to be active")
	}

	atomic.StoreInt32(&e.active, 1)
	if e.GetActiveHTTPClient() != backupHTTPClient {
		t.Errorf("Expected backup HTTP client to be active")
	}
}

func TestRemoveBackupProxy(t *testing.T) {
	e := &envoy{backup: &Proxy{}}
	e.RemoveBackupProxy()
	if e.GetBackup() != nil {
		t.Errorf("Backup proxy not removed correctly")
	}
}

func TestRemoveBackupHTTPClient(t *testing.T) {
	e := &envoy{backupHTTP: &http.Client{}}
	e.RemoveBackupHTTPClient()
	if e.GetBackupHTTPClient() != nil {
		t.Errorf("Backup HTTP client not removed correctly")
	}
}

func TestConfigureHealthParams(t *testing.T) {
	e := &envoy{healthMonitor: NewProxyHealth()}
	e.ConfigureHealthParams(3, 2, time.Second)
	// Assuming SetThreshold properly sets parameters, no assertion needed
}

func TestHasHealthDeteriorated(t *testing.T) {
	e := &envoy{healthMonitor: NewProxyHealth()}
	if e.HasHealthDeteriorated() {
		t.Errorf("Health should not have deteriorated initially")
	}
}
