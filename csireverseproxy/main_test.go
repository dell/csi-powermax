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

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/config"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8smock"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8sutils"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/servermock"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	"github.com/kubernetes-csi/csi-lib-utils/leaderelection"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"gopkg.in/yaml.v2"
)

type mockServer struct {
	server      *httptest.Server
	certificate *x509.Certificate
}

func (mock *mockServer) certPEMBytes() []byte {
	certPEM := new(bytes.Buffer)
	pem.Encode(certPEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: mock.certificate.Raw,
	})
	return certPEM.Bytes()
}

const (
	tmpSAConfigFile           = "sa-config-test-main.yaml"
	authenticationEndpoint    = "/univmax/authenticate"
	timeoutEndpoint           = "/univmax/timeout"
	defaultEndpoint           = "/univmax"
	primaryCertSecretName     = "cert-secret-4"
	backupCertSecretName      = "cert-secret-9"
	proxySecretName           = "proxy-secret-1"
	proxySecretName2          = "proxy-secret-2"
	storageArrayID            = "000000000001"
	primaryPort               = "9104"
	backupPort                = "9109"
	skipPrimaryCertValidation = false
	skipBackupCertValidation  = false
	mgmtServerUsername        = "admin"
	mgmtServerPassword        = "password"
	paramsConfigMapFile       = "config-params.yaml"
)

var (
	server                              *Server
	primaryMockServer, backupMockServer *mockServer
	httpClient                          *http.Client
)

func startTestServer(config string) error {
	if server != nil {
		return nil
	}
	var err error
	k8sUtils := k8smock.Init()
	os.Setenv(common.EnvInClusterConfig, "true")
	serverOpts := getServerOpts()
	serverOpts.ConfigDir = common.TempConfigDir
	// Create test proxy config and start the server
	serverOpts.ConfigFileName = tmpSAConfigFile

	if config == "secret" {
		log.Infof("Reverse proxy reading mounted secret")
		os.Setenv(common.EnvReverseProxyUseSecret, "true")
		os.Setenv(common.EnvSecretFilePath, common.TempConfigDir+"/"+tmpSAConfigFile)
		os.Setenv(common.EnvPowermaxConfigPath, common.TestConfigDir+"/"+paramsConfigMapFile)
		err = createTempSecret()
		if err != nil {
			return err
		}
	} else {
		log.Infof("Reverse proxy reading mounted config map")
		os.Setenv(common.EnvReverseProxyUseSecret, "false")
		err := createTempConfig()
		if err != nil {
			return err
		}
	}
	_, err = k8sUtils.CreateNewCredentialSecret(proxySecretName)
	if err != nil {
		return err
	}

	_, err = k8sUtils.CreateNewCredentialSecret(proxySecretName2)
	if err != nil {
		return err
	}
	_, err = k8sUtils.CreateNewCredentialSecret(primaryCertSecretName)
	if err != nil {
		return err
	}
	_, err = k8sUtils.CreateNewCertSecret(primaryCertSecretName)
	if err != nil {
		return err
	}
	_, err = k8sUtils.CreateNewCertSecret(backupCertSecretName)
	if err != nil {
		return err
	}
	server, err = startServer(k8sUtils, serverOpts)
	if err == nil {
		log.Printf("started revproxy server successfully on port %s", serverOpts.Port)
	}

	return err
}

func getURL(port, path string) string {
	if path[0] != '/' {
		path = "/" + path
	}
	return fmt.Sprintf("https://127.0.0.1:%s%s", port, path)
}

func getHTTPClient() *http.Client {
	if httpClient != nil {
		return httpClient
	}
	tlsConfig := tls.Config{
		InsecureSkipVerify: true,
	}
	tr := &http.Transport{
		TLSClientConfig:     &tlsConfig,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
	}
	httpClient = &http.Client{Transport: tr}
	return httpClient
}

func createMockServer(port string) (*mockServer, error) {
	handler := servermock.GetHandler()
	testServer := httptest.NewUnstartedServer(handler)
	testServer.Listener.Close()
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		return nil, err
	}
	testServer.Listener = listener
	testServer.StartTLS()
	certificate := testServer.Certificate()
	return &mockServer{
		server:      testServer,
		certificate: certificate,
	}, nil
}

