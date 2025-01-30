/*
 Copyright © 2021-2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8smock"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8sutils"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v2"
)

func readConfig() (*ProxyConfigMap, error) {
	return ReadConfig(common.TestConfigFileName, "./../../"+common.TestConfigDir, viper.New())
}

func readProxySecret() (*ProxySecret, error) {
	setReverseProxyUseSecret(true)
	relativePath := filepath.Join(".", "..", "..", common.TestConfigDir, common.TestSecretFileName)
	setEnv(common.EnvSecretFilePath, relativePath)
	return ReadConfigFromSecret(viper.New())
}

func TestMain(m *testing.M) {
	status := 0
	if st := m.Run(); st > status {
		status = st
	}
	err := utils.RemoveTempFiles()
	if err != nil {
		log.Fatalf("Failed to cleanup temp files. (%s)", err.Error())
		status = 1
	}
	os.Exit(status)
}

func TestReadConfig(t *testing.T) {
	proxyConfigMap, err := readConfig()
	if err != nil {
		t.Errorf(err.Error())
		return
	}
	fmt.Printf("%v", proxyConfigMap)
}

func setEnv(key, value string) error {
	err := os.Setenv(key, value)
	if err != nil {
		return err
	}
	return nil
}

func setReverseProxyUseSecret(value bool) error {
	if value {
		return setEnv(common.EnvReverseProxyUseSecret, "true")
	}
	return setEnv(common.EnvReverseProxyUseSecret, "false")
}

func newProxyConfig(configMap *ProxyConfigMap, utils k8sutils.UtilsInterface) (*ProxyConfig, error) {
	return NewProxyConfig(configMap, utils)
}

func getProxyConfig(t *testing.T) (*ProxyConfig, error) {
	setReverseProxyUseSecret(false)
	k8sUtils := k8smock.Init()
	configMap, err := readConfig()
	if err != nil {
		t.Errorf("Failed to read config. (%s)", err.Error())
		return nil, err
	}
	for _, storageArray := range configMap.Config.StorageArrayConfig {
		for _, secretName := range storageArray.ProxyCredentialSecrets {
			_, err := k8sUtils.CreateNewCredentialSecret(secretName)
			if err != nil {
				t.Errorf("Failed to create proxy credential secret. (%s)", err.Error())
				return nil, err
			}
		}
	}
	for _, managementServer := range configMap.Config.ManagementServerConfig {
		_, err := k8sUtils.CreateNewCredentialSecret(managementServer.ArrayCredentialSecret)
		if err != nil {
			t.Errorf("Failed to create server credential secret. (%s)", err.Error())
			return nil, err
		}
		_, err = k8sUtils.CreateNewCertSecret(managementServer.CertSecret)
		if err != nil {
			t.Errorf("Fialed to create server cert secret. (%s)", err.Error())
			return nil, err
		}
	}
	config, err := NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Errorf("Failed to create new config. (%s)", err.Error())
		return nil, err
	}
	return config, nil
}

func TestNewProxyConfig(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	if config == nil {
		t.Error("Config not created properly")
		return
	}
	fmt.Printf("Management servers: %+v\n", config.GetManagementServers())
	config.Log()
	fmt.Println("Proxy config created successfully")
}

func TestParseConfig(t *testing.T) {
	k8sUtils := k8smock.Init()
	configMap, err := readConfig()
	if err != nil {
		t.Errorf("Failed to read config. (%s)", err.Error())
		return
	}

	// deep copy the configmap to avoid modifying the original
	deepCopyFunc := func(src *ProxyConfigMap) ProxyConfigMap {
		var dst ProxyConfigMap
		bytes, _ := yaml.Marshal(src)   // Serialize
		_ = yaml.Unmarshal(bytes, &dst) // Deserialize
		return dst
	}

	tests := []struct {
		name    string
		cm      ProxyConfigMap
		before  func(*ProxyConfigMap)
		wantErr bool
		errMsg  string
	}{
		{
			name: "ProxyConfigMap.Config is nil",
			before: func(cm *ProxyConfigMap) {
				*cm = deepCopyFunc(configMap)
				cm.Config = nil
			},
			wantErr: true,
		},
		{
			name: "Primary URL is nil",
			before: func(cm *ProxyConfigMap) {
				*cm = deepCopyFunc(configMap)
				cm.Config.StorageArrayConfig[0].PrimaryEndpoint = ""
			},
			wantErr: true,
		},
		{
			name: "Backup URL is nil",
			before: func(cm *ProxyConfigMap) {
				*cm = deepCopyFunc(configMap)
				cm.Config.StorageArrayConfig[0].BackupEndpoint = ""
			},
			wantErr: true,
		},
		{
			name: "Backup URL is not present",
			before: func(cm *ProxyConfigMap) {
				*cm = deepCopyFunc(configMap)
				// modify the configmap to include a new array
				cm.Config.StorageArrayConfig[0].BackupEndpoint = "123000123000"
			},
			wantErr: true,
		},
		{
			name: "Primary and backup URL non empty not present",
			before: func(cm *ProxyConfigMap) {
				*cm = deepCopyFunc(configMap)
				storageArrayConfig := StorageArrayConfig{
					PrimaryEndpoint:        "new-primary-1.unisphe.re:8443",
					BackupEndpoint:         "new-backup-1.unisphe.re:8443",
					StorageArrayID:         "123000123000",
					ProxyCredentialSecrets: []string{"new-primary-unisphere-secret-1", "new-backup-unisphere-secret-1"},
				}
				cm.Config.StorageArrayConfig = append(cm.Config.StorageArrayConfig, storageArrayConfig)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.before(&tt.cm)
			_, err = NewProxyConfig(&tt.cm, k8sUtils)
			if err == nil && tt.wantErr {
				t.Logf("Expecing error but got none")
			}
		})
	}
}

func TestProxyConfig_UpdateCerts(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	testSecrets := []string{"secret-cert", "invalid-secret-cert", "primary-unisphere-secret-1", "primary-unisphere-secret-2"}
	for _, secret := range testSecrets {
		if config.IsSecretConfiguredForCerts(secret) {
			config.UpdateCerts(secret, "/path/to/new/dummy/cert/file.pem")
		}
	}
	fmt.Println("Cert secrets updated successfully.")
}

func TestProxyConfig_UpdateCreds(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	testSecrets := []string{"proxy-secret", "powermax-secret", "primary-unisphere-secret-1"}
	newCredentials := &common.Credentials{
		UserName: "new-test-username",
		Password: "new-test-password",
	}
	for _, secret := range testSecrets {
		if config.IsSecretConfiguredForArrays(secret) {
			config.UpdateCreds(secret, newCredentials)
		}
	}
	config.UpdateCreds("powermax-secret", newCredentials)
	fmt.Println("Credentials updated successfully")
}

func TestProxyConfig_UpdateCertsAndCredentials(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	k8sUtils := k8smock.Init()
	serverSecret, _ := k8sUtils.CreateNewCredentialSecret("powermax-secret")
	proxySecret, _ := k8sUtils.CreateNewCredentialSecret("proxy-secret")
	serverSecret.Data["username"] = []byte("new-username")
	proxySecret.Data["username"] = []byte("new-username")
	certSecret, _ := k8sUtils.CreateNewCertSecret("secret-cert")
	config.UpdateCertsAndCredentials(k8sUtils, serverSecret)
	config.UpdateCertsAndCredentials(k8sUtils, proxySecret)
	config.UpdateCertsAndCredentials(k8sUtils, certSecret)

	primaryUnisphereSecret2, _ := k8sUtils.CreateNewCredentialSecret("primary-unisphere-secret-2")
	primaryUnisphereSecret2.Data["username"] = []byte("new-username")
	primaryUnisphereCert2, _ := k8sUtils.CreateNewCertSecret("primary-unisphere-cert-2")
	config.UpdateCertsAndCredentials(k8sUtils, primaryUnisphereSecret2)
	config.UpdateCertsAndCredentials(k8sUtils, primaryUnisphereCert2)

	fmt.Println("Certs and Secrets updated successfully")
}

func TestProxyConfig_UpdateManagementServers(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	newConfig := config.DeepCopy()
	_, _, err = config.UpdateManagementServers(newConfig)
	if err != nil {
		t.Errorf("failed to update management servers. (%s)", err.Error())
		return
	}
	fmt.Println("Management servers updated successfully")

	// Test for invalid DeepCopy
	var nilConfig *ProxyConfig
	returnedConfig := nilConfig.DeepCopy()
	if returnedConfig != nil {
		t.Errorf("Expected nil config")
		return
	}
}

func TestProxyConfig_UpdateManagedArrays(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	newConfig := config.DeepCopy()
	config.UpdateManagedArrays(newConfig)
	fmt.Printf("Managed arrays updated successfully")

	// Test invalid config
	managedArrays := &newConfig.managedArrays
	for _, array := range *managedArrays {
		saveURL := array.PrimaryEndpoint
		array.PrimaryEndpoint = url.URL{
			Scheme: "https", Host: "new-primary-1.unisphe.re:8443",
		}
		config.UpdateManagedArrays(newConfig)
		array.PrimaryEndpoint = saveURL

	}
	// config.UpdateManagedArrays(newConfig)
	fmt.Printf("Managed arrays updated successfully")
}

func TestProxyConfig_GetAuthorizedArrays(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	fmt.Printf("Authorized arrays: %+v\n", config.GetAuthorizedArrays("test-username", "test-password"))
}

func TestProxyConfig_GetStorageArray(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	fmt.Printf("Storage arrays: %+v\n", config.GetStorageArray("000197900045")) // non empty invalid
	fmt.Printf("Storage arrays: %+v\n", config.GetStorageArray("000000000001")) // non empty valid
	fmt.Printf("Storage arrays: %+v\n", config.GetStorageArray(""))             // emp
}

func TestProxyConfig_GetManagedArrayAndServers(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}

	_ = config.GetManagedArraysAndServers()
}

func Test_GetManagementServerCredentials(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}

	for _, server := range config.GetManagementServers() {
		_, _ = config.GetManagementServerCredentials(server.Endpoint)
	}

	// Fetch invalid mgmt server
	_, _ = config.GetManagementServerCredentials(url.URL{
		Scheme: "https",
		Host:   "10.20.30.40",
	})
}

func TestGetManagementServer(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	for _, server := range config.GetManagementServers() {
		_, _ = config.GetManagementServer(server.Endpoint)
	}

	_, _ = config.GetManagementServer(url.URL{
		Scheme: "https",
		Host:   "10.20.30.40",
	})
}

func TestIsUserAuthorized(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}

	// valiid username, nonexistent array
	_, _ = config.IsUserAuthorized("test-username", "test-password", "12345678910")

	// nonexistent username
	_, _ = config.IsUserAuthorized("non-existent-username", "test-password", "12345678910")
}

func TestReadConfigFromSecret(t *testing.T) {
	setReverseProxyUseSecret(true)
	proxySecret, err := readProxySecret()
	if err != nil {
		t.Errorf(err.Error())
		return
	}
	fmt.Printf("%v", proxySecret)
}

func TestNewProxyConfigFromSecret(t *testing.T) {
	setReverseProxyUseSecret(true)
	proxySecret, err := readProxySecret()
	if err != nil {
		t.Errorf(err.Error())
		return
	}
	k8sUtils := k8smock.Init()
	proxyConfig, err := NewProxyConfigFromSecret(proxySecret, k8sUtils)
	if err != nil {
		return
	}
	if proxyConfig == nil {
		t.Error("Config not created properly")
		return
	}
	fmt.Printf("Management servers: %+v\n", proxyConfig.GetManagementServers())
}

func getProxyConfigFromSecret(t *testing.T) (*ProxyConfig, error) {
	proxySecret, err := readProxySecret()
	if err != nil {
		t.Errorf(err.Error())
		return nil, err
	}
	k8sUtils := k8smock.Init()
	proxyConfig, err := NewProxyConfigFromSecret(proxySecret, k8sUtils)
	if err != nil {
		return nil, err
	}
	return proxyConfig, nil
}

func TestProxyConfig_ParseConfigFromSecret(t *testing.T) {
	testCases := []struct {
		name           string
		proxySecret    ProxySecret
		expectedConfig *ProxyConfig
		expectedError  error
	}{
		{
			name: "Valid config with one array and one management server",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "test-symm-id",
						PrimaryEndpoint: "https://management.example.com",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: nil,
		},
		{
			name: "Invalid config with no arrays",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("no storage arrays configured"),
		},
		{
			name: "Invalid config with no endpoints configured for a storage array",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID: "test-symm-id",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("primary endpoint not configured for array: test-symm-id"),
		},
		{
			name: "Invalid config with primary endpoint not among management servers",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "test-symm-id",
						PrimaryEndpoint: "https://example.com",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("primary endpoint: %s for array: %s not present among management endpoint addresses", "https://example.com", "test-symm-id"),
		},
		{
			name: "Valid config with multiple arrays and multiple management servers",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "array1",
						PrimaryEndpoint: "https://management1.example.com",
						BackupEndpoint:  "https://management2.example.com",
					},
					{
						StorageArrayID:  "array2",
						PrimaryEndpoint: "https://management1.example.com",
						BackupEndpoint:  "https://management2.example.com",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management1.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
					{
						Endpoint:                  "https://management2.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: nil,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var config ProxyConfig
			err := config.ParseConfigFromSecret(tc.proxySecret, nil)
			if err != nil {
				assert.Equal(t, tc.expectedError, err)
				return
			}
			// assert.Equal(t, tc.expectedConfig, &config)
		})
	}
}

func TestProxyConfig_GetAuthorizedArraysFromSecret(t *testing.T) {
	testCases := []struct {
		name           string
		username       string
		password       string
		proxySecret    ProxySecret
		expectedArrays []string
		expectedError  error
	}{
		{
			name:     "Valid config with one array and one management server",
			username: "test-username",
			password: "password",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "test-symm-id",
						PrimaryEndpoint: "https://management.example.com",
					},
				},

				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError:  nil,
			expectedArrays: []string{"test-symm-id"},
		},
		{
			name:     "Invalid config with no arrays",
			username: "test-username",
			password: "password",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("no storage arrays configured"),
		},
		{
			name:     "Invalid config with no endpoints configured for a storage array",
			username: "test-username",
			password: "password",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID: "test-symm-id",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("primary endpoint not configured for array: test-symm-id"),
		},
		{
			name:     "Invalid config with primary endpoint not among management servers",
			username: "test-username",
			password: "password",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "test-symm-id",
						PrimaryEndpoint: "https://example.com",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError: fmt.Errorf("primary endpoint: %s for array: %s not present among management endpoint addresses", "https://example.com", "test-symm-id"),
		},
		{
			name:     "Valid config with multiple arrays and multiple management servers",
			username: "test-username",
			password: "password",
			proxySecret: ProxySecret{
				StorageArrayConfig: []StorageArrayConfig{
					{
						StorageArrayID:  "array1",
						PrimaryEndpoint: "https://management1.example.com",
						BackupEndpoint:  "https://management2.example.com",
					},
					{
						StorageArrayID:  "array2",
						PrimaryEndpoint: "https://management1.example.com",
						BackupEndpoint:  "https://management2.example.com",
					},
				},
				ManagementServerConfig: []ManagementServerConfig{
					{
						Endpoint:                  "https://management1.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
					{
						Endpoint:                  "https://management2.example.com",
						Username:                  "test-username",
						Password:                  "password",
						SkipCertificateValidation: true,
					},
				},
			},
			expectedError:  nil,
			expectedArrays: []string{"array1", "array2", "array1", "array2"}, // Returns the arrays via the two different management servers! Is this unexpected?!
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var config ProxyConfig
			err := config.ParseConfigFromSecret(tc.proxySecret, nil)
			if err != nil {
				assert.Equal(t, tc.expectedError, err)
				return
			}
			authorizedArrays := config.GetAuthorizedArrays(tc.username, tc.password)
			if err != nil {
				assert.Equal(t, tc.expectedError, err)
			} else {
				assert.Equal(t, len(tc.expectedArrays), len(authorizedArrays))
			}
		})
	}
}

func TestManagementServer_DeepCopy(t *testing.T) {
	testCases := []struct {
		name             string
		managementServer ManagementServer
		expectedCopy     ManagementServer
	}{
		{
			name: "Valid management server",
			managementServer: ManagementServer{
				Endpoint:                  url.URL{Host: "example.com"},
				StorageArrayIdentifiers:   []string{"000197900045"},
				Credentials:               common.Credentials{UserName: "test-username", Password: "test-password"},
				CredentialSecret:          "test-secret",
				SkipCertificateValidation: true,
				CertFile:                  "test-cert",
				CertSecret:                "test-cert-secret",
				Username:                  "test-username",
				Password:                  "test-password",
			},
			expectedCopy: ManagementServer{
				Endpoint:                  url.URL{Host: "example.com"},
				StorageArrayIdentifiers:   []string{"000197900045"},
				Credentials:               common.Credentials{UserName: "test-username", Password: "test-password"},
				CredentialSecret:          "test-secret",
				SkipCertificateValidation: true,
				CertFile:                  "test-cert",
				CertSecret:                "test-cert-secret",
				Username:                  "test-username",
				Password:                  "test-password",
			},
		},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			copy := tc.managementServer.DeepCopy()
			if !reflect.DeepEqual(tc.expectedCopy, *copy) {
				t.Errorf("Expected copy: %+v, but got: %+v", tc.expectedCopy, *copy)
			}
		})
	}
}

func TestProxyConfig_GetManagementServerCredentials(t *testing.T) {
	testCases := []struct {
		name          string
		mgmtEndpoint  url.URL
		expectedCred  common.Credentials
		expectedError error
	}{
		{
			name:          "Valid management server with EnvReverseProxyUseSecret=true",
			mgmtEndpoint:  url.URL{Host: "primary-1.unisphe.re:8443"},
			expectedCred:  common.Credentials{UserName: "admin", Password: "password"},
			expectedError: nil,
		},
		{
			name:          "Valid management server with EnvReverseProxyUseSecret=false",
			mgmtEndpoint:  url.URL{Host: "primary-1.unisphe.re:8443"},
			expectedCred:  common.Credentials{UserName: "test-username", Password: "test-password"},
			expectedError: nil,
		},
		{
			name:          "Invalid management server with EnvReverseProxyUseSecret=true",
			mgmtEndpoint:  url.URL{Host: "invalid.com"},
			expectedCred:  common.Credentials{},
			expectedError: fmt.Errorf("endpoint not configured"),
		},
		{
			name:          "Invalid management server with EnvReverseProxyUseSecret=false",
			mgmtEndpoint:  url.URL{Host: "invalid.com"},
			expectedCred:  common.Credentials{},
			expectedError: fmt.Errorf("endpoint not configured"),
		},
		// Add more test cases as needed
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var config *ProxyConfig
			var err error
			var endpoint url.URL

			// Set the EnvReverseProxyUseSecret environment variable
			if strings.Contains(tc.name, "EnvReverseProxyUseSecret=true") {
				setReverseProxyUseSecret(true)
				config, err = getProxyConfigFromSecret(t)
				if err != nil {
					return
				}

			} else if strings.Contains(tc.name, "EnvReverseProxyUseSecret=false") {
				setReverseProxyUseSecret(false)
				config, err = getProxyConfig(t)
				if err != nil {
					return
				}
			}
			if tc.expectedError == nil {
				endpoint = config.GetManagementServers()[0].Endpoint
			} else {
				endpoint = tc.mgmtEndpoint
			}

			credentials, err := config.GetManagementServerCredentials(endpoint)
			if err != nil {
				assert.Equal(t, tc.expectedError, err)
			} else {
				assert.Equal(t, tc.expectedCred, credentials)
			}
		})
	}
}
