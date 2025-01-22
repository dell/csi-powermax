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
	"crypto/subtle"
	"fmt"
	"net/url"
	"os"
	"reflect"

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/k8sutils"
	"revproxy/v2/pkg/utils"

	"github.com/mitchellh/mapstructure"
	log "github.com/sirupsen/logrus"

	"github.com/spf13/viper"
	corev1 "k8s.io/api/core/v1"
)

// StorageArrayConfig represents the configuration of a storage array in the config file
type StorageArrayConfig struct {
	StorageArrayID         string   `yaml:"storageArrayId"`
	PrimaryEndpoint        string   `yaml:"primaryURL"`
	BackupEndpoint         string   `yaml:"backupURL,omitempty"`
	ProxyCredentialSecrets []string `yaml:"proxyCredentialSecrets"`
}

// ProxyCredentialSecret is used for storing a credential for a secret
type ProxyCredentialSecret struct {
	Credentials      common.Credentials
	CredentialSecret string
}

// StorageArray represents a StorageArray (formed using StorageArrayConfig)
type StorageArray struct {
	StorageArrayIdentifier string
	PrimaryEndpoint        url.URL
	SecondaryEndpoint      url.URL
	ProxyCredentialSecrets map[string]ProxyCredentialSecret
}

// StorageArrayServer represents an array with its primary and backup management server
type StorageArrayServer struct {
	Array         StorageArray
	PrimaryServer ManagementServer
	BackupServer  *ManagementServer
}

func getEnv(envName, defaultValue string) string {
	envVal, found := os.LookupEnv(envName)
	if !found {
		envVal = defaultValue
	}
	return envVal
}

// DeepCopy - used for deep copy of StorageArray
func (sa *StorageArray) DeepCopy() *StorageArray {
	if sa == nil {
		return nil
	}
	cloned := StorageArray{}
	cloned = *sa
	cloned.ProxyCredentialSecrets = make(map[string]ProxyCredentialSecret, len(sa.ProxyCredentialSecrets))
	for secret, proxySecret := range sa.ProxyCredentialSecrets {
		cloned.ProxyCredentialSecrets[secret] = proxySecret
	}
	return &cloned
}

// ManagementServerConfig - represents a management server configuration for the management server
type ManagementServerConfig struct {
	Endpoint                  string        `yaml:"url"`
	ArrayCredentialSecret     string        `yaml:"arrayCredentialSecret,omitempty"`
	SkipCertificateValidation bool          `yaml:"skipCertificateValidation,omitempty"`
	CertSecret                string        `yaml:"certSecret,omitempty"`
	Limits                    common.Limits `yaml:"limits,omitempty" mapstructure:"limits"`
	Username                  string        `yaml:"username,omitempty"`
	Password                  string        `yaml:"password,omitempty"`
}

// ManagementServer - represents a Management Server (formed using ManagementServerConfig)
type ManagementServer struct {
	Endpoint                  url.URL
	StorageArrayIdentifiers   []string
	Credentials               common.Credentials
	CredentialSecret          string
	SkipCertificateValidation bool
	CertFile                  string
	CertSecret                string
	Limits                    common.Limits
	Username                  string
	Password                  string
}

// DeepCopy is used for creating a deep copy of Management Server
func (ms *ManagementServer) DeepCopy() *ManagementServer {
	clone := ManagementServer{}
	clone = *ms
	clone.StorageArrayIdentifiers = make([]string, len(ms.StorageArrayIdentifiers))
	copy(clone.StorageArrayIdentifiers, ms.StorageArrayIdentifiers)
	return &clone
}

// Config - represents proxy configuration in the config file
type Config struct {
	StorageArrayConfig     []StorageArrayConfig     `yaml:"storageArrays" mapstructure:"storageArrays"`
	ManagementServerConfig []ManagementServerConfig `yaml:"managementServers" mapstructure:"managementServers"`
}

// ProxyConfigMap - represents the configuration file
type ProxyConfigMap struct {
	Port      string  `yaml:"port,omitempty"`
	LogLevel  string  `yaml:"logLevel,omitempty"`
	LogFormat string  `yaml:"logFormat,omitempty"`
	Config    *Config `yaml:"config,omitempty" mapstructure:"config"`
}