func doHTTPRequest(port, path string) (string, error) {
	client := getHTTPClient()
	req, err := http.NewRequest("GET", getURL(port, path), nil)
	if err != nil {
		return "", err
	}
	creds := common.Credentials{
		UserName: "test-username",
		Password: "test-password",
	}
	req.Header.Set("Authorization", utils.BasicAuth(creds))

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

func runRequestLoop(count int, duration time.Duration, port, path string) error {
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := doHTTPRequest(port, path)
			if err != nil {
				log.Error(err.Error())
			}
		}(i)
	}
	wg.Wait()
	time.Sleep(duration)
	_, err := doHTTPRequest(port, path)
	if err != nil {
		log.Error(err.Error())
	}
	return nil
}

func stopServers() {
	if primaryMockServer != nil {
		primaryMockServer.server.Close()
	}
	if backupMockServer != nil {
		backupMockServer.server.Close()
	}
	if server != nil {
		server.SigChan <- syscall.SIGHUP
		server = nil
	}
}

func readYAMLConfig(filename, fileDir string) (config.ProxyConfigMap, error) {
	configmap := config.ProxyConfigMap{}
	file := filepath.Join(fileDir, filename)
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return configmap, err
	}
	err = yaml.Unmarshal(yamlFile, &configmap)
	if err != nil {
		return configmap, err
	}
	return configmap, nil
}

func readYAMLConfigParams(filename, fileDir string) (config.ParamsConfigMap, error) {
	configmap := config.ParamsConfigMap{}
	file := filepath.Join(fileDir, filename)
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return configmap, err
	}
	err = yaml.Unmarshal(yamlFile, &configmap)
	if err != nil {
		return configmap, err
	}
	return configmap, nil
}

func readYAMLSecret(filename, fileDir string) (config.ProxySecret, error) {
	secret := config.ProxySecret{}
	file := filepath.Join(fileDir, filename)
	yamlFile, err := os.ReadFile(file)
	if err != nil {
		return secret, err
	}
	err = yaml.Unmarshal(yamlFile, &secret)
	if err != nil {
		return secret, err
	}
	return secret, nil
}

func writeYAMLConfig(val interface{}, fileName, fileDir string) error {
	file, err := yaml.Marshal(&val)
	if err != nil {
		return err
	}
	filepath := filepath.Join(fileDir, fileName)
	return os.WriteFile(filepath, file, 0o777)
}

func createTempConfig() error {
	proxyConfigMap, err := readYAMLConfig(common.TestConfigFileName, common.TestConfigDir)
	if err != nil {
		log.Fatalf("Failed to read sample config file. (%s)", err.Error())
		return err
	}

	proxyConfigMap.Port = common.DefaultPort
	// Create a ManagementServerConfig
	tempMgmtServerConfig := createTempManagementServers()
	proxyConfigMap.Config.ManagementServerConfig = tempMgmtServerConfig
	// Create a StorageArrayConfig
	tempStorageArrayConfig := createTempStorageArrays()
	proxyConfigMap.Config.StorageArrayConfig = tempStorageArrayConfig
	err = writeYAMLConfig(proxyConfigMap, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		log.Fatalf("Failed to create a temporary config file. (%s)", err.Error())
	}
	return err
}

func createTempSecret() error {
	proxySecret, err := readYAMLSecret(common.TestSecretFileName, common.TestConfigDir)
	if err != nil {
		log.Fatalf("Failed to read secret (%s)", err.Error())
		return err
	}

	// Create a ManagementServerConfig
	tempMgmtServerConfig := createTempManagementServers()
	proxySecret.ManagementServerConfig = tempMgmtServerConfig
	// Create a StorageArrayConfig
	tempStorageArrayConfig := createTempStorageArrays()
	proxySecret.StorageArrayConfig = tempStorageArrayConfig
	err = writeYAMLConfig(proxySecret, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		log.Fatalf("Failed to create a temporary config file. (%s)", err.Error())
	}
	return err
}

func createTempStorageArrays() []config.StorageArrayConfig {
	tempStorageArrayConfig := []config.StorageArrayConfig{
		{
			PrimaryEndpoint:        getURL(primaryPort, "/"),
			BackupEndpoint:         getURL(backupPort, "/"),
			StorageArrayID:         storageArrayID,
			ProxyCredentialSecrets: []string{proxySecretName},
		},
	}

	if getEnv(common.EnvReverseProxyUseSecret, "false") == "false" {
		for i := 0; i < len(tempStorageArrayConfig); i++ {
			tempStorageArrayConfig[i].ProxyCredentialSecrets = []string{proxySecretName}
		}
	}
	return tempStorageArrayConfig
}

