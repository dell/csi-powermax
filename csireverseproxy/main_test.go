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

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/config"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8smock"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/servermock"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	log "github.com/sirupsen/logrus"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
		//os.Setenv(common.EnvPowermaxConfigPath, common.TempConfigDir+"/"+"sa-config-test-main.yaml")
		err := createTempConfig()
		if err != nil {
			return err
		}
	}
	_, err = k8sUtils.CreateNewCredentialSecret(proxySecretName)
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
	yamlFile, err := ioutil.ReadFile(file)
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

func ZZZOriginalMain(m *testing.M) {
	config := os.Getenv("CONFIG")

	log.Infof("### Running tests for config - %s ####", config)
	var err error
	status := 0
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
	if st := m.Run(); st > 0 {
		log.Infof("Test run status = %d", st)
	}
	log.Info("Stopping the mock and proxy servers")
	stopServers()
	log.Info("Removing the certs, temp files")
	err = utils.RemoveTempFiles()
	if err != nil {
		log.Fatalln(err.Error())
		os.Exit(1)
	}

	log.Infof("Test run completed for %s", config)
	os.Exit(status)
}

func InitializeSetup(config string) {
	var err error
	//status := 0
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

	// Run the test logic multiple times with different configurations
	t.Run("TestSecretCase", func(t *testing.T) {
		log.Infof("#### Running tests with secret ####")
		os.Setenv(common.EnvReverseProxyUseSecret, "true")
		InitializeSetup("secret")
		defer TearDownSetup()

		//Run tests
		TestSAHTTPRequest(t)
		TestServer_EventHandler(t)
		TestServer_SAEventHandler(t)
		TestSecretHandler(t)
		TestCMHandler(t)
		TestCMParamsHandler(t)
		log.Infof("#### END Running tests with secret ####")
	})

	t.Run("TestCMCase", func(t *testing.T) {
		log.Infof("#### Running tests with configmap ####")
		InitializeSetup("configmap")
		defer TearDownSetup()
		os.Setenv(common.EnvReverseProxyUseSecret, "false")

		// Run tests
		TestSAHTTPRequest(t)
		TestServer_EventHandler(t)
		TestServer_SAEventHandler(t)
		TestSecretHandler(t)
		TestCMHandler(t)
		TestCMParamsHandler(t)
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

func TestServer_Start(t *testing.T) {

	config := "configmap"
	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		config = "secret"
	}

	err := startTestServer(config)
	if err != nil {
		t.Error(err.Error())
	}
}

func TestServer_EventHandler(t *testing.T) {
	k8sUtils := k8smock.Init()
	_, err := k8sUtils.CreateNewCertSecret(primaryCertSecretName)
	if err != nil {
		t.Error(err.Error())
	}
}

func TestServer_SAEventHandler(t *testing.T) {
	k8sUtils := k8smock.Init()
	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		//t.Skip("Skipping test as proxy is using secret")
		return
	}

	oldProxySecret := server.config.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
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
	newProxySecret := server.config.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
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
	oldProxySecret = server.config.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
	if reflect.DeepEqual(oldProxySecret, newProxySecret) {
		t.Errorf("cert file should change after update")
	} else {
		fmt.Println("Secret Reverted Successfully")
	}
}

func TestSecretHandler(t *testing.T) {
	if os.Getenv(common.EnvReverseProxyUseSecret) == "false" {
		//t.Skip("Skipping test as proxy is using secret")
		return
	}
	secret, err := readYAMLSecret(tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	secret.ManagementServerConfig[0].Password = "test-password"
	err = writeYAMLConfig(secret, tmpSAConfigFile, common.TempConfigDir)
	time.Sleep(10 * time.Second)

	secret.ManagementServerConfig[0].Password = "password"
	err = writeYAMLConfig(secret, tmpSAConfigFile, common.TempConfigDir)
	time.Sleep(10 * time.Second)
}

func TestCMParamsHandler(t *testing.T) {
	if os.Getenv(common.EnvReverseProxyUseSecret) == "false" {
		// t.Skip("Skipping test as proxy is using secret")
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
	time.Sleep(10 * time.Second)

	cmp.LogFormat = "TEXT"
	cmp.LogLevel = "info"
	err = writeYAMLConfig(cmp, paramsConfigMapFile, common.TempConfigDir)
	time.Sleep(10 * time.Second)
}

func TestCMHandler(t *testing.T) {

	if os.Getenv(common.EnvReverseProxyUseSecret) == "true" {
		//t.Skip("Skipping test as proxy is using secret")
		return
	}
	cm, err := readYAMLConfig(tmpSAConfigFile, common.TempConfigDir)
	if err != nil {
		t.Error("Failed to read config")
		return
	}

	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "test-secret"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	time.Sleep(10 * time.Second)

	cm.Config.ManagementServerConfig[0].ArrayCredentialSecret = "proxy-secret-1"
	err = writeYAMLConfig(cm, tmpSAConfigFile, common.TempConfigDir)
	time.Sleep(10 * time.Second)
}

func TestSAHTTPRequest(t *testing.T) {
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