// ProxySecret - represents the configuration file
type ProxySecret struct {
	StorageArrayConfig     []StorageArrayConfig     `yaml:"storageArrays" mapstructure:"storageArrays"`
	ManagementServerConfig []ManagementServerConfig `yaml:"managementServers" mapstructure:"managementServers"`
}

// ProxyConfig - represents Proxy Config (formed using ProxyConfigMap OR ProxySecret)
type ProxyConfig struct {
	Port              string
	managedArrays     map[string]*StorageArray
	managementServers map[url.URL]*ManagementServer
	proxyCredentials  map[string]*ProxyUser
}

// DeepCopy is used to create a deep copy of ProxyConfig
func (pc *ProxyConfig) DeepCopy() *ProxyConfig {
	if pc == nil {
		return nil
	}
	cloned := new(ProxyConfig)
	cloned.proxyCredentials = make(map[string]*ProxyUser)
	cloned.managementServers = make(map[url.URL]*ManagementServer)
	cloned.managedArrays = make(map[string]*StorageArray)
	cloned.Port = pc.Port
	for key, value := range pc.managedArrays {
		array := *value
		cloned.managedArrays[key] = &array
	}
	for key, value := range pc.managementServers {
		cloned.managementServers[key] = value.DeepCopy()
	}
	for key, value := range pc.proxyCredentials {
		creds := *value
		cloned.proxyCredentials[key] = &creds
	}
	return cloned
}

// Log - logs the Proxy Config
func (pc *ProxyConfig) Log() {
	log.Println("---------------------")
	log.Printf("port ::: %+s\n", pc.Port)
	log.Println("---------------------")
	log.Println("managedArrays")
	for key, val := range pc.managedArrays {
		log.Printf("%s ::: %+v\n", key, val)
	}
	log.Println("---------------------")
	log.Println("---------------------")
	log.Println("managementServers")
	for key, val := range pc.managementServers {
		log.Printf("%v ::: %+v\n", key, val)
	}
	log.Println("---------------------")
	log.Println("---------------------")
	log.Println("proxyCredentials")
	for key, val := range pc.proxyCredentials {
		log.Printf("%s ::: %+v\n", key, val)
	}
	log.Println("---------------------")
}

func (pc *ProxyConfig) updateProxyCredentials(creds common.Credentials, storageArrayIdentifier string) {
	if proxyUser, ok := pc.proxyCredentials[creds.UserName]; ok {
		if subtle.ConstantTimeCompare([]byte(creds.Password), []byte(proxyUser.ProxyCredential.Password)) == 1 {
			// Credentials already exist in map
			proxyUser.StorageArrayIdentifiers = utils.AppendIfMissingStringSlice(
				proxyUser.StorageArrayIdentifiers, storageArrayIdentifier)
		}
	} else {
		proxyUser := ProxyUser{
			ProxyCredential:         creds,
			StorageArrayIdentifiers: []string{storageArrayIdentifier},
		}
		pc.proxyCredentials[creds.UserName] = &proxyUser
	}
}

func (pc *ProxyConfig) updateProxyCredentialsFromSecret(username, password, storageArrayIdentifier string) {

	if proxyUser, ok := pc.proxyCredentials[username]; ok {
		if subtle.ConstantTimeCompare([]byte(password), []byte(proxyUser.ProxyCredential.Password)) == 1 {
			// Credentials already exist in map
			proxyUser.StorageArrayIdentifiers = utils.AppendIfMissingStringSlice(
				proxyUser.StorageArrayIdentifiers, storageArrayIdentifier)
		}
	} else {
		creds := &common.Credentials{
			UserName: username,
			Password: password,
		}
		proxyUser := ProxyUser{
			ProxyCredential:         *creds,
			StorageArrayIdentifiers: []string{storageArrayIdentifier},
		}
		pc.proxyCredentials[username] = &proxyUser
	}
}

// GetManagementServers - Returns the list of management servers present in ProxyConfig
func (pc *ProxyConfig) GetManagementServers() []ManagementServer {
	mgmtServers := make([]ManagementServer, 0)
	for _, v := range pc.managementServers {
		mgmtServers = append(mgmtServers, *v)
	}
	return mgmtServers
}