func createTempManagementServers() []config.ManagementServerConfig {
	// Create a primary management server
	primaryMgmntServer := config.ManagementServerConfig{
		Endpoint:                  getURL(primaryPort, "/"),
		SkipCertificateValidation: skipPrimaryCertValidation,
	}

	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		primaryMgmntServer.SkipCertificateValidation = true
	} else {
		primaryMgmntServer.CertSecret = primaryCertSecretName
	}

	// Create a backup management server
	backupMgmntServer := config.ManagementServerConfig{
		Endpoint:                  getURL(backupPort, "/"),
		SkipCertificateValidation: skipBackupCertValidation,
	}

	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		backupMgmntServer.SkipCertificateValidation = true
	} else {
		backupMgmntServer.CertSecret = primaryCertSecretName
	}

	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
		primaryMgmntServer.Username = mgmtServerUsername
		primaryMgmntServer.Password = mgmtServerPassword
		backupMgmntServer.Username = mgmtServerUsername
		backupMgmntServer.Password = mgmtServerPassword
	} else {
		primaryMgmntServer.ArrayCredentialSecret = proxySecretName
		backupMgmntServer.ArrayCredentialSecret = proxySecretName
	}

	tempMgmtServerConfig := []config.ManagementServerConfig{primaryMgmntServer, backupMgmntServer}
	return tempMgmtServerConfig
}

// Initializes test setup, sets up managements server, and starts proxy server.
func InitializeSetup(config string) {
	var err error

	// Start the mock server
	log.Info("Creating primary mock server...")
	primaryMockServer, err = createMockServer(primaryPort)
	if err != nil {
		log.Fatalf("Failed to create primary mock server. (%s)", err.Error())
		os.Exit(1)
	}
	log.Infof("Primary mock server listening on %s", primaryMockServer.server.URL)
	log.Info("Creating backup mock server...")
	backupMockServer, err = createMockServer(backupPort)
	if err != nil {
		log.Fatalf("Failed to create backup mock server. (%s)", err.Error())
		stopServers()
		os.Exit(1)
	}
	log.Infof("Backup mock server listening on %s", backupMockServer.server.URL)
	// Start proxy server and other services
	log.Info("Starting proxy server...")
	err = startTestServer(config)
	if err != nil {
		log.Fatalf("Failed to start proxy server. (%s)", err.Error())
		stopServers()
		os.Exit(1)
	}

	err = serverReady()
	if err != nil {
		log.Fatalf("Failed to start proxy server. (%s)", err.Error())
		stopServers()
		os.Exit(1)
	}
	log.Info("Proxy server started successfully")
}

func TearDownSetup() {
	log.Info("Stopping the mock and proxy servers")
	stopServers()
	log.Info("Removing the certs, temp files")
	err := utils.RemoveTempFiles()
	if err != nil {
		log.Fatalln(err.Error())
		os.Exit(1)
	}
	log.Info("Proxy and mock servers stopped successfully")
}

func TestMultipleRuns(t *testing.T) {
	// Remove any files left over from previous runs to ensure clean run
	TearDownSetup()

	// Run the tests multiple times with different configurations (secret or configmap)
	t.Run("TestSecretCase", func(t *testing.T) {
		log.Infof("#### Running tests with secret ####")
		os.Setenv(common.EnvReverseProxyUseSecret, "true")
		InitializeSetup("secret")
		defer TearDownSetup()

		// Run tests
		SAHTTPRequestTest(t)
		ServerSAEventHandlerTest(t)
		SecretHandlerTest(t)
		CMHandlerTest(t)
		CMParamsHandlerTest(t)
		log.Infof("#### END Running tests with secret ####")
	})

	t.Run("TestConfigMapCase", func(t *testing.T) {
		log.Infof("#### Running tests with configmap ####")
		InitializeSetup("configmap")
		defer TearDownSetup()
		os.Setenv(common.EnvReverseProxyUseSecret, "false")

		// Run tests
		SAHTTPRequestTest(t)
		ServerSAEventHandlerTest(t)
		SecretHandlerTest(t)
		CMHandlerTest(t)
		CMParamsHandlerTest(t)
		log.Infof("#### END Running tests with configmap ####")
	})
}

