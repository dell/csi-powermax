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
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/utils"

	"github.com/gorilla/mux"
)

func readConfigFile() string {
	relativePath := filepath.Join(".", "..", "..", common.TestConfigDir, common.TestConfigFileName)
	return relativePath
}

func TestNewProxy(t *testing.T) {
	configFile := readConfigFile()
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

func TestGetRouter(t *testing.T) {
	testCases := []struct {
		name           string
		proxy          *Proxy
		expectedRouter *mux.Router
		expectedErr    error
	}{
		{
			name: "Successful case",
			proxy: &Proxy{
				config: config.ProxyConfig{
					// Set the necessary fields for the ProxyConfig
				},
			},
			expectedRouter: mux.NewRouter(),
			expectedErr:    nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			router := tc.proxy.GetRouter()
			req := func() *http.Request {
				// ctx := context.WithValue(context.Background(), 0, arrayID)
				req, _ := http.NewRequest("GET", "/volume", nil)
				vars := map[string]string{
					"symid": "000000000001",
				}
				req = mux.SetURLVars(req, vars)
				req.SetBasicAuth("test-username", "test-password")
				return req
			}()

			router.ServeHTTP(httptest.NewRecorder(), req)

			t.Logf("Router: %v", router)
		})
	}
}

type ProxyClientSymIDKey string

func TestServeVolume(t *testing.T) {
	server := fakeServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("fake unisphere received: %s %s", r.Method, r.URL)
		_, err := w.Write([]byte(`{"id": "00000000-1111-2abc-def3-44gh55ij66kl_0"}`))
		if err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	}))

	// symKey := "proxyClientSymID"
	arrayID := "000000000001"
	testCases := []struct {
		name        string
		proxy       func() *Proxy
		req         *http.Request
		expectedErr error
	}{
		{
			name: "Successful case",
			proxy: func() *Proxy {
				// Create a new Proxy
				proxy, err := createValidProxyConfig(t, server)
				if err != nil {
					t.Errorf("Failed to create proxy: %v", err)
					return nil
				}

				return proxy
			},
			req: func() *http.Request {
				// ctx := context.WithValue(context.Background(), 0, arrayID)
				req, _ := http.NewRequest("GET", "/volume", nil)
				vars := map[string]string{
					"symid": arrayID,
				}
				req = mux.SetURLVars(req, vars)
				req.SetBasicAuth("test-username", "test-password")
				return req
			}(),
			expectedErr: nil,
		},
		// {
		// 	name:  "Error case",
		// 	proxy: &Proxy{
		// 		// Set the necessary fields for the Proxy object
		// 	},
		// 	req: func() *http.Request {
		// 		req, _ := http.NewRequest("GET", "/volume", nil)
		// 		return req
		// 	}(),
		// 	expectedErr: errors.New("some error"),
		// },
		// Add more test cases as needed
	}

	go utils.LockRequestHandler()
	time.Sleep(100 * time.Millisecond) // Allow goroutine to start

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			proxy := tc.proxy()
			if proxy == nil {
				return
			}

			proxy.ServeVolume(httptest.NewRecorder(), tc.req)
			// if !reflect.DeepEqual(err, tc.expectedErr) {
			// 	t.Errorf("Expected error to be %v, but got %v", tc.expectedErr, err)
			// }
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
