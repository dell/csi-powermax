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
	"context"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/config"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/k8sutils"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/proxy"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	"github.com/dell/csmlog"

	"github.com/kubernetes-csi/csi-lib-utils/leaderelection"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	corev1 "k8s.io/api/core/v1"
)

// RevProxy - interface which is implemented by the different proxy implementations
type RevProxy interface {
	ServeReverseProxy(res http.ResponseWriter, req *http.Request)
	UpdateConfig(proxyConfig config.ProxyConfig) error
	GetRouter() http.Handler
	SetAuthToken(token string)
}

// ServerOpts - Proxy server configuration
type ServerOpts struct {
	CertDir            string
	TLSCertDir         string
	NameSpace          string
	CertFile           string
	KeyFile            string
	ConfigDir          string
	ConfigFileName     string
	InCluster          bool
	SecretFilePath     string
	Port               string
	MetricsEnabled     bool
	MetricsPort        string
	MetricsTLSCertFile string
	MetricsTLSKeyFile  string
}

// Server represents the proxy server
type Server struct {
	HTTPServer *http.Server
	Port       string
	CertFile   string
	KeyFile    string
	config     *config.ProxyConfig
	Proxy      *proxy.Proxy
	SigChan    chan os.Signal
	WaitGroup  sync.WaitGroup
	Mutex      sync.Mutex
	Opts       ServerOpts
}

func getEnv(envName, defaultValue string) string {
	envVal, found := os.LookupEnv(envName)
	if !found {
		envVal = defaultValue
	}
	return envVal
}

func getServerOpts() ServerOpts {
	certDir := getEnv(common.EnvCertDirName, common.DefaultCertDirName)
	tlsCertDir := getEnv(common.EnvTLSCertDirName, common.DefaultTLSCertDirName)
	defaultNameSpace := getEnv(common.EnvWatchNameSpace, common.DefaultNameSpace)
	configFile := getEnv(common.EnvConfigFileName, common.DefaultConfigFileName)
	configDir := getEnv(common.EnvConfigDirName, common.DefaultConfigDir)
	inClusterEnvVal := getEnv(common.EnvInClusterConfig, "true")
	inCluster := false
	port := getEnv(common.EnvSidecarProxyPort, common.DefaultPort)
	metricsEnabled := false
	metricsPort := getEnv(common.EnvMetricsPort, common.DefaultMetricsPort)
	metricsTLSCertFile := getEnv(common.EnvMetricsTLSCertFile, "")
	metricsTLSKeyFile := getEnv(common.EnvMetricsTLSKeyFile, "")

	if strings.ToLower(inClusterEnvVal) == "true" {
		inCluster = true
	}

	// Check if metrics is enabled
	if strings.EqualFold(os.Getenv(common.EnvMetricsEnabled), "true") {
		metricsEnabled = true
	}

	return ServerOpts{
		CertDir:            certDir,
		TLSCertDir:         tlsCertDir,
		NameSpace:          defaultNameSpace,
		ConfigFileName:     configFile,
		ConfigDir:          configDir,
		CertFile:           common.DefaultCertFile,
		KeyFile:            common.DefaultKeyFile,
		InCluster:          inCluster,
		Port:               port,
		MetricsEnabled:     metricsEnabled,
		MetricsPort:        metricsPort,
		MetricsTLSCertFile: metricsTLSCertFile,
		MetricsTLSKeyFile:  metricsTLSKeyFile,
	}
}

// SetConfig - sets config for the server
func (s *Server) SetConfig(c *config.ProxyConfig) {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	s.config = c
}

// Config - Returns the server config
func (s *Server) Config() *config.ProxyConfig {
	s.Mutex.Lock()
	defer s.Mutex.Unlock()
	return s.config
}