func serverReady() error {
	client := getHTTPClient()

	url := getURL(server.Port, "/")
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	responseCh := make(chan *http.Response)
	errorCh := make(chan error)

	go func() {
		resp, err := client.Get(url)
		if err != nil {
			errorCh <- err
			return
		}
		responseCh <- resp
	}()

	select {
	case resp := <-responseCh:
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			// server is ready to accept requests
			return nil
		}
	case err := <-errorCh:
		fmt.Println("Error:", err)
	case <-time.After(time.Second * 10):
		return errors.New("timeout: server is not ready to accept requests")
	}
	return nil
}

func TestServerStart(t *testing.T) {
	config := "configmap"
	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		config = "secret"
	}

	err := startTestServer(config)
	if err != nil {
		t.Error(err.Error())
	}
}

// Tests the configmap handler, needs a running server
func TestSignalHandlerOnConfigChange(t *testing.T) {
	log.Infof("Start CMHandlerTest")
	defer log.Infof("End CMHandlerTest")

	cm, err := readYAMLConfig(tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	cm.LogLevel = "Debug"
	cm.LogFormat = "json"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to write config")
		return
	}

	log.Infof("Start CMHandlerTest - update secret to 2")
	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "proxy-secret-2"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)

	log.Infof("Start CMHandlerTest - update secret to 1")

	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "proxy-secret-1"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)

	// Failure case, lets create a new mgmt server with bad URL.
	badManagementServer := config.ManagementServerConfig{
		ArrayCredentialSecret:     proxySecretName,
		Endpoint:                  "://example.com/@",
		SkipCertificateValidation: true,
	}

	originalConfig := cm.Config.ManagementServerConfig
	cm.Config.ManagementServerConfig = append(cm.Config.ManagementServerConfig, badManagementServer)
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}

	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)
	// Revert to original config for safety
	cm.Config.ManagementServerConfig = originalConfig
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}
}

func TestServer_EventHandler(t *testing.T) {
	k8sUtils := k8smock.Init()
	_, err := k8sUtils.CreateNewCertSecret(primaryCertSecretName)
	if err != nil {
		t.Error(err.Error())
	}
}

// Tests the server SA event handler, needs a running server.
// For tests marked as needs a running server, the following can be used to make them run independently
// 1. Rename the test function name to begin with Test
// 2. call InitializeSetup with the parameter "secret" or "configmap"
// 3. call TearDownSetup at the end (or defer)
func ServerSAEventHandlerTest(t *testing.T) {
	log.Infof("Start ServerSAEventHandlerTest")
	k8sUtils := k8smock.Init()
	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		return
	}

	oldProxySecret := server.Config().GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxySecretName,
			Namespace: common.DefaultNameSpace,
		},
		Data: map[string][]byte{
			"username": []byte("username"),
			"password": []byte("password"),
		},
		Type: "Generic",
	}
	server.EventHandler(k8sUtils, newSecret)
	newProxySecret := server.Config().GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
	if reflect.DeepEqual(oldProxySecret, newProxySecret) {
		t.Errorf("cert file should change after update")
	} else {
		fmt.Println("Secret Updated Successfully")
	}
	newSecret = &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxySecretName,
			Namespace: common.DefaultNameSpace,
		},
		Data: map[string][]byte{
			"username": []byte("test-username"),
			"password": []byte("test-password"),
		},
		Type: "Generic",
	}
	server.EventHandler(k8sUtils, newSecret)
	oldProxySecret = server.Config().GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
	if reflect.DeepEqual(oldProxySecret, newProxySecret) {
		t.Errorf("cert file should change after update")
	} else {
		fmt.Println("Secret Reverted Successfully")
	}

	log.Infof("End ServerSAEventHandlerTest")
}

