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
	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/utils"
	"testing"

	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/stretchr/testify/assert"

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
			name: "Success: ServeVolume - with symid header",
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
					req.Header.Set("symid", arrayID)
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
		{
			name: "Fail: ServeReverseProxy - unauthorized",
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

					vars := map[string]string{}
					req = mux.SetURLVars(req, vars)
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
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

func TestGetRouter_ServeVersions(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeVersions - /{version}/system/version",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Success: ServeVersions - with symid in header",
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
					url := fmt.Sprintf("%s/%s/system/version", utils.Prefix, "9.1")
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")
					req.Header.Set("symid", arrayID)
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
			name: "Fail: ServeVersions - unauthorized",
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
					url := fmt.Sprintf("%s/%s/system/version", utils.Prefix, "9.1")
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		},
		{
			name: "Fail: ServeVersions - unauthorized - wrong password",
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
					url := fmt.Sprintf("%s/%s/system/version", utils.Prefix, "9.1")
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "myPassword")
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
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
		})
	}
}

func TestGetRouter_ServePerformance(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServePerformance - /performance/Array/keys",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeVersions - unauthorized",
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
					url := fmt.Sprintf("%s/performance/Array/keys", utils.Prefix)
					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
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
		})
	}
}

func TestGetRouter_ServeVolumePerformance(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
		expect *httptest.ResponseRecorder
	}{
		{
			name: "Success: ServeVolumePerformance - /performance/Volume/metrics",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusOK,
				Body: bytes.NewBuffer([]byte(`"id":"00000000-1111-2abc-def3-44gh55ij66kl_0"`)),
			},
		},
		{
			name: "Fail: ServeVolumePerformance - Empty Body",
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
					url := fmt.Sprintf("%s/performance/Volume/metrics", utils.Prefix)

					body := []byte(``)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

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
			expect: &httptest.ResponseRecorder{
				Code: http.StatusInternalServerError,
				Body: bytes.NewBuffer([]byte(`failed to decode request`)),
			},
		},
		{
			name: "Fail: ServeVolumePerformance - Invalid SystemID",
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
					url := fmt.Sprintf("%s/performance/Volume/metrics", utils.Prefix)

					// Invalid systemID
					body := []byte(`{"systemId": "000000000009"}`)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

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
			expect: &httptest.ResponseRecorder{
				Code: http.StatusInternalServerError,
				Body: bytes.NewBuffer([]byte(`failed to find array id in the configuration`)),
			},
		},
		{
			name: "Fail: ServeVolumePerformance - Bad Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusUnauthorized,
				Body: bytes.NewBuffer([]byte(`Not Authorised`)),
			},
		},
		{
			name: "Fail: ServeVolumePerformance - Empty Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(``))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusBadRequest,
				Body: bytes.NewBuffer([]byte(``)),
			},
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
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)

				assert.Equal(t, tc.expect.Code, resp.Code)
				assert.True(t, bytes.Contains(resp.Body.Bytes(), tc.expect.Body.Bytes()),
					fmt.Sprintf("expected: %s, \ngot: %s", tc.expect.Body.Bytes(), resp.Body.Bytes()))
			}
		})
	}
}