// GetManagedArraysAndServers returns a list of arrays with their corresponding management servers
func (pc *ProxyConfig) GetManagedArraysAndServers() map[string]StorageArrayServer {
	arrayServers := make(map[string]StorageArrayServer)
	for _, server := range pc.managementServers {
		for _, arrayID := range server.StorageArrayIdentifiers {
			var (
				arrayServer StorageArrayServer
				ok          bool
			)
			if arrayServer, ok = arrayServers[arrayID]; !ok {
				arrayServer = StorageArrayServer{}
				arrayServer.Array = *(pc.managedArrays[arrayID])
			}
			if server.Endpoint == arrayServer.Array.PrimaryEndpoint {
				arrayServer.PrimaryServer = *(server)
			} else if server.Endpoint == arrayServer.Array.SecondaryEndpoint {
				arrayServer.BackupServer = server
			}
			arrayServers[arrayID] = arrayServer
		}
	}
	return arrayServers
}

// IsSecretConfiguredForCerts - returns true if the given secret name is configured for certificates
func (pc *ProxyConfig) IsSecretConfiguredForCerts(secretName string) bool {
	found := false
	for _, server := range pc.managementServers {
		if server.CertSecret == secretName {
			found = true
			break
		}
	}
	return found
}

// IsSecretConfiguredForArrays - returns true if a given secret name has been configured
// as credential secret for a storage array
func (pc *ProxyConfig) IsSecretConfiguredForArrays(secretName string) bool {

	log.Infof("Checking secret : %s", secretName)
	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" && secretName == common.DefaultReverseProxySecretName {
		//TODO: Need to check if rray.ProxyCredentialSecrets[secretName].CredentialSecret.Username and password are set or not.
		return true
	}

	for _, array := range pc.managedArrays {
		if array.ProxyCredentialSecrets[secretName].CredentialSecret == secretName {
			return true
		}
	}
	return false
}

// UpdateCerts - Given a secret name and cert file name, updates the management server
func (pc *ProxyConfig) UpdateCerts(secretName, certFileName string) bool {
	isUpdated := false
	for _, server := range pc.managementServers {
		if server.CertSecret == secretName {
			server.CertFile = certFileName
			isUpdated = true
		}
	}
	return isUpdated
}

// UpdateCreds - Given a secret name and credentials, updates management servers and storage array
// proxy credentials
func (pc *ProxyConfig) UpdateCreds(secretName string, credentials *common.Credentials) bool {
	isUpdated := false

	for _, server := range pc.managementServers {
		// TODO: Check if there is a better way to check for this
		if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" && secretName == common.DefaultReverseProxySecretName {
			server.Username = credentials.UserName
			server.Password = credentials.Password
			isUpdated = true
		} else {
			if server.CredentialSecret == secretName {
				if server.Credentials != *credentials {
					server.Credentials = *credentials
					isUpdated = true
				}
			}
		}
	}

	for _, array := range pc.managedArrays {
		if array.ProxyCredentialSecrets[secretName].CredentialSecret == secretName {
			if array.ProxyCredentialSecrets[secretName].Credentials != *credentials {
				storageArrayIdentifiers := pc.proxyCredentials[array.ProxyCredentialSecrets[secretName].Credentials.UserName].StorageArrayIdentifiers
				delete(pc.proxyCredentials, array.ProxyCredentialSecrets[secretName].Credentials.UserName)
				newProxyCredentialSecret := &ProxyCredentialSecret{
					Credentials:      *credentials,
					CredentialSecret: secretName,
				}
				array.ProxyCredentialSecrets[secretName] = *newProxyCredentialSecret
				pc.proxyCredentials[credentials.UserName] = &ProxyUser{
					ProxyCredential:         *credentials,
					StorageArrayIdentifiers: storageArrayIdentifiers,
				}
				isUpdated = true
			}
		}
	}
	return isUpdated
}