// Tests the secret handler, needs a running server
func SecretHandlerTest(t *testing.T) {
	log.Infof("Start SecretHandlerTest")
	defer log.Infof("End SecretHandlerTest")

	if os.Getenv(common.EnvReverseProxyUseSecret) == "false" {
		log.Infof("SecretHandlerTest: Skipping test for config map case")
		return
	}
	secret, err := readYAMLSecret(tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	secret.ManagementServerConfig[0].Password = "test-password"
	err = writeYAMLConfig(secret, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update secret file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the secret to be updated
	time.Sleep(1 * time.Second)

	secret.ManagementServerConfig[0].Password = "password"
	err = writeYAMLConfig(secret, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update secret file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the secret to be updated
	time.Sleep(1 * time.Second)
}

// Tests the params configmap handler of reverseproxy, needs a running server
func CMParamsHandlerTest(t *testing.T) {
	log.Infof("Start CMParamsHandlerTest")
	defer log.Infof("End CMParamsHandlerTest")
	if os.Getenv(common.EnvReverseProxyUseSecret) == "false" {
		log.Infof("CMParamsHandlerTest: Skipping test for config map case")
		return
	}
	cmp, err := readYAMLConfigParams(paramsConfigMapFile, common.TestConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	cmp.LogFormat = "json"
	cmp.LogLevel = "debug"
	err = writeYAMLConfig(cmp, paramsConfigMapFile, common.TestConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map params file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)

	cmp.LogFormat = "TEXT"
	cmp.LogLevel = "info"
	err = writeYAMLConfig(cmp, paramsConfigMapFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map params file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)
}

// Tests the configmap handler, needs a running server
func CMHandlerTest(t *testing.T) {
	log.Infof("Start CMHandlerTest")
	defer log.Infof("End CMHandlerTest")

	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		log.Infof("CMHandlerTest: Skipping test for secret case")
		return
	}
	cm, err := readYAMLConfig(tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "test-secret"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)

	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "proxy-secret-1"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Errorf("Failed to update config map file. (%s)", err.Error())
	}
	// sleep for 1 seconds to allow the configmap to be updated
	time.Sleep(1 * time.Second)
}

// Tests the http request, needs a running server
func SAHTTPRequestTest(t *testing.T) {
	log.Infof("Start SAHTTPRequestTest")
	defer log.Infof("End SAHTTPRequestTest")
	// make a request for version
	path := utils.Prefix + "/version"
	resp, err := doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for symmterix
	path = utils.Prefix + "/91/system/symmetrix"
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for capabilities
	path = utils.Prefix + "/91/replication/capabilities/symmetrix"
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to endpoint for ServeReverseProxy
	path = utils.Prefix + "/91/sloprovisioning/symmetrix/" + storageArrayID
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to ServeVolume
	path = utils.Prefix + "/91/sloprovisioning/symmetrix/" + storageArrayID + "/volume"
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to ServeIterator
	id := "00000000-1111-2abc-def3-44gh55ij66kl_0"
	path = utils.Prefix + "/common/Iterator/" + id + "/page"
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for performance
	path = utils.Prefix + "/performance/Array/keys"
	resp, err = doHTTPRequest(server.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	path = utils.PrivatePrefix + "/91/sloprovisioning/symmetrix/" + storageArrayID
	resp, err = doHTTPRequest(server.Port, path)
	log.Info("test info is there")
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)
}

func TestMainFunc(t *testing.T) {
	defaultRunFunc := runFunc
	defaultGetKubeClientSetFunc := k8sInitFunc
	defaultRunLeaderElectionFunc := runWithLeaderElectionFunc
	defaultStartServerFunc := startServerFunc

	afterEach := func() {
		runFunc = defaultRunFunc
		k8sInitFunc = defaultGetKubeClientSetFunc
		runWithLeaderElectionFunc = defaultRunLeaderElectionFunc
		startServerFunc = defaultStartServerFunc
	}

	runningCh := make(chan string)
	tests := []struct {
		name    string
		setup   func()
		want    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "execute main without leader election k8s init func failure",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "false")
				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					// must defer writing to the channel so all goroutines can finish running,
					// avoiding data races triggered by resetting the default func vars in the afterEach() func.
					defer func() { runningCh <- "not running" }()

					return nil, errors.New("error, k8s init failed")
				}
			},
			want:    "not running",
			wantErr: true,
			errMsg:  "should not be running",
		},
		{
			name: "execute main() with leader election failure",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "true")
				t.Setenv(common.EnvWatchNameSpace, common.DefaultNameSpace)

				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					return &Server{
						Opts: opts,
					}, nil
				}

				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return &k8sutils.K8sUtils{
						KubernetesClient: k8sutils.KubernetesClient{
							Clientset: fake.NewSimpleClientset(),
						},
					}, nil
				}

				runWithLeaderElectionFunc = func(_ *k8sutils.KubernetesClient) (err error) {
					// must defer writing to the channel so all goroutines can finish running,
					// avoiding data races triggered by resetting the default func vars in the afterEach() func.
					defer func() { runningCh <- "not running" }()

					ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
					defer cancel()

					// Simulate leader election failure by setting lease duration > renew
					lei := leaderelection.NewLeaderElection(fake.NewSimpleClientset(), "csi-powermax-reverse-proxy-dellemc-com", runFunc)
					lei.WithRenewDeadline(10 * time.Second)
					lei.WithLeaseDuration(5 * time.Second)
					lei.WithContext(ctx)
					lei.WithNamespace(getEnv(common.EnvWatchNameSpace, common.DefaultNameSpace))

					// leader election Run() panics on failure, catch the panic and return an error
					defer func() {
						if r := recover(); r != nil {
							log.Errorf("Leader election panicked: %v", r)
							err = fmt.Errorf("%v, leader election failed due to panic: %v", err, r)
						}
					}()

					err = lei.Run()
					if err != nil {
						t.Errorf("leader election failed as expected")
					} else {
						t.Errorf("expected leader election failure but got none")
					}
					return err
				}
			},
			want:    "not running",
			wantErr: true,
			errMsg:  "should not be running",
		},
		{
			name: "execute main() with leader election k8s init func failure",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "true")
				t.Setenv(common.EnvWatchNameSpace, common.DefaultNameSpace)

				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					return &Server{
						Opts: opts,
					}, nil
				}

				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					// must defer writing to the channel so all goroutines can finish running,
					// avoiding data races triggered by resetting the default func vars in the afterEach() func.
					defer func() { runningCh <- "not running" }()

					return nil, errors.New("error, k8s init failed")
				}
			},
			want:    "not running",
			wantErr: true,
			errMsg:  "should not be running",
		},
		{
			name: "execute main() without leader election",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "false")
				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					// must defer writing to the channel so all goroutines can finish running,
					// avoiding data races triggered by resetting the default func vars in the afterEach() func.
					defer func() { runningCh <- "running" }()

					return &Server{
						Opts: opts,
					}, nil
				}
				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return &k8sutils.K8sUtils{
						KubernetesClient: k8sutils.KubernetesClient{
							Clientset: fake.NewSimpleClientset(),
						},
					}, nil
				}
			},
			want:    "running",
			wantErr: false,
		},
		{
			name: "execute main() with leader election",
			setup: func() {
				runFunc = func(_ context.Context) {
					runningCh <- "running"
				}

				t.Setenv(common.EnvIsLeaderElectionEnabled, "true")
				t.Setenv(common.EnvWatchNameSpace, common.DefaultNameSpace)

				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					return &Server{
						Opts: opts,
					}, nil
				}

				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return &k8sutils.K8sUtils{
						KubernetesClient: k8sutils.KubernetesClient{
							Clientset: fake.NewSimpleClientset(),
						},
					}, nil
				}
			},
			want:    "running",
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer afterEach()
			tt.setup()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			go main()

			select {
			case running := <-runningCh:
				switch running {
				case "running":
					if tt.want != "running" {
						t.Errorf("main() = %v, want %v", running, tt.want)
					}

				case "not running":
					if tt.want != "not running" {
						t.Errorf("main() = %v, want %v", running, tt.want)
					}
				}
			case <-ctx.Done():
				t.Errorf("timed out waiting for driver to run")
			}
		})
	}
}

