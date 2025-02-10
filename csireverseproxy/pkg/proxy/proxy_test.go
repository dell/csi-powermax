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

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/utils"

	types "github.com/dell/gopowermax/v2/types/v100"

	"github.com/gorilla/mux"
)

func readConfigFile(fileName string) string {
	relativePath := filepath.Join(".", "..", "..", common.TestConfigDir, fileName)
	return relativePath
}

func TestNewProxy(t *testing.T) {
	configFile := readConfigFile(common.TestConfigFileName)
	configMap, err := config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	k8sUtils := k8smock.Init()
	_, err = k8sUtils.CreateNewCertSecret("secret-cert")
	if err != nil {
		t.Fatalf("Failed to create new cert secret: %v", err)
	}

	err = createSecrets(k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create secrets: %v", err)
	}

	proxyConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	t.Logf("proxyConfig: %v", proxyConfig)

	// Create a new Proxy using the ProxyConfig
	_, err = NewProxy(*proxyConfig)
	if err != nil {
		t.Errorf("Failed to create new proxy: %v", err)
		return
	}
}

func TestUpdateConfig(t *testing.T) {
	configFile := readConfigFile(common.TestConfigFileName)
	configMap, err := config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	k8sUtils := k8smock.Init()
	_, err = k8sUtils.CreateNewCertSecret("secret-cert")
	if err != nil {
		t.Fatalf("Failed to create new cert secret: %v", err)
	}

	err = createSecrets(k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create secrets: %v", err)
	}

	proxyConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	t.Logf("proxyConfig: %v", proxyConfig)

	// Create a new Proxy using the ProxyConfig
	proxy, err := NewProxy(*proxyConfig)
	if err != nil {
		t.Errorf("Failed to create new proxy: %v", err)
		return
	}

	// Create new configFile - Test Update ALL endpoints
	configFile = readConfigFile("configB.yaml")
	configMap, err = config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	newConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*newConfig)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test Update ALL endpoints

	// Create new configFile - Test Remove array
	configFile = readConfigFile("configB_single.yaml")
	configMap, err = config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	singleConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*singleConfig)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test Remove array

	// Create new configFile - Test No Backup
	configFile = readConfigFile("configB_single_noBackup.yaml")
	configMap, err = config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	noBackup, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*noBackup)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test No Backup

	// Create new configFile - Test Add Backup
	configFile = readConfigFile("configB_single.yaml")
	configMap, err = config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	addBackup, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*addBackup)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test Add Backup

	// Create new configFile - Test New Array
	configFile = readConfigFile("configC_single.yaml")
	configMap, err = config.ReadConfig(filepath.Base(configFile), filepath.Dir(configFile))
	if err != nil {
		t.Fatalf("Failed to read config: %v", err)
	}

	lastConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Fatalf("Failed to create new proxy config: %v", err)
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*lastConfig)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test New Array

	// Create new configFile - Test No Update
	err = proxy.UpdateConfig(*lastConfig)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}
	// End of Test No Update
}

func TestGetRouter_ServeVolume(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeVolume - /{version}/sloprovisioning/symmetrix/{symid}/volume",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}

			})),
		},
		{
			name: "Fail: ServeVolume - unauthorized",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}

			})),
		},
		{
			name: "Fail: ServeVolume - unauthorized - wrong password",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "myPassword")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeVolume - Bad Response",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})),
		},
		{
			name: "Fail: ServeVolume - Empty Response",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(``))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
	}

	utils.InitializeLock()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := tc.proxy(tc.server)
			if proxy == nil {
				return
			}

			// Setup router
			router := proxy.GetRouter()
			for _, reqFunc := range tc.req {
				req := reqFunc()
				router.ServeHTTP(httptest.NewRecorder(), req)
			}

			t.Logf("Router: %v", router)
		})
	}
}