// UpdateCertsAndCredentials - Updates certs and credentials given a secret and returns a boolean to indicate if any
// updates was done
func (pc *ProxyConfig) UpdateCertsAndCredentials(k8sUtils k8sutils.UtilsInterface, secret *corev1.Secret) (bool, error) {
	hasChanged := false
	for _, server := range pc.managementServers {
		if server.CredentialSecret == secret.Name {
			credentials, err := k8sUtils.GetCredentialsFromSecret(secret)
			if err != nil {
				return hasChanged, err
			}
			if server.Credentials != *credentials {
				server.Credentials = *credentials
				hasChanged = true
			}
		} else if server.CertSecret == secret.Name {
			certFile, err := k8sUtils.GetCertFileFromSecret(secret)
			if err != nil {
				return hasChanged, err
			}
			server.CertFile = certFile
			hasChanged = true
		}
	}
	for _, array := range pc.managedArrays {
		if array.ProxyCredentialSecrets[secret.Name].CredentialSecret == secret.Name {
			credentials, err := k8sUtils.GetCredentialsFromSecret(secret)
			if err != nil {
				return hasChanged, err
			}
			if array.ProxyCredentialSecrets[secret.Name].Credentials != *credentials {
				storageArrayIdentifiers := pc.proxyCredentials[array.ProxyCredentialSecrets[secret.Name].Credentials.UserName].StorageArrayIdentifiers
				delete(pc.proxyCredentials, array.ProxyCredentialSecrets[secret.Name].Credentials.UserName)
				newProxyCredentialSecret := &ProxyCredentialSecret{
					Credentials:      *credentials,
					CredentialSecret: secret.Name,
				}
				array.ProxyCredentialSecrets[secret.Name] = *newProxyCredentialSecret
				pc.proxyCredentials[credentials.UserName] = &ProxyUser{
					ProxyCredential:         *credentials,
					StorageArrayIdentifiers: storageArrayIdentifiers,
				}
				hasChanged = true
			}
		}
	}
	return hasChanged, nil
}

// UpdateManagementServers - Updates the list of management servers
func (pc *ProxyConfig) UpdateManagementServers(config *ProxyConfig) ([]ManagementServer, []ManagementServer, error) {
	deletedManagementServers := make([]ManagementServer, 0)
	updatedManagemetServers := make([]ManagementServer, 0)
	// Check for deleted management servers, if any delete the map entry for it
	for url, server := range pc.managementServers {
		if _, ok := config.managementServers[url]; !ok {
			// the management server is not present in new config, delete its entry from active config
			deletedManagementServers = append(deletedManagementServers, *server)
			delete(pc.managementServers, url)
		}
	}
	// Check for adding/updating a new/existing management server, if any
	for url, mgmntServer := range config.managementServers {
		if _, ok := pc.managementServers[url]; !ok {
			// Not found, the management server is not present in active config so add its entry
			updatedManagemetServers = append(updatedManagemetServers, *mgmntServer)
			pc.managementServers[url] = config.managementServers[url]
		} else {
			// Found, update the map if management server is updated
			if !reflect.DeepEqual(pc.managementServers[url], config.managementServers[url]) {
				updatedManagemetServers = append(updatedManagemetServers, *mgmntServer)
				pc.managementServers[url] = config.managementServers[url]
			}
		}
	}
	if !reflect.DeepEqual(pc.managementServers, config.managementServers) {
		return nil, nil, fmt.Errorf("something wrong in adding/removing servers")
	}
	return deletedManagementServers, updatedManagemetServers, nil
}

// UpdateManagedArrays - updates the set of managed arrays
func (pc *ProxyConfig) UpdateManagedArrays(config *ProxyConfig) {
	if !reflect.DeepEqual(pc.managedArrays, config.managedArrays) {
		log.Info("Detected changes, updating managed array config")
		pc.managedArrays = config.managedArrays
		pc.proxyCredentials = config.proxyCredentials
	}
}

// GetAuthorizedArrays - Given a credential, returns the list of authorized storage arrays
func (pc *ProxyConfig) GetAuthorizedArrays(username, password string) []string {
	authorizedArrays := make([]string, 0)
	var isAuth bool
	var err error
	for _, array := range pc.managedArrays {
		for _, mgmtserver := range pc.managementServers {
			if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
				log.Printf("Fetch authorized arrays based on secret\n")
				isAuth, err = pc.IsUserAuthorized(mgmtserver.Username, mgmtserver.Password, array.StorageArrayIdentifier)
			} else {
				isAuth, err = pc.IsUserAuthorized(username, password, array.StorageArrayIdentifier)
			}

			if err != nil {
				log.Errorf("error : (%s)", err.Error())
			}
			if isAuth {
				authorizedArrays = append(authorizedArrays, array.StorageArrayIdentifier)
			}
		}
	}
	return authorizedArrays
}