func TestGetRouter_ServeFSPerformance(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
		expect *httptest.ResponseRecorder
	}{
		{
			name: "Success: ServeFSPerformance - /performance/file/filesystem/metrics",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusOK,
				Body: bytes.NewBuffer([]byte(`"id":"00000000-1111-2abc-def3-44gh55ij66kl_0"`)),
			},
		},
		{
			name: "Fail: ServeFSPerformance - Empty Body",
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
					url := fmt.Sprintf("%s/performance/file/filesystem/metrics", utils.Prefix)

					body := []byte(``)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusInternalServerError,
				Body: bytes.NewBuffer([]byte(`failed to decode request`)),
			},
		},
		{
			name: "Fail: ServeFSPerformance - Invalid SystemID",
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
					url := fmt.Sprintf("%s/performance/file/filesystem/metrics", utils.Prefix)

					// Invalid systemID
					body := []byte(`{"systemId": "000000000009"}`)
					req, _ := http.NewRequest("GET", url, bytes.NewBuffer(body))

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)
					req.SetBasicAuth("test-username", "test-password")

					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusInternalServerError,
				Body: bytes.NewBuffer([]byte(`failed to find array id in the configuration`)),
			},
		},
		{
			name: "Fail: ServeFSPerformance - Bad Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusUnauthorized,
				Body: bytes.NewBuffer([]byte(`Not Authorise`)),
			},
		},
		{
			name: "Fail: ServeFSPerformance - Empty Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(``))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
			expect: &httptest.ResponseRecorder{
				Code: http.StatusBadRequest,
				Body: bytes.NewBuffer([]byte(``)),
			},
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
				resp := httptest.NewRecorder()
				router.ServeHTTP(resp, req)

				if resp != nil && tc.expect != nil {
					assert.Equal(t, tc.expect.Code, resp.Code)
					assert.True(t, bytes.Contains(resp.Body.Bytes(), tc.expect.Body.Bytes()),
						fmt.Sprintf("expected: %s, \ngot: %s", tc.expect.Body.Bytes(), resp.Body.Bytes()))
				}
			}
		})
	}
}

func TestGetRouter_ServeIterator(t *testing.T) {
	volumeIteratorID := "00000000-1111-2abc-def3-44gh55ij66kl_0"
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeIterator - /common/Iterator/{iterId}/page",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeIterator - missing iterator id",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		},
		{
			name: "Fail: ServeIterator - unauthorized",
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
		})
	}
}

func TestGetRouter_ServeSymmetrix(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeSymmetrix - /{version}/sloprovisioning/symmetrix",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				list := types.SymmetrixIDList{
					SymmetrixIDs: []string{"00000000-1111-2abc-def3-44gh55ij66kl"},
				}

				bytes, _ := json.Marshal(list)

				_, err := w.Write(bytes)
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeSymmetrix - unauthorized",
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
					version := "1.0"
					url := fmt.Sprintf("%s/%s/sloprovisioning/symmetrix", utils.Prefix, version)

					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)

					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		},
		{
			name: "Fail: ServeSymmetrix - Bad Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})),
		},
		{
			name: "Fail: ServeSymmetrix - Illformed Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bytes, _ := json.Marshal(``)

				_, err := w.Write(bytes)
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeSymmetrix - Empty Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				list := types.SymmetrixIDList{}

				bytes, _ := json.Marshal(list)

				_, err := w.Write(bytes)
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
		})
	}
}

func TestGetRouter_ServeReplicationCapabilities(t *testing.T) {
	testCases := []struct {
		name   string
		proxy  func(*httptest.Server) *Proxy
		req    []func() *http.Request
		server *httptest.Server
	}{
		{
			name: "Success: ServeReplicationCapabilities - /{version}/replication/capabilities/symmetrix",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			})),
		},
		{
			name: "Fail: ServeReplicationCapabilities - unauthorized",
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
					version := "1.0"
					url := fmt.Sprintf("%s/%s/replication/capabilities/symmetrix", utils.Prefix, version)

					req, _ := http.NewRequest("GET", url, nil)

					vars := map[string]string{
						"symid": arrayID,
					}
					req = mux.SetURLVars(req, vars)

					return req
				},
			},
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})),
		},
		{
			name: "Fail: ServeReplicationCapabilities - Bad Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
			})),
		},
		{
			name: "Fail: ServeReplicationCapabilities - Illformed Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				bytes, _ := json.Marshal(``)

				_, err := w.Write(bytes)
				if err != nil {
					t.Errorf("expected nil error, got %v", err)
				}
			})),
		},
		{
			name: "Fail: ServeReplicationCapabilities - Empty Response",
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
			server: fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				result := types.SymReplicationCapabilities{
					SymmetrixCapability: []types.SymmetrixCapability{},
					Successful:          false,
					FailMessage:         "empty response",
				}

				bytes, _ := json.Marshal(result)

				_, err := w.Write(bytes)
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
