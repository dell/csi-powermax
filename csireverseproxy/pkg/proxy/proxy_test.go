/*
Copyright © 2021 - 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"os"
	"path/filepath"
	"testing"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/config"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8smock"

	"github.com/spf13/viper"
)

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
func readProxySecret() (*config.ProxySecret, error) {
	setReverseProxyUseSecret(true)
	relativePath := filepath.Join(".", "..", "..", common.TestConfigDir, common.TestSecretFileName)
	setEnv(common.EnvSecretFilePath, relativePath)
	return config.ReadConfigFromSecret(viper.New())
}

func getProxyConfigFromSecret(t *testing.T) (*config.ProxyConfig, error) {
	proxySecret, err := readProxySecret()
	if err != nil {
		t.Errorf(err.Error())
		return nil, err
	}
	k8sUtils := k8smock.Init()
	_, err = k8sUtils.CreateNewCertSecret("secret-cert")
	if err != nil {
		return nil, err
	}

	proxyConfig, err := config.NewProxyConfigFromSecret(proxySecret, k8sUtils)
	if err != nil {
		return nil, err
	}
	return proxyConfig, nil
}

func TestNewProxy(t *testing.T) {
	// Get the ProxyConfig using the getProxyConfigFromSecret function
	proxyConfig, err := getProxyConfigFromSecret(t)
	if err != nil {
		t.Errorf("Failed to get proxy config: %v", err)
		return
	}

	// Create a new Proxy using the ProxyConfig
	proxy, err := NewProxy(*proxyConfig)
	if err != nil {
		t.Errorf("Failed to create new proxy: %v", err)
		return
	}

	// Validate the envoyClients in Proxy.envoyMap
	for arrayID, envoy := range proxy.envoyMap {
		// Check if the envoy client is not nil
		if envoy == nil {
			t.Errorf("Envoy client for array ID %s is nil", arrayID)
			continue
		}

		// Check if the primary and backup envoy clients are set correctly
		if envoy.GetPrimary() == nil {
			t.Errorf("Primary envoy client for array ID %s is nil", arrayID)
		}
		if envoy.GetBackup() == nil {
			t.Errorf("Backup envoy client for array ID %s is nil", arrayID)
		}
		if _, err := proxy.getHTTPClient(arrayID); err != nil {
			t.Errorf("Active HTTP client for array ID %s is nil", arrayID)
		}
		if _, err := proxy.getProxyBySymmID(arrayID); err != nil {
			t.Errorf("Proxy for array ID %s is nil", arrayID)
		}
	}
}

func TestProxy_UpdateConfig(t *testing.T) {
	// Get the ProxyConfig using the getProxyConfigFromSecret function
	proxyConfig, err := getProxyConfigFromSecret(t)
	if err != nil {
		t.Errorf("Failed to get proxy config: %v", err)
		return
	}

	// Create a new Proxy using the ProxyConfig
	proxy, err := NewProxy(*proxyConfig)
	if err != nil {
		t.Errorf("Failed to create new proxy: %v", err)
		return
	}

	// Swap the primary and backup endpoints in the ProxyConfig
	for _, serverArray := range proxyConfig.GetManagedArraysAndServers() {
		if serverArray.BackupServer != nil {
			tmp := serverArray.PrimaryServer
			serverArray.PrimaryServer = *serverArray.BackupServer
			serverArray.BackupServer = &tmp
		}
	}

	// Update the Proxy with the updated ProxyConfig
	err = proxy.UpdateConfig(*proxyConfig)
	if err != nil {
		t.Errorf("Failed to update proxy config: %v", err)
		return
	}

	// Validate that the envoyClients in Proxy.envoyMap have been updated correctly
	for arrayID, envoy := range proxy.envoyMap {
		if envoy.GetPrimary() == nil {
			t.Errorf("Primary envoy client for array ID %s is nil", arrayID)
			continue
		}

		if envoy.GetBackup() == nil {
			t.Errorf("Backup envoy client for array ID %s is nil", arrayID)
			continue
		}

		/*// Check if the primary and backup envoy clients have been swapped
		if envoy.GetPrimary().URL.Host != proxyConfig.GetManagementServer(arrayID).PrimaryServer.Endpoint.Host {
			t.Errorf("Primary envoy client for array ID %s is not the primary server", arrayID)
		}

		if envoy.GetBackup().URL.Host != proxyConfig.GetManagementServer(arrayID).BackupServer.Endpoint.Host {
			t.Errorf("Backup envoy client for array ID %s is not the backup server", arrayID)
		}*/
	}
}