// GetManagementServerCredentials - Given a management server URL, returns the associated credentials
func (pc *ProxyConfig) GetManagementServerCredentials(mgmtURL url.URL) (common.Credentials, error) {
	// TODO: Review commented code and refactor
	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
		var arrayCredentials common.Credentials
		arrayCredentials.UserName = pc.managementServers[mgmtURL].Username
		arrayCredentials.Password = pc.managementServers[mgmtURL].Password
		if _, ok := pc.managementServers[mgmtURL]; ok {
			return arrayCredentials, nil
		}
	}

	if mgmtServer, ok := pc.managementServers[mgmtURL]; ok {
		return mgmtServer.Credentials, nil
	}
	return common.Credentials{}, fmt.Errorf("url not configured")
}

// IsUserAuthorized - Returns if a given user is authorized to access a specific storage array
func (pc *ProxyConfig) IsUserAuthorized(username, password, storageArrayID string) (bool, error) {
	if creds, ok := pc.proxyCredentials[username]; ok {
		if utils.IsStringInSlice(creds.StorageArrayIdentifiers, storageArrayID) {
			if subtle.ConstantTimeCompare([]byte(creds.ProxyCredential.Password), []byte(password)) == 1 {
				return true, nil
			}
			return false, fmt.Errorf("incorrect password")
		}
		return false, fmt.Errorf("unauthorized for this array")
	}
	return false, fmt.Errorf("unknown credentials")
}

// GetStorageArray - Returns a list of storage array given a storage array id
func (pc *ProxyConfig) GetStorageArray(storageArrayID string) []StorageArray {
	storageArrays := make([]StorageArray, 0)
	if storageArrayID != "" {
		if storageArray, ok := pc.managedArrays[storageArrayID]; ok {
			storageArrays = append(storageArrays, *storageArray)
		}
	} else {
		for _, v := range pc.managedArrays {
			storageArrays = append(storageArrays, *v)
		}
	}
	return storageArrays
}

// GetManagementServer returns a management server corresponding to a URL
func (pc *ProxyConfig) GetManagementServer(url url.URL) (ManagementServer, bool) {
	if server, ok := pc.managementServers[url]; ok {
		return *server, ok
	}
	return ManagementServer{}, false
}

// ProxyUser - used for storing a proxy user and list of associated storage array identifiers
type ProxyUser struct {
	StorageArrayIdentifiers []string
	ProxyCredential         common.Credentials
	//Username                string
	// Password                string
}

// DeepClone is used to create a deep copy of Proxy User
func (pu *ProxyUser) DeepClone() *ProxyUser {
	if pu == nil {
		return nil
	}
	cloned := ProxyUser{}
	cloned = *pu
	cloned.StorageArrayIdentifiers = make([]string, len(pu.StorageArrayIdentifiers))
	copy(cloned.StorageArrayIdentifiers, pu.StorageArrayIdentifiers)
	return &cloned
}

