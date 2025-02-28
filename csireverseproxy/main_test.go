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

	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/k8smock"
	"revproxy/v2/pkg/k8sutils"
	"revproxy/v2/pkg/servermock"
	"revproxy/v2/pkg/utils"

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
)

var (
	server                              *Server
	primaryMockServer, backupMockServer *mockServer
	httpClient                          *http.Client
)

func startTestServer() error {
	if server != nil {
		return nil
	}
	k8sUtils := k8smock.Init()
	os.Setenv(common.EnvInClusterConfig, "true")
	serverOpts := getServerOpts()
	serverOpts.ConfigDir = common.TempConfigDir
	// Create test proxy config and start the server
	serverOpts.ConfigFileName = tmpSAConfigFile
	err := createTempConfig()
	if err != nil {
		return err
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

	filename := tmpSAConfigFile
	proxyConfigMap.Port = "2222"
	// Create a ManagementServerConfig
	tempMgmtServerConfig := createTempManagementServers()
	proxyConfigMap.Config.ManagementServerConfig = tempMgmtServerConfig
	// Create a StorageArrayConfig
	tempStorageArrayConfig := createTempStorageArrays()
	proxyConfigMap.Config.StorageArrayConfig = tempStorageArrayConfig
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
		URL:                       "://example.com/@",
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

func TestServer_SAEventHandler(t *testing.T) {
	k8sUtils := k8smock.Init()
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
