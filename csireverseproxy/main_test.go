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

package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
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

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/servermock"
	"revproxy/v2/pkg/utils"

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
)

var (
	standAloneServer                    *Server
	primaryMockServer, backupMockServer *mockServer
	httpClient                          *http.Client
)

func startTestServer() error {
	if standAloneServer != nil {
		return nil
	}
	k8sUtils := k8smock.Init()
	serverOpts := getServerOpts()
	serverOpts.ConfigDir = common.TempConfigDir
	// Create test standAlone proxy config and start the standAlone server
	serverOpts.ConfigFileName = tmpSAConfigFile
	err := createTempConfig("StandAlone")
	if err != nil {
		return err
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
	standAloneServer, err = startServer(k8sUtils, serverOpts)
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
	body, err := ioutil.ReadAll(resp.Body)
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
	if standAloneServer != nil {
		standAloneServer.SigChan <- syscall.SIGHUP
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

func writeYAMLConfig(val interface{}, fileName, fileDir string) error {
	file, err := yaml.Marshal(&val)
	if err != nil {
		return err
	}
	filepath := filepath.Join(fileDir, fileName)
	return ioutil.WriteFile(filepath, file, 0o777)
}

func createTempConfig(mode string) error {
	proxyConfigMap, err := readYAMLConfig(common.TestConfigFileName, common.TestConfigDir)
	if err != nil {
		log.Fatalf("Failed to read sample config file. (%s)", err.Error())
		return err
	}
	// set proxy mode for respective server
	proxyConfigMap.Mode = config.ProxyMode(mode)
	filename := tmpSAConfigFile
	proxyConfigMap.Port = "8080"
	// Create a ManagementServerConfig
	tempMgmtServerConfig := createTempManagementServers()
	proxyConfigMap.StandAloneConfig.ManagementServerConfig = tempMgmtServerConfig
	// Create a StorageArrayConfig
	tempStorageArrayConfig := createTempStorageArrays()
	proxyConfigMap.StandAloneConfig.StorageArrayConfig = tempStorageArrayConfig
	err = writeYAMLConfig(proxyConfigMap, filename, common.TempConfigDir)
	if err != nil {
		log.Fatalf("Failed to create a temporary config file. (%s)", err.Error())
	}
	return err
}

func createTempStorageArrays() []config.StorageArrayConfig {
	tempStorageArrayConfig := []config.StorageArrayConfig{
		{
			PrimaryURL:             getURL(primaryPort, "/"),
			BackupURL:              getURL(backupPort, "/"),
			StorageArrayID:         storageArrayID,
			ProxyCredentialSecrets: []string{proxySecretName},
		},
	}
	return tempStorageArrayConfig
}

func createTempManagementServers() []config.ManagementServerConfig {
	// Create a primary management server
	primaryMgmntServer := config.ManagementServerConfig{
		ArrayCredentialSecret:     proxySecretName,
		URL:                       getURL(primaryPort, "/"),
		SkipCertificateValidation: skipPrimaryCertValidation,
	}
	if !skipPrimaryCertValidation {
		primaryMgmntServer.CertSecret = primaryCertSecretName
	}
	// Create a backup management server
	backupMgmntServer := config.ManagementServerConfig{
		ArrayCredentialSecret:     proxySecretName,
		URL:                       getURL(backupPort, "/"),
		SkipCertificateValidation: skipBackupCertValidation,
	}
	if !skipBackupCertValidation {
		backupMgmntServer.CertSecret = backupCertSecretName
	}
	tempMgmtServerConfig := []config.ManagementServerConfig{primaryMgmntServer, backupMgmntServer}
	return tempMgmtServerConfig
}

func TestMain(m *testing.M) {
	status := 0
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
	err = startTestServer()
	if err != nil {
		log.Fatalf("Failed to start proxy server. (%s)", err.Error())
		stopServers()
		os.Exit(1)
	}
	log.Info("Proxy server started successfully")
	if st := m.Run(); st > status {
		status = st
	}
	log.Info("Stopping the mock and proxy servers")
	stopServers()
	log.Info("Removing the certs")
	err = utils.RemoveTempFiles()
	if err != nil {
		log.Fatalln(err.Error())
		os.Exit(1)
	}
	os.Exit(status)
}

func TestServer_Start(t *testing.T) {
	err := startTestServer()
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
	oldProxySecret := standAloneServer.config.StandAloneProxyConfig.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
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
	standAloneServer.EventHandler(k8sUtils, newSecret)
	newProxySecret := standAloneServer.config.StandAloneProxyConfig.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
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
	standAloneServer.EventHandler(k8sUtils, newSecret)
	oldProxySecret = standAloneServer.config.StandAloneProxyConfig.GetStorageArray(storageArrayID)[0].ProxyCredentialSecrets[proxySecretName]
	if reflect.DeepEqual(oldProxySecret, newProxySecret) {
		t.Errorf("cert file should change after update")
	} else {
		fmt.Println("Secret Reverted Successfully")
	}
}

func TestSAHTTPRequest(t *testing.T) {
	// make a request for version
	path := utils.Prefix + "/version"
	resp, err := doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for symmterix
	path = utils.Prefix + "/91/system/symmetrix"
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for capabilities
	path = utils.Prefix + "/91/replication/capabilities/symmetrix"
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to endpoint for ServeReverseProxy
	path = utils.Prefix + "/91/sloprovisioning/symmetrix/" + storageArrayID
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to ServeVolume
	path = utils.Prefix + "/91/sloprovisioning/symmetrix/" + storageArrayID + "/volume"
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request to ServeIterator
	id := "00000000-1111-2abc-def3-44gh55ij66kl_0"
	path = utils.Prefix + "/common/Iterator/" + id + "/page"
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	// make a request for performance
	path = utils.Prefix + "/performance/Array/keys"
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)

	path = utils.PrivatePrefix + "/91/sloprovisioning/symmetrix/" + storageArrayID
	resp, err = doHTTPRequest(standAloneServer.Port, path)
	log.Info("test info is there")
	if err != nil {
		t.Error(err.Error())
		return
	}
	fmt.Printf("RESPONSE_BODY: %s\n", resp)
}