// isSidecarMode detects if running as sidecar by checking multiple indicators
func isSidecarMode() bool {
	// Method 1: Check the DeployAsSidecar environment variable set by the operator
	// In sidecar mode, the operator sets DeployAsSidecar=true
	deployAsSidecar := getEnv("DeployAsSidecar", "") // No default for more accurate detection

	// Method 2: Check if we're running in the same pod as CSI driver containers
	// by checking for CSI driver environment variables or container presence

	// Check for CSI driver specific environment variables that indicate sidecar mode
	csiDriverEnvVars := []string{
		"X_CSI_POWERMAX_ENDPOINT",
		"X_CSI_POWERMAX_USER",
		"X_CSI_POWERMAX_PASSWORD",
		"X_CSI_POWERMAX_NODENAME",
		"X_CSI_POWERMAX_PORTGROUPS",
	}

	hasCSIEnvVars := false
	for _, envVar := range csiDriverEnvVars {
		if getEnv(envVar, "") != "" {
			hasCSIEnvVars = true
			break
		}
	}

	// Method 3: Check pod name and namespace patterns
	podName := getEnv("POD_NAME", "")

	// CSI driver pods typically have specific naming patterns
	isCSIDriverPod := strings.Contains(podName, "controller") ||
		strings.Contains(podName, "node") ||
		strings.Contains(podName, "csi-")

	// Debug logging to understand the mode detection
	csmlog.Infof("DeployAsSidecar environment variable: '%s'", deployAsSidecar)
	csmlog.Infof("Has CSI driver environment variables: %t", hasCSIEnvVars)
	csmlog.Infof("Pod name: '%s', Is CSI driver pod: %t", podName, isCSIDriverPod)

	// Determine sidecar mode based on multiple indicators
	isSidecar := false

	// Primary check: DeployAsSidecar environment variable explicitly set
	if deployAsSidecar != "" {
		lowerValue := strings.ToLower(strings.TrimSpace(deployAsSidecar))
		isSidecar = lowerValue == "true" || lowerValue == "1" || lowerValue == "yes"
		csmlog.Infof("DeployAsSidecar explicitly set, sidecar mode: %t", isSidecar)
	} else {
		// Fallback check: If we have CSI driver env vars and are in a CSI driver pod
		isSidecar = hasCSIEnvVars && isCSIDriverPod
		csmlog.Infof("DeployAsSidecar not set, using fallback detection, sidecar mode: %t", isSidecar)
	}

	csmlog.Infof("Final sidecar mode determination: %t", isSidecar)
	return isSidecar
}

// Setup sets up the server and the proxy configuration
// this includes - reading the secret or config map, creating appropriate proxy instance
// and setting up the signal handler channel
func (s *Server) Setup(k8sUtils k8sutils.UtilsInterface) error {
	// Auth token validation is handled during loading in the proxy configuration section below

	// Read the config from secret if secret provided
	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
		csmlog.Info("Reading config using secret")

		vs := viper.New()
		proxySecret, err := config.ReadConfigFromSecret(vs)
		if err != nil {
			csmlog.Errorf("Error while reading config from secret: %v", err)
			return err
		}

		proxyConfig, err := config.NewProxyConfigFromSecret(proxySecret, k8sUtils)
		if err != nil {
			csmlog.Errorf("Error while creating proxy config from secret: %v", err)
			return err
		}
		s.CertFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.CertFile)
		s.KeyFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.KeyFile)

		s.Port = proxyConfig.Port
		proxy, err := proxy.NewProxy(*proxyConfig)
		if err != nil {
			csmlog.Errorf("Error while creating proxy instance from secret: %v", err)
			return err
		}

		csmlog.Info("Setting up watcher for mounted secret")
		s.SetupConfigWatcher(k8sUtils, vs, s.configChangeSecret)

		// params config map
		vcp := viper.New()
		paramsFilePath := getEnv(common.EnvPowermaxConfigPath, "")
		paramsConfig, err := config.ReadParamsConfigMapFromPath(paramsFilePath, vcp)
		if err != nil {
			csmlog.Errorf("Error while reading from params config map: %v", err)
			return err
		}
		if paramsConfig.Port != "" {
			csmlog.Infof("Setting reverseproxy port to %s", paramsConfig.Port)
			s.Port = paramsConfig.Port
			proxyConfig.Port = paramsConfig.Port
		}

		csmlog.Info("Setting up watcher for mounted params config map")
		s.SetupConfigWatcher(k8sUtils, vcp, s.configChangeParamsConfigMap)

		s.Proxy = proxy
		s.SetConfig(proxyConfig)
		s.SigChan = make(chan os.Signal, 1)

	} else {
		// Read the config from config map
		csmlog.Info("Reading config using config map")
		vcm := viper.New()
		proxyConfigMap, err := config.ReadConfig(s.Opts.ConfigFileName, s.Opts.ConfigDir, vcm)
		if err != nil {
			return err
		}
		updateRevProxyLogParams(proxyConfigMap.LogFormat, proxyConfigMap.LogLevel)
		proxyConfig, err := config.NewProxyConfig(proxyConfigMap, k8sUtils)
		if err != nil {
			return err
		}
		s.CertFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.CertFile)
		s.KeyFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.KeyFile)
		s.Port = proxyConfig.Port
		proxy, err := proxy.NewProxy(*proxyConfig)
		if err != nil {
			csmlog.Errorf("Error while creating proxy instance from config map: %v", err)
			return err
		}

		csmlog.Info("Setting up watcher for mounted reverse proxy config map")

		s.SetupConfigWatcher(k8sUtils, vcm, s.configChangeConfigMap)

		s.Proxy = proxy
		s.SetConfig(proxyConfig)
		s.SigChan = make(chan os.Signal, 1)
	}

	// Load shared auth token if configured
	// Note: Auth token is only used in standalone mode, not in sidecar mode
	isSidecar := isSidecarMode()
	if tokenFile := getEnv(common.EnvProxyAuthTokenFile, ""); tokenFile != "" {
		if isSidecar {
			csmlog.Info("Sidecar deployment detected - auth token is not used in sidecar mode, skipping")
			// Skip auth token loading entirely in sidecar mode
		} else {
			csmlog.Info("Standalone deployment detected - loading auth token")
			tokenBytes, err := os.ReadFile(filepath.Clean(tokenFile))
			if err != nil {
				csmlog.Warnf("Proxy auth token file %s not found or not readable in standalone mode, continuing without auth token: %v", tokenFile, err)
				// Continue deployment without auth token - don't fail
			} else {
				token := strings.TrimSpace(string(tokenBytes))
				if token == "" {
					csmlog.Warnf("Proxy auth token file %s is empty in standalone mode, continuing without auth token", tokenFile)
					// Continue deployment without auth token - don't fail
				} else {
					s.Proxy.SetAuthToken(token)
					csmlog.Info("Proxy auth token loaded successfully for standalone deployment")
				}
			}
		}
	}

	return nil
}

