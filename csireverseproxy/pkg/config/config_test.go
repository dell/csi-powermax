/*
 Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/k8sutils"
	"revproxy/v2/pkg/utils"
	"testing"

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
		log.Fatalf("Failed to cleanup temp files. (%s)\n", err.Error())
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

func getStandAloneProxyConfig(t *testing.T) (*ProxyConfig, error) {
	k8sUtils := k8smock.Init()
	configMap, err := readConfig()
	if err != nil {
		t.Errorf("Failed to read config. (%s)", err.Error())
		return nil, err
	}
	for _, storageArray := range configMap.StandAloneConfig.StorageArrayConfig {
		for _, secretName := range storageArray.ProxyCredentialSecrets {
			_, err := k8sUtils.CreateNewCredentialSecret(secretName)
			if err != nil {
				t.Errorf("Failed to create proxy credential secret. (%s)", err.Error())
				return nil, err
			}
		}
	}
	for _, managementServer := range configMap.StandAloneConfig.ManagementServerConfig {
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
	configMap.Mode = "StandAlone"
	config, err := NewProxyConfig(configMap, k8sUtils)
	if err != nil {
		t.Errorf("Failed to create new standalone proxy config. (%s)", err.Error())
		return nil, err
	}
	return config, nil
}

func TestNewLinkedProxyConfig(t *testing.T) {
	k8sUtils := k8smock.Init()
	proxyConfigMap, err := readConfig()
	if err != nil {
		t.Errorf("read config failed: %s", err.Error())
		return
	}
	if proxyConfigMap.LinkConfig.Primary.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Primary.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	if proxyConfigMap.LinkConfig.Backup.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Backup.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	log.Debug("config read")
	proxyConfigMap.Mode = "Linked"
	proxyConfig, err := newProxyConfig(proxyConfigMap, k8sUtils)
	if err != nil {
		t.Errorf("new Linked ProxyConfig failed: %s", err.Error())
		return
	}
	if proxyConfig.LinkProxyConfig == nil {
		t.Error("Config not created properly")
		return
	}
	proxyConfig.LinkProxyConfig.Log()
	log.Info("Linked Proxy Config created successfully")
}

func TestNewStandAloneProxyConfig(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	if config.StandAloneProxyConfig == nil {
		t.Error("Config not created properly")
		return
	}
	fmt.Printf("Management servers: %+v\n", config.StandAloneProxyConfig.GetManagementServers())
	config.StandAloneProxyConfig.Log()
	fmt.Println("StandAlone proxy config created successfully")
}

func TestLinkedProxyConfig_IsCertSecretRelated(t *testing.T) {
	k8sUtils := k8smock.Init()
	proxyConfigMap, err := readConfig()
	if err != nil {
		t.Errorf("read config failed: %s", err.Error())
		return
	}
	log.Debug("config read")
	if proxyConfigMap.LinkConfig.Primary.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Primary.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	if proxyConfigMap.LinkConfig.Backup.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Backup.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	proxyConfig, err := newProxyConfig(proxyConfigMap, k8sUtils)
	if err != nil {
		t.Errorf("new Linked ProxyConfig failed: %s", err.Error())
		return
	}
	primaryCerts := proxyConfig.LinkProxyConfig.Primary.CertSecret
	backupCerts := proxyConfig.LinkProxyConfig.Backup.CertSecret
	if !proxyConfig.LinkProxyConfig.IsCertSecretRelated(primaryCerts) {
		t.Errorf("failed: primary certs should match")
		return
	}
	if !proxyConfig.LinkProxyConfig.IsCertSecretRelated(backupCerts) {
		t.Errorf("failed: backup certs should match")
		return
	}
	newCertSecret, err := k8sUtils.CreateNewCertSecret("new-cert-secret")
	if err != nil {
		t.Error(err.Error())
		return
	}
	if proxyConfig.LinkProxyConfig.IsCertSecretRelated(newCertSecret.Name) {
		t.Errorf("failed: other certs should not match")
		return
	}
}
func TestLinkedProxyConfig_UpdateCertFileName(t *testing.T) {
	k8sUtils := k8smock.Init()
	proxyConfigMap, err := readConfig()
	if err != nil {
		t.Errorf("read config failed: %s", err.Error())
		return
	}
	log.Debug("config read")
	if proxyConfigMap.LinkConfig.Primary.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Primary.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	if proxyConfigMap.LinkConfig.Backup.CertSecret != "" {
		_, err = k8sUtils.CreateNewCertSecret(proxyConfigMap.LinkConfig.Backup.CertSecret)
		if err != nil {
			t.Error(err.Error())
			return
		}
	}
	proxyConfig, err := newProxyConfig(proxyConfigMap, k8sUtils)
	if err != nil {
		t.Errorf("new Linked ProxyConfig failed: %s", err.Error())
		return
	}
	newCertSecret, err := k8sUtils.CreateNewCertSecret("new-cert-secret")
	if err != nil {
		t.Error(err.Error())
		return
	}
	certFile, err := k8sUtils.GetCertFileFromSecret(newCertSecret)
	if err != nil {
		t.Error(err.Error())
		return
	}
	if proxyConfig.LinkProxyConfig.UpdateCertFileName(newCertSecret.Name, certFile) {
		t.Errorf("failed: certs should not match")
		return
	}
	primaryCerts := proxyConfig.LinkProxyConfig.Primary.CertSecret
	if !proxyConfig.LinkProxyConfig.UpdateCertFileName(primaryCerts, certFile) {
		t.Errorf("failed: primary certs should match")
		return
	}
	backupCerts := proxyConfig.LinkProxyConfig.Primary.CertSecret
	if !proxyConfig.LinkProxyConfig.UpdateCertFileName(backupCerts, certFile) {
		t.Errorf("failed: backup certs should match")
		return
	}

}

func TestStandAloneProxyConfig_UpdateCerts(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	testSecrets := []string{"secret-cert", "invalid-secret-cert"}
	for _, secret := range testSecrets {
		if config.StandAloneProxyConfig.IsSecretConfiguredForCerts(secret) {
			config.StandAloneProxyConfig.UpdateCerts(secret, "/path/to/new/dummy/cert/file.pem")
		}
	}
	fmt.Println("Cert secrets updated successfully.")
}

func TestStandAloneProxyConfig_UpdateCreds(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	testSecrets := []string{"proxy-secret", "powermax-secret"}
	newCredentials := &common.Credentials{
		UserName: "new-test-username",
		Password: "new-test-password",
	}
	for _, secret := range testSecrets {
		if config.StandAloneProxyConfig.IsSecretConfiguredForArrays(secret) {
			config.StandAloneProxyConfig.UpdateCreds(secret, newCredentials)
		}
	}
	config.StandAloneProxyConfig.UpdateCreds("powermax-secret", newCredentials)
	fmt.Println("Credentials updated successfully")
}

func TestStandAloneProxyConfig_UpdateCertsAndCredentials(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	k8sUtils := k8smock.Init()
	serverSecret, _ := k8sUtils.CreateNewCredentialSecret("powermax-secret")
	proxySecret, _ := k8sUtils.CreateNewCredentialSecret("proxy-secret")
	serverSecret.Data["username"] = []byte("new-username")
	proxySecret.Data["username"] = []byte("new-username")
	certSecret, _ := k8sUtils.CreateNewCertSecret("secret-cert")
	config.StandAloneProxyConfig.UpdateCertsAndCredentials(k8sUtils, serverSecret)
	config.StandAloneProxyConfig.UpdateCertsAndCredentials(k8sUtils, proxySecret)
	config.StandAloneProxyConfig.UpdateCertsAndCredentials(k8sUtils, certSecret)
	fmt.Println("Certs and Secrets updated successfully")
}

func TestStandAloneProxyConfig_UpdateManagementServers(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	newConfig := config.DeepCopy()
	_, _, err = config.StandAloneProxyConfig.UpdateManagementServers(newConfig.StandAloneProxyConfig)
	if err != nil {
		t.Errorf("failed to update management servers. (%s)", err.Error())
		return
	}
	fmt.Println("Management servers updated successfully")
}

func TestStandAloneProxyConfig_UpdateManagedArrays(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	newConfig := config.DeepCopy()
	config.StandAloneProxyConfig.UpdateManagedArrays(newConfig.StandAloneProxyConfig)
	fmt.Printf("Managed arrays updated successfully")
}

func TestStandAloneProxyConfig_GetAuthorizedArrays(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	fmt.Printf("Authorized arrays: %+v\n", config.StandAloneProxyConfig.GetAuthorizedArrays("test-username", "test-password"))
}

func TestStandAloneProxyConfig_GetStorageArray(t *testing.T) {
	config, err := getStandAloneProxyConfig(t)
	if err != nil {
		return
	}
	fmt.Printf("Storage arrays: %+v\n", config.StandAloneProxyConfig.GetStorageArray("000197900045"))
}