// ParseConfig - Parses a given proxy config map
func (pc *ProxyConfig) ParseConfig(proxyConfigMap ProxyConfigMap, k8sUtils k8sutils.UtilsInterface) error {
	pc.Port = proxyConfigMap.Port
	config := proxyConfigMap.Config
	if config == nil {
		return fmt.Errorf("config is empty")
	}
	pc.managedArrays = make(map[string]*StorageArray)
	pc.managementServers = make(map[url.URL]*ManagementServer)
	pc.proxyCredentials = make(map[string]*ProxyUser)
	storageArrayIdentifiers := make(map[url.URL][]string)
	ipAddresses := make([]string, 0)

	for _, mgmtServer := range config.ManagementServerConfig {
		ipAddresses = append(ipAddresses, mgmtServer.Endpoint)
	}
	for _, array := range config.StorageArrayConfig {
		if array.PrimaryEndpoint == "" {
			return fmt.Errorf("primary endpoint not configured for array: %s", array.StorageArrayID)
		}
		if !utils.IsStringInSlice(ipAddresses, array.PrimaryEndpoint) {
			return fmt.Errorf("primary endpoint: %s for array: %s not present among management URL addresses",
				array.PrimaryEndpoint, array)
		}
		if array.BackupEndpoint != "" {
			if !utils.IsStringInSlice(ipAddresses, array.BackupEndpoint) {
				return fmt.Errorf("backup endpoint: %s for array: %s is not in the list of management URL addresses. Ignoring it",
					array.BackupEndpoint, array)
			}
		}
		primaryURL, err := url.Parse(array.PrimaryEndpoint)
		if err != nil {
			return err
		}
		backupURL := &url.URL{}
		if array.BackupEndpoint != "" {
			backupURL, err = url.Parse(array.BackupEndpoint)
			if err != nil {
				return err
			}
		}
		pc.managedArrays[array.StorageArrayID] = &StorageArray{
			StorageArrayIdentifier: array.StorageArrayID,
			PrimaryEndpoint:        *primaryURL,
			SecondaryEndpoint:      *backupURL,
		}
		// adding Primary and Backup URl to storageArrayIdentifier, later to be used in management server
		storageArrayIdentifiers[*primaryURL] = append(storageArrayIdentifiers[*primaryURL], array.StorageArrayID)
		storageArrayIdentifiers[*backupURL] = append(storageArrayIdentifiers[*backupURL], array.StorageArrayID)

		// Reading proxy credentials for the array
		if len(array.ProxyCredentialSecrets) > 0 {
			pc.managedArrays[array.StorageArrayID].ProxyCredentialSecrets = make(map[string]ProxyCredentialSecret)
			for _, secret := range array.ProxyCredentialSecrets {
				proxyCredentials, err := k8sUtils.GetCredentialsFromSecretName(secret)
				if err != nil {
					return err
				}

				proxyCredentialSecret := &ProxyCredentialSecret{
					Credentials:      *proxyCredentials,
					CredentialSecret: secret,
				}

				pc.managedArrays[array.StorageArrayID].ProxyCredentialSecrets[secret] = *proxyCredentialSecret
				pc.updateProxyCredentials(*proxyCredentials, array.StorageArrayID)
			}
		}
	}
	for _, managementServer := range config.ManagementServerConfig {
		var arrayCredentials common.Credentials
		if managementServer.ArrayCredentialSecret != "" {
			credentials, err := k8sUtils.GetCredentialsFromSecretName(managementServer.ArrayCredentialSecret)
			if err != nil {
				return err
			}
			arrayCredentials = *credentials
		}
		mgmtURL, err := url.Parse(managementServer.Endpoint)
		if err != nil {
			return err
		}
		var certFile string
		if managementServer.CertSecret != "" {
			certFile, err = k8sUtils.GetCertFileFromSecretName(managementServer.CertSecret)
			if err != nil {
				return err
			}
		}
		pc.managementServers[*mgmtURL] = &ManagementServer{
			Endpoint:                  *mgmtURL,
			StorageArrayIdentifiers:   storageArrayIdentifiers[*mgmtURL],
			SkipCertificateValidation: managementServer.SkipCertificateValidation,
			CertFile:                  certFile,
			CertSecret:                managementServer.CertSecret,
			Credentials:               arrayCredentials,
			CredentialSecret:          managementServer.ArrayCredentialSecret,
			Limits:                    managementServer.Limits,
		}
	}
	return nil
}

