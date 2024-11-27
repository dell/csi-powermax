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
)

func readConfig() (*ProxyConfigMap, error) {
	return ReadConfig(common.TestConfigFileName, "./../../"+common.TestConfigDir)
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