// GetRevProxy - returns the current active proxy for the server
func (s *Server) GetRevProxy() RevProxy {
	return s.Proxy
}

// Start - starts the HTTPS server
func (s *Server) Start() {
	s.WaitGroup.Add(1)
	if s.HTTPServer == nil {
		port := utils.GetListenAddress(s.Port)
		handler := s.GetRevProxy().GetRouter()
		server := http.Server{
			Addr:              port,
			Handler:           handler,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			defer s.WaitGroup.Done()
			// always returns error. ErrServerClosed on graceful close
			if err := server.ListenAndServeTLS(s.CertFile, s.KeyFile); err != http.ErrServerClosed {
				csmlog.Fatalf("ListenAndServe(): %v", err)
			}
		}()
		s.HTTPServer = &server
	}
}

// SignalHandler - listens for SIGINT and SIGHUP
// when the signal is received it stops the k8s informer
// and attempts to shutdown the HTTPS server gracefully
func (s *Server) SignalHandler(k8sUtils k8sutils.UtilsInterface) {
	go func() {
		signal.Notify(s.SigChan, syscall.SIGINT, syscall.SIGHUP)
		csmlog.Debug("SignalHandler setup to listen for SIGINT and SIGHUP")
		sig := <-s.SigChan
		csmlog.Infof("Received signal: %v", sig)
		// Stop InformerFactory
		k8sUtils.StopInformer()
		// gracefully shutdown http server
		err := s.HTTPServer.Shutdown(context.Background())
		if err != nil {
			csmlog.Errorf("Error during graceful shutdown of the server: %v", err)
		} else {
			csmlog.Info("Server shutdown gracefully on signal")
		}
		close(s.SigChan)
	}()
}

func updateRevProxyLogParams(format, logLevel string) {
	logFormatFromConfig := strings.ToLower(format)
	if !strings.EqualFold(logFormatFromConfig, "json") && !strings.EqualFold(logFormatFromConfig, "text") && (logFormatFromConfig != "") {
		csmlog.Infof("Unsupported logFormat: %s supplied. Defaulting to json", logFormatFromConfig)
	}
	level := csmlog.InfoLevel // Use info as default
	if logLevel != "" {
		logLevel = strings.ToLower(logLevel)
		l, err := csmlog.ParseLevel(logLevel)
		if err != nil {
			csmlog.Errorf("logLevel %s value not recognized, error: %s, Setting to default: %s",
				logLevel, err.Error(), level)
		} else {
			level = l
		}
	} else {
		csmlog.Info("Couldn't read logLevel from config file. Using info level as default")
	}
	setLogFormatAndLevel(logFormatFromConfig, level)
}