// ParseConfigFromSecret - Parses a given proxy secret
func (pc *ProxyConfig) ParseConfigFromSecret(proxySecret ProxySecret, k8sUtils k8sutils.UtilsInterface) error {
	// TODO: Need to get port information dynamically, Hardcoded for now
	pc.Port = "2222"
	log.Printf("Explicitly setting PORT to %s", pc.Port)

	pc.managedArrays = make(map[string]*StorageArray)
	pc.managementServers = make(map[url.URL]*ManagementServer)
	pc.proxyCredentials = make(map[string]*ProxyUser)
	storageArrayIdentifiers := make(map[url.URL][]string)
	ipAddresses := make([]string, 0)
	for _, mgmtServer := range proxySecret.ManagementServerConfig {
		ipAddresses = append(ipAddresses, mgmtServer.Endpoint)
	}
	for _, array := range proxySecret.StorageArrayConfig {
		if array.PrimaryEndpoint == "" {
			return fmt.Errorf("primary endpoint not configured for array: %s", array.StorageArrayID)
		}
		if !utils.IsStringInSlice(ipAddresses, array.PrimaryEndpoint) {
			return fmt.Errorf("primary endpoint: %s for array: %s not present among management URL addresses",
				array.PrimaryEndpoint, array)
		}
		if array.BackupEndpoint != "" {
			if !utils.IsStringInSlice(ipAddresses, array.BackupEndpoint) {
				return fmt.Errorf("backup endpoint: %s for array: %s is not in the list of management URL addresses. Ignoring it",
					array.BackupEndpoint, array)
			}
		}
		primaryEndpoint, err := url.Parse(array.PrimaryEndpoint)
		if err != nil {
			return err
		}
		backupEndpoint := &url.URL{}
		if array.BackupEndpoint != "" {
			backupEndpoint, err = url.Parse(array.BackupEndpoint)
			if err != nil {
				return err
			}
		}
		pc.managedArrays[array.StorageArrayID] = &StorageArray{
			StorageArrayIdentifier: array.StorageArrayID,
			PrimaryEndpoint:        *primaryEndpoint,
			SecondaryEndpoint:      *backupEndpoint,
		}
		// adding Primary and Backup URl to storageArrayIdentifier, later to be used in management server
		storageArrayIdentifiers[*primaryEndpoint] = append(storageArrayIdentifiers[*primaryEndpoint], array.StorageArrayID)
		storageArrayIdentifiers[*backupEndpoint] = append(storageArrayIdentifiers[*backupEndpoint], array.StorageArrayID)

		// Reading proxy credentials for the array
		if len(array.ProxyCredentialSecrets) > 0 {
			pc.managedArrays[array.StorageArrayID].ProxyCredentialSecrets = make(map[string]ProxyCredentialSecret)
			for _, mgmtServer := range proxySecret.ManagementServerConfig {
				if array.PrimaryEndpoint == mgmtServer.Endpoint || array.BackupEndpoint == mgmtServer.Endpoint {
					pc.updateProxyCredentialsFromSecret(mgmtServer.Username, mgmtServer.Password, array.StorageArrayID)
				}
			}
		}
	}
	for _, managementServer := range proxySecret.ManagementServerConfig {
		mgmtEndpoint, err := url.Parse(managementServer.Endpoint)
		if err != nil {
			return err
		}
		var certFile string
		if managementServer.CertSecret != "" {
			certFile, err = k8sUtils.GetCertFileFromSecretName(managementServer.CertSecret)
			if err != nil {
				return err
			}
		}
		pc.managementServers[*mgmtEndpoint] = &ManagementServer{
			Endpoint:                  *mgmtEndpoint,
			StorageArrayIdentifiers:   storageArrayIdentifiers[*mgmtEndpoint],
			SkipCertificateValidation: managementServer.SkipCertificateValidation,
			CertFile:                  certFile,
			CertSecret:                managementServer.CertSecret,
			//Credentials:               arrayCredentials,
			CredentialSecret: managementServer.ArrayCredentialSecret,
			Limits:           managementServer.Limits,
			Username:         managementServer.Username,
			Password:         managementServer.Password,
		}
	}

	for _, array := range pc.managedArrays {
		primaryEndpoint := array.PrimaryEndpoint
		backupEndpoint := array.SecondaryEndpoint
		var primaryUsername, primaryPassword string
		var secondaryUsername, secondaryPassword string
		if primaryServer, ok := pc.managementServers[primaryEndpoint]; ok {
			primaryUsername = primaryServer.Username
			primaryPassword = primaryServer.Password
		} else {
			return fmt.Errorf("incorrect primary URL")
		}

		if secondaryServer, ok := pc.managementServers[backupEndpoint]; ok {
			secondaryUsername = secondaryServer.Username
			secondaryPassword = secondaryServer.Password

		} else {
			return fmt.Errorf("incorrect secondary URL")
		}
		pc.updateProxyCredentialsFromSecret(primaryUsername, primaryPassword, array.StorageArrayIdentifier)
		pc.updateProxyCredentialsFromSecret(secondaryUsername, secondaryPassword, array.StorageArrayIdentifier)

	}
	return nil
}