func TestGetRouter_ServeReverseProxy(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeReverseProxy - /{version}/sloprovisioning/symmetrix/{symid}",
			proxy: func(server *httptest.Server) *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("%s/%s/sloprovisioning/symmetrix/%s", utils.Prefix, "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}

			})),
		},
	}

	utils.InitializeLock()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := tc.proxy(tc.server)
			if proxy == nil {
				return
			}

			// Setup router
			router := proxy.GetRouter()
			for _, reqFunc := range tc.req {
				req := reqFunc()
				router.ServeHTTP(httptest.NewRecorder(), req)
			}

			t.Logf("Router: %v", router)
		})
	}
}
func TestGetRouter(t *testing.T) {
	volumeIteratorID := "00000000-1111-2abc-def3-44gh55ij66kl_0"
	server := fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("fake unisphere received: %s %s", r.Method, r.URL)
		switch r.URL.Path {
		case "/univmax/restapi/1.0/sloprovisioning/symmetrix":
			list := types.SymmetrixIDList{
				SymmetrixIDs: []string{"00000000-1111-2abc-def3-44gh55ij66kl"},
			}

			bytes, _ := json.Marshal(list)

			_, err := w.Write(bytes)
			if err != nil {
				t.Errorf("expected nil error, got %v", err)
			}
		case "/univmax/restapi/1.0/replication/capabilities/symmetrix":
			result := types.SymReplicationCapabilities{
				SymmetrixCapability: []types.SymmetrixCapability{
					{
						SymmetrixID: "00000000-1111-2abc-def3-44gh55ij66kl",
					},
				},
				Successful:  true,
				FailMessage: "",
			}

			bytes, _ := json.Marshal(result)

			_, err := w.Write(bytes)
			if err != nil {
				t.Errorf("expected nil error, got %v", err)
			}
		default:
			_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
			if err != nil {
				t.Errorf("expected nil error, got %v", err)
			}
		}
	}))

	testCases := []struct {
		name        string
		proxy       func() *Proxy
		req         []func() *http.Request
		expectedErr error
	}{
		{
			name: "Success: ServeVersions - /{version}/system/version",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("%s/%s/system/version", utils.Prefix, "9.1")
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServePerformance - /performance/Array/keys",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("%s/performance/Array/keys", utils.Prefix)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServeVolumePerformance - /performance/Volume/metrics",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("%s/performance/Volume/metrics", utils.Prefix)

					body := []byte(`{"systemId": "000000000001"}`)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServeFSPerformance - /performance/file/filesystem/metrics",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("%s/performance/file/filesystem/metrics", utils.Prefix)

					body := []byte(`{"systemId": "000000000001"}`)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServeIterator - /common/Iterator/{iterId}/page",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					url := fmt.Sprintf("/univmax/restapi/%s/sloprovisioning/symmetrix/%s/volume", "9.1", arrayID)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					return req
				},
				func() *http.Request {
					arrayID := "000000000001"
					iterID := volumeIteratorID
					url := fmt.Sprintf("%s/common/Iterator/%s/page", utils.Prefix, iterID)

					body := []byte(`{"systemId": "000000000001"}`)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

					vars := map[string]string{
						"symid":  arrayID,
						"iterId": iterID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServeSymmetrix - /{version}/sloprovisioning/symmetrix",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					version := "1.0"
					url := fmt.Sprintf("%s/%s/sloprovisioning/symmetrix", utils.Prefix, version)

					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			expectedErr: nil,
		},
		{
			name: "Success: ServeReplicationCapabilities - /{version}/replication/capabilities/symmetrix",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: []func() *http.Request{
				func() *http.Request {
					arrayID := "000000000001"
					version := "1.0"
					url := fmt.Sprintf("%s/%s/replication/capabilities/symmetrix", utils.Prefix, version)

					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			expectedErr: nil,
		},
	}

	// go utils.LockRequestHandler()
	// time.Sleep(100 * time.Millisecond) // Allow goroutine to start
	utils.InitializeLock()

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := tc.proxy()
			if proxy == nil {
				return
			}

			// Setup router
			router := proxy.GetRouter()
			for _, reqFunc := range tc.req {
				req := reqFunc()
				router.ServeHTTP(httptest.NewRecorder(), req)
			}

			t.Logf("Router: %v", router)
		})
	}
}

func createSecrets(k8sUtils *k8smock.MockUtils) error {
	_, err := k8sUtils.CreateNewCredentialSecret("primary-unisphere-secret-1")
	if err != nil {
		return err
	}

	_, err = k8sUtils.CreateNewCredentialSecret("backup-unisphere-secret-1")
	if err != nil {
		return err
	}

	_, err = k8sUtils.CreateNewCredentialSecret("primary-unisphere-secret-2")
	if err != nil {
		return err
	}

	_, err = k8sUtils.CreateNewCredentialSecret("backup-unisphere-secret-2")
	if err != nil {
		return err
	}

	return nil
}

func createValidProxyConfig(_ *testing.T, server *httptest.Server) (*Proxy, error) {
	configMap := mockProxyConfigMap(server)

	k8sUtils := k8smock.Init()
	_, err := k8sUtils.CreateNewCertSecret("secret-cert")
	if err != nil {
		return nil, err
	}

	err = createSecrets(k8sUtils)
	if err != nil {
		return nil, err
	}

	proxyConfig, err := config.NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		return nil, err
	}

	proxy, err := NewProxy(*proxyConfig)
	if err != nil {
		return nil, err
	}

	return proxy, nil
}

func fakeServer(t *testing.T, h http.Handler) *httptest.Server {
	s := httptest.NewServer(h)
	t.Cleanup(func() {
		s.Close()
	})
	return s
}

func mockProxyConfigMap(server *httptest.Server) *config.ProxyConfigMap {
	return &config.ProxyConfigMap{
		Port:      "2222",
		LogLevel:  "info",
		LogFormat: "json",
		Config: &config.Config{
			StorageArrayConfig: []config.StorageArrayConfig{
				{
					StorageArrayID:         "000000000001",
					PrimaryURL:             server.URL,
					ProxyCredentialSecrets: []string{"primary-unisphere-secret-1", "backup-unisphere-secret-1"},
				},
			},
			ManagementServerConfig: []config.ManagementServerConfig{
				{URL: server.URL, SkipCertificateValidation: true},
			},
		},
	}
}