func TestRun(t *testing.T) {
	defaultRunFunc := runFunc
	defaultGetKubeClientSetFunc := k8sInitFunc
	defaultRunLeaderElectionFunc := runWithLeaderElectionFunc
	defaultStartServerFunc := startServerFunc

	afterEach := func() {
		runFunc = defaultRunFunc
		k8sInitFunc = defaultGetKubeClientSetFunc
		runWithLeaderElectionFunc = defaultRunLeaderElectionFunc
		startServerFunc = defaultStartServerFunc
	}

	runningCh := make(chan string)
	tests := []struct {
		name    string
		setup   func()
		want    string
		wantErr bool
		errMsg  string
	}{
		{
			name: "execute run sucess",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "false")
				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					return &Server{
						Opts: opts,
					}, nil
				}
				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return &k8sutils.K8sUtils{
						KubernetesClient: k8sutils.KubernetesClient{
							Clientset: fake.NewSimpleClientset(),
						},
					}, nil
				}
				runFunc = func(_ context.Context) {
					runningCh <- "running"
				}
			},
			want:    "running",
			wantErr: false,
			errMsg:  "",
		},
		{
			name: "execute run start server failure",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "false")
				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return &k8sutils.K8sUtils{
						KubernetesClient: k8sutils.KubernetesClient{
							Clientset: fake.NewSimpleClientset(),
						},
					}, nil
				}
				startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
					runningCh <- "should not be running"
					return nil, errors.New("error, start server failed")
				}
			},
			want:    "should not be running",
			wantErr: true,
			errMsg:  "error, start server failed",
		},
		{
			name: "execute run k8s init failure",
			setup: func() {
				t.Setenv(common.EnvIsLeaderElectionEnabled, "false")
				k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
					return nil, errors.New("error, k8s init failed")
				}
			},
			want:    "",
			wantErr: true,
			errMsg:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer afterEach()

			tt.setup()

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*20)
			defer cancel()

			errCh := make(chan error)
			go func() {
				errCh <- run(ctx)
			}()

			select {
			case running := <-runningCh:
				if running != tt.want {
					t.Errorf("Run() = %v, want %v", running, tt.want)
				}
			case e := <-errCh:
				if (e != nil) != tt.wantErr {
					t.Errorf("Run() error = %v, wantErr %v", e, tt.wantErr)
				}
			case <-ctx.Done():
				t.Errorf("Run() timed out")
			}
		})
	}
}