func setLogFormatAndLevel(format string, level csmlog.Level) {
	csmlog.SetFormat(format)
	csmlog.Infof("Setting log level to %v", level)
	csmlog.SetLevel(level)
}

// SetupConfigWatcher - Uses viper config change watcher to watch for
// config change events on the yaml file
// this also works with configmaps as viper evaluates the symlinks (from the configmap mount)
// When a config change event is received, the proxy are updated with the new configuration
func (s *Server) SetupConfigWatcher(k8sUtils k8sutils.UtilsInterface, v *viper.Viper, f func(k k8sutils.UtilsInterface, v *viper.Viper)) {
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		csmlog.Infof("Received a config change event %s for %s", e.Op.String(), e.Name)
		f(k8sUtils, v)
	})
}

func (s *Server) configChangeConfigMap(k8sUtils k8sutils.UtilsInterface, vcm *viper.Viper) {
	csmlog.Infof("Received a config change event for configmap - all settings")
	var proxyConfigMap config.ProxyConfigMap
	err := vcm.Unmarshal(&proxyConfigMap)
	if err != nil {
		csmlog.Errorf("Error in unmarshalling the config: %s", err.Error())
		return
	}
	err = proxyConfigMap.CustomUnmarshal(vcm)
	if err != nil {
		csmlog.Errorf("Error in unmarshalling the config map: %s", err.Error())
		return
	}
	updateRevProxyLogParams(proxyConfigMap.LogFormat, proxyConfigMap.LogLevel)
	proxyConfig, err := config.NewProxyConfig(&proxyConfigMap, k8sUtils)
	if err != nil || proxyConfig == nil {
		csmlog.Errorf("Error parsing the config: %v", err)
	} else {
		s.SetConfig(proxyConfig)
		err = s.GetRevProxy().UpdateConfig(*proxyConfig)
		if err != nil {
			csmlog.Errorf("Error in updating the config: %s", err.Error())
		}
		csmlog.Infof("Updated proxy config")
	}
}

func (s *Server) configChangeSecret(k8sUtils k8sutils.UtilsInterface, vs *viper.Viper) {
	csmlog.Info("Received a config change event for secret")
	var proxySecret config.ProxySecret
	err := vs.Unmarshal(&proxySecret)
	if err != nil {
		csmlog.Errorf("Error in unmarshalling the config: %s", err.Error())
	} else {
		proxyConfig, err := config.NewProxyConfigFromSecret(&proxySecret, k8sUtils)
		if err != nil || proxyConfig == nil {
			csmlog.Errorf("Error parsing the config: %v", err)
		} else {
			s.SetConfig(proxyConfig)
			err = s.GetRevProxy().UpdateConfig(*proxyConfig)
			if err != nil {
				csmlog.Errorf("Error in updating the config: %s", err.Error())
			}
		}
	}
}

func (s *Server) configChangeParamsConfigMap(_ k8sutils.UtilsInterface, vcmp *viper.Viper) {
	csmlog.Infof("Received a config change event for params configmap")
	var ParamsConfigMap config.ParamsConfigMap
	err := vcmp.Unmarshal(&ParamsConfigMap)
	if err != nil {
		csmlog.Errorf("Error in unmarshalling the params config: %s", err.Error())
		return
	}

	updateRevProxyLogParams(ParamsConfigMap.LogFormat, ParamsConfigMap.LogLevel)
	config := s.Config()
	csmlog.Infof("Updating reverse proxy port to %s", ParamsConfigMap.Port)
	config.Port = ParamsConfigMap.Port
	err = s.GetRevProxy().UpdateConfig(*config)
	if err != nil {
		csmlog.Errorf("Error in updating the config: %s", err.Error())
	}
}

