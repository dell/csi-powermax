/*
 Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"os"
	"testing"

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/k8sutils"
	"revproxy/v2/pkg/utils"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"path/filepath"
)

func readConfig() (*ProxyConfigMap, error) {
	return ReadConfig(common.TestConfigFileName, "./../../"+common.TestConfigDir, viper.New())
}

func readProxySecret() (*ProxySecret, error) {
	setReverseProxyUseSecret(true)
	relativePath := filepath.Join(".","..","..",common.TestConfigDir,common.TestSecretFileName)
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
	} else {
		return setEnv(common.EnvReverseProxyUseSecret, "false")
	}
}

func newProxyConfig(configMap *ProxyConfigMap, utils k8sutils.UtilsInterface) (*ProxyConfig, error) {
	return NewProxyConfig(configMap, utils)
}

func getProxyConfig(t *testing.T) (*ProxyConfig, error) {
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

func TestProxyConfig_UpdateCerts(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	testSecrets := []string{"secret-cert", "invalid-secret-cert"}
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
	testSecrets := []string{"proxy-secret", "powermax-secret"}
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
}

func TestProxyConfig_UpdateManagedArrays(t *testing.T) {
	config, err := getProxyConfig(t)
	if err != nil {
		return
	}
	newConfig := config.DeepCopy()
	config.UpdateManagedArrays(newConfig)
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
	fmt.Printf("Storage arrays: %+v\n", config.GetStorageArray("000197900045"))
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
			//assert.Equal(t, tc.expectedConfig, &config)
		})
	}
}