func TestUpdateRevProxyLogParams(t *testing.T) {
	tests := []struct {
		name      string
		format    string
		logLevel  string
		expectLog log.Level
	}{
		{"Default values", "", "", log.DebugLevel},
		{"Valid JSON format", "json", "info", log.InfoLevel},
		{"Valid Text format", "text", "warn", log.WarnLevel},
		{"Invalid format defaults to text", "xml", "error", log.ErrorLevel},
		{"Invalid log level defaults to debug", "json", "invalid", log.DebugLevel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Capture log output
			var logOutput strings.Builder
			logrus.SetOutput(&logOutput)

			updateRevProxyLogParams(tt.format, tt.logLevel)

			// Validate log level
			assert.Equal(t, tt.expectLog, logrus.GetLevel())
		})
	}
}

func TestK8sInitFunc(t *testing.T) {
	tests := []struct {
		name        string
		kubeClient  *k8sutils.KubernetesClient
		expectError bool
		expectNil   bool
	}{
		{
			name: "Successful Initialization",
			kubeClient: &k8sutils.KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return fake.NewSimpleClientset(), nil
				},
				GetPathFromEnvFunc: func() string { return "./" },
			},
			expectError: false,
			expectNil:   false,
		},
		{
			name: "Failed Initialization",
			kubeClient: &k8sutils.KubernetesClient{
				Clientset:         fake.NewSimpleClientset(),
				RestForConfigFunc: func() (*rest.Config, error) { return &rest.Config{}, nil },
				NewForConfigFunc: func(config *rest.Config) (kubernetes.Interface, error) {
					return nil, errors.New("error")
				},
				GetPathFromEnvFunc: func() string { return "./" },
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resyncPeriod := 10 * time.Second
			k8sUtils, err := k8sInitFunc("default", "/tmp/certs", true, resyncPeriod, tt.kubeClient)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectNil {
				assert.Nil(t, k8sUtils)
			} else {
				assert.NotNil(t, k8sUtils)
			}
		})
	}
}

func TestStartServerFuncFailure(t *testing.T) {
	opts := getServerOpts()
	// Invalid config file
	opts.ConfigFileName = "invalid.yaml"
	opts.ConfigDir = "./invalid-dir"
	_, err := startServerFunc(k8smock.Init(), opts)
	if err == nil {
		t.Errorf("expecting server to not start, but it was started")
	}
}