// NewProxyConfig - returns a new proxy config given a proxy config map
func NewProxyConfig(configMap *ProxyConfigMap, k8sUtils k8sutils.UtilsInterface) (*ProxyConfig, error) {
	var proxyConfig ProxyConfig
	err := proxyConfig.ParseConfig(*configMap, k8sUtils)
	if err != nil {
		return nil, err
	}
	return &proxyConfig, nil
}

// Unmarshal - Custom unmarshal function for config map
/* 	Since the common ManagementServerConfig and StorageArrayConfig have been modified to use Endpoint instead of URL,
	viper has an issue unmarshalling those in to the struct for ConfigMap which uses URLs still. Hence a custom function is needed
 	to make sure that they are parsed correctly.
*/
func (c *ProxyConfigMap) Unmarshal() error {
	settings := viper.AllSettings()

	log.Infof("viper all settings: %v\n", viper.AllSettings())

	// Retrieve all settings as a map
	// Custom handling for URL fields before unmarshaling
	for i, managementServer := range viper.Get("config.managementservers").([]interface{}) {
		serverMap := managementServer.(map[interface{}]interface{})
		// Check if the "url" field exists (mapped from "url" in the YAML) and is a string
		if urlStr, ok := serverMap["url"].(string); ok {
			parsedURL, err := url.Parse(urlStr)
			if err != nil {
				return fmt.Errorf("invalid URL for url: %w", err)
			}
			// Update the map with the parsed URL
			c.Config.ManagementServerConfig[i].Endpoint = parsedURL.String()
		}
	}

	for i, storageArray := range viper.Get("config.storagearrays").([]interface{}) {
		arrayMap := storageArray.(map[interface{}]interface{})
		// Parse the primaryurl and backupurl to see if they exist
		if urlStr, ok := arrayMap["primaryURL"].(string); ok {
			parsedURL, err := url.Parse(urlStr)
			if err != nil {
				return fmt.Errorf("invalid URL for primaryEndpoint: %w", err)
			}
			c.Config.StorageArrayConfig[i].PrimaryEndpoint = parsedURL.String()
		}
		if urlStr, ok := arrayMap["backupURL"].(string); ok {
			parsedURL, err := url.Parse(urlStr)
			if err != nil {
				return fmt.Errorf("invalid URL for backupEndpoint: %w", err)
			}
			c.Config.StorageArrayConfig[i].BackupEndpoint = parsedURL.String()
		}
	}

	// Unmarshal the updated settings into the config struct
	return mapstructure.Decode(settings, c)
}

// ReadConfig - uses viper to read the config from the config map
func ReadConfig(configFile, configPath string) (*ProxyConfigMap, error) {
	viper := viper.New()
	viper.SetConfigName(configFile)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(configPath)
	// viper.SetEnvPrefix("X_CSI_REVPROXY")
	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}
	log.Println(viper.AllSettings())
	var configMap ProxyConfigMap
	err = viper.Unmarshal(&configMap)
	if err != nil {
		return nil, err
	}

	err = configMap.Unmarshal()
	if err != nil {
		return nil, err
	}
	return &configMap, nil
}

// NewProxyConfigFromSecret - new config using secret
func NewProxyConfigFromSecret(proxySecret *ProxySecret, k8sUtils k8sutils.UtilsInterface) (*ProxyConfig, error) {
	var proxyConfig ProxyConfig
	err := proxyConfig.ParseConfigFromSecret(*proxySecret, k8sUtils)
	if err != nil {
		return nil, err
	}
	return &proxyConfig, nil
}

// ReadConfigFromSecret - read config using secret
func ReadConfigFromSecret(secretFileName, secretFilePath string) (*ProxySecret, error) {
	log.Printf("Reading secret: %s from path: %s \n", secretFileName, secretFilePath)
	viper := viper.New()
	viper.SetConfigName(secretFileName)
	viper.SetConfigType("yaml")
	viper.AddConfigPath(secretFilePath)
	err := viper.ReadInConfig()
	if err != nil {
		return nil, err
	}
	var secret ProxySecret
	err = viper.Unmarshal(&secret)
	if err != nil {
		return nil, err
	}
	return &secret, nil
}