// EventHandler - callback function which is used by k8sutils
// when an event related to a secret in the namespace being watched
// is received by the informer
func (s *Server) EventHandler(k8sUtils k8sutils.UtilsInterface, secret *corev1.Secret) {
	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
		csmlog.Infof("using mounted secret for reverse proxy. ignoring the config change event")
		return
	}
	conf := s.Config().DeepCopy()
	hasChanged := false

	csmlog.Infof("Received a config change event for secret %s", secret.Name)
	found := conf.IsSecretConfiguredForCerts(secret.Name)
	if found {
		certFileName, err := k8sUtils.GetCertFileFromSecret(secret)
		if err != nil {
			csmlog.Errorf("failed to get cert file from secret (error: %s). ignoring the config change event", err.Error())
			return
		}
		isUpdated := conf.UpdateCerts(secret.Name, certFileName)
		if isUpdated {
			hasChanged = true
		}
	}
	found = conf.IsSecretConfiguredForArrays(secret.Name)
	if found {
		creds, err := k8sUtils.GetCredentialsFromSecret(secret)
		if err != nil {
			csmlog.Errorf("failed to get credentials from secret (error: %s). ignoring the config change event", err.Error())
			return
		}
		isUpdated := conf.UpdateCreds(secret.Name, creds)
		if isUpdated {
			hasChanged = true
		}
	}

	if hasChanged {
		err := s.GetRevProxy().UpdateConfig(*conf)
		if err != nil {
			csmlog.Fatalf("Failed to update credentials/certs for the secret(%s)", secret.Name)
		}
		s.SetConfig(conf)
		csmlog.Errorf("Credentials/Certs updated successfully for the secret(%s)", secret.Name)
	}
}

func startServer(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
	server := &Server{
		Opts: opts,
	}

	err := server.Setup(k8sUtils)
	if err != nil {
		csmlog.Errorf("Failed to setup Server (%s)", err.Error())
		return nil, err
	}

	// Start the Secrets informer
	k8sUtils.StartInformer(server.EventHandler)

	// Start the lock request handler
	utils.InitializeLock()

	// Start the server
	server.Start()

	// Setup the signal handler
	server.SignalHandler(k8sUtils)

	return server, nil
}

func run(_ context.Context) error {
	signal.Ignore()

	// Get the server opts
	opts := getServerOpts()

	// Create an informer
	k8sUtils, err := k8sInitFunc(opts.NameSpace, opts.CertDir, opts.InCluster, time.Second*30, &k8sutils.KubernetesClient{})
	if err != nil {
		csmlog.Errorf("run failed - %s", err.Error())
		return err
	}

	server, err := startServerFunc(k8sUtils, opts)
	if err != nil {
		csmlog.Errorf("Server start failed - %s", err.Error())
		return err
	}

	// Wait for the server to exit gracefully
	server.WaitGroup.Wait()

	// Sleep for sometime to allow all goroutines to finish logging
	time.Sleep(100 * time.Millisecond)

	return nil
}

func main() {
	if isLEEnabled := getEnv(common.EnvIsLeaderElectionEnabled, "false"); isLEEnabled == "true" {
		isInCluster := getEnv(common.EnvInClusterConfig, "false")
		kubeClient, err := k8sInitFunc(common.DefaultNameSpace, common.DefaultCertDirName, isInCluster == "true", time.Second*30, &k8sutils.KubernetesClient{})
		if err != nil {
			csmlog.Errorf("failed to create kube client: [%s]", err.Error())
			return
		}
		err = runWithLeaderElectionFunc(&kubeClient.KubernetesClient)
		if err != nil {
			csmlog.Errorf("failed to initialize leader election: [%s]", err.Error())
		}
	} else {
		runFunc(context.TODO())
	}
}

var runWithLeaderElectionFunc = func(kubeClient *k8sutils.KubernetesClient) (err error) {
	lei := leaderelection.NewLeaderElection(kubeClient.Clientset, "csi-powermax-reverse-proxy-dellemc-com", runFunc)
	lei.WithNamespace(getEnv(common.EnvWatchNameSpace, common.DefaultNameSpace))
	if err = lei.Run(); err != nil {
		csmlog.Errorf("leader election failed reason: [%s]", err.Error())
	}
	return err
}

var k8sInitFunc = func(namespace string, certDir string, isInCluster bool, resyncPeriod time.Duration, kubeClient *k8sutils.KubernetesClient) (*k8sutils.K8sUtils, error) {
	return k8sutils.Init(namespace, certDir, isInCluster, resyncPeriod, kubeClient)
}

var runFunc = func(ctx context.Context) {
	err := run(ctx)
	if err != nil {
		csmlog.Errorf("Failed to run server: %s ", err.Error())
	}
}

var startServerFunc = func(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
	return startServer(k8sUtils, opts)
}
