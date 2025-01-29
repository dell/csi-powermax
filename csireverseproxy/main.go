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

	log "github.com/sirupsen/logrus"

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
}

// ServerOpts - Proxy server configuration
type ServerOpts struct {
	CertDir        string
	TLSCertDir     string
	NameSpace      string
	CertFile       string
	KeyFile        string
	ConfigDir      string
	ConfigFileName string
	InCluster      bool
	SecretFilePath string
	Port           string
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
	inClusterEnvVal := getEnv(common.EnvInClusterConfig, "false")
	inCluster := false
	port := getEnv(common.EnvSidecarProxyPort, common.DefaultPort)

	if strings.ToLower(inClusterEnvVal) == "true" {
		inCluster = true
	}
	return ServerOpts{
		CertDir:        certDir,
		TLSCertDir:     tlsCertDir,
		NameSpace:      defaultNameSpace,
		ConfigFileName: configFile,
		ConfigDir:      configDir,
		CertFile:       common.DefaultCertFile,
		KeyFile:        common.DefaultKeyFile,
		InCluster:      inCluster,
		Port:           port,
	}
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

// Setup sets up the server and the proxy configuration
// this includes - reading the secret or config map, creating appropriate proxy instance
// and setting up the signal handler channel
func (s *Server) Setup(k8sUtils k8sutils.UtilsInterface) error {

	// Read the config from secret if secret provided
	if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
		log.Printf("Reading config using secret")

		vs := viper.New()
		proxySecret, err := config.ReadConfigFromSecret(vs)
		if err != nil {
			log.Printf("Error while reading config from secret: %v\n", err)
			return err
		}

		proxyConfig, err := config.NewProxyConfigFromSecret(proxySecret, k8sUtils)
		if err != nil {
			log.Printf("Error while creating proxy config from secret: %v\n", err)
			return err
		}
		s.CertFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.CertFile)
		s.KeyFile = filepath.Join(s.Opts.TLSCertDir, s.Opts.KeyFile)

		s.Port = proxyConfig.Port
		proxy, err := proxy.NewProxy(*proxyConfig)
		if err != nil {
			log.Printf("Error while creating proxy instance from secret: %v\n", err)
			return err
		}

		log.Infof("Setting up watcher for mounted secret")
		s.SetupConfigWatcher(k8sUtils, vs, s.configChangeSecret)

		// params config map
		vcp := viper.New()
		paramsFilePath := getEnv(common.EnvPowermaxConfigPath, "")
		paramsConfig, err := config.ReadParamsConfigMapFromPath(paramsFilePath, vcp)
		if err != nil {
			log.Printf("Error while reading from params config map: %v\n", err)
			return err
		}
		if paramsConfig.Port != "" {
			log.Infof("Setting reverseproxy port to %s", paramsConfig.Port)
			s.Port = paramsConfig.Port
			proxyConfig.Port = paramsConfig.Port
		}

		log.Infof("Setting up watcher for mounted params config map")
		s.SetupConfigWatcher(k8sUtils, vcp, s.configChangeParamsConfigMap)

		s.Proxy = proxy
		s.SetConfig(proxyConfig)
		s.SigChan = make(chan os.Signal, 1)

	} else {
		// Read the config from config map
		log.Printf("Reading config using config map")
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
			log.Printf("Error while creating proxy instance from config map: %v\n", err)
			return err
		}

		log.Infof("Setting up watcher for mounted reverse proxy config map")

		s.SetupConfigWatcher(k8sUtils, vcm, s.configChangeConfigMap)

		s.Proxy = proxy
		s.SetConfig(proxyConfig)
		s.SigChan = make(chan os.Signal, 1)
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
				log.Fatalf("ListenAndServe(): %v", err)
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
		log.Debug("SignalHandler setup to listen for SIGINT and SIGHUP")
		sig := <-s.SigChan
		log.Infof("Received signal: %v", sig)
		// Stop InformerFactory
		k8sUtils.StopInformer()
		// gracefully shutdown http server
		err := s.HTTPServer.Shutdown(context.Background())
		if err != nil {
			log.Fatalf("Error during graceful shutdown of the server: %v", err)
		} else {
			log.Info("Server shutdown gracefully on signal")
		}
		close(s.SigChan)
	}()
}

func updateRevProxyLogParams(format, logLevel string) {
	logFormatFromConfig := strings.ToLower(format)
	var formatter log.Formatter
	// Use text logger as default
	formatter = &log.TextFormatter{
		DisableColors: true,
		FullTimestamp: true,
	}
	if strings.EqualFold(logFormatFromConfig, "json") {
		formatter = &log.JSONFormatter{
			TimestampFormat: time.RFC3339Nano,
		}
	} else if !strings.EqualFold(logFormatFromConfig, "text") && (logFormatFromConfig != "") {
		log.Printf("Unsupported logFormat: %s supplied. Defaulting to text", logFormatFromConfig)
	}
	level := log.DebugLevel // Use debug as default
	if logLevel != "" {
		logLevel = strings.ToLower(logLevel)
		l, err := log.ParseLevel(logLevel)
		if err != nil {
			log.WithError(err).Errorf("logLevel %s value not recognized, error: %s, Setting to default: %s",
				logLevel, err.Error(), level)
		} else {
			level = l
		}
	} else {
		log.Print("Couldn't read logLevel from config file. Using debug level as default")
	}
	setLogFormatAndLevel(formatter, level)
}

func setLogFormatAndLevel(logFormat log.Formatter, level log.Level) {
	log.SetFormatter(logFormat)
	log.Infof("Setting log level to %v", level)
	log.SetLevel(level)
}

// SetupConfigWatcher - Uses viper config change watcher to watch for
// config change events on the yaml file
// this also works with configmaps as viper evaluates the symlinks (from the configmap mount)
// When a config change event is received, the proxy are updated with the new configuration
func (s *Server) SetupConfigWatcher(k8sUtils k8sutils.UtilsInterface, v *viper.Viper, f func(k k8sutils.UtilsInterface, v *viper.Viper)) {
	v.WatchConfig()
	v.OnConfigChange(func(e fsnotify.Event) {
		log.Infof("Received a config change event %s for %s", e.Op.String(), e.Name)
		f(k8sUtils, v)
	})
}

func (s *Server) configChangeConfigMap(k8sUtils k8sutils.UtilsInterface, vcm *viper.Viper) {
	log.Infof("Received a config change event for configmap")
	var proxyConfigMap config.ProxyConfigMap
	err := vcm.Unmarshal(&proxyConfigMap)
	if err != nil {
		log.Errorf("Error in unmarshalling the config: %s", err.Error())
		return
	}
	err = proxyConfigMap.CustomUnmarshal(vcm)
	if err != nil {
		log.Errorf("Error in unmarshalling the config map: %s", err.Error())
		return
	}
	updateRevProxyLogParams(proxyConfigMap.LogFormat, proxyConfigMap.LogLevel)
	proxyConfig, err := config.NewProxyConfig(&proxyConfigMap, k8sUtils)
	if err != nil || proxyConfig == nil {
		log.Errorf("Error parsing the config: %v", err)
	} else {
		s.SetConfig(proxyConfig)
		err = s.GetRevProxy().UpdateConfig(*proxyConfig)
		if err != nil {
			log.Errorf("Error in updating the config: %s", err.Error())
		}
	}
}

func (s *Server) configChangeSecret(k8sUtils k8sutils.UtilsInterface, vs *viper.Viper) {
	log.Infof("Received a config change event for secret")
	var proxySecret config.ProxySecret
	err := vs.Unmarshal(&proxySecret)
	if err != nil {
		log.Errorf("Error in unmarshalling the config: %s", err.Error())
	} else {
		proxyConfig, err := config.NewProxyConfigFromSecret(&proxySecret, k8sUtils)
		if err != nil || proxyConfig == nil {
			log.Errorf("Error parsing the config: %v", err)
		} else {
			s.SetConfig(proxyConfig)
			err = s.GetRevProxy().UpdateConfig(*proxyConfig)
			if err != nil {
				log.Errorf("Error in updating the config: %s", err.Error())
			}
		}
	}
}

func (s *Server) configChangeParamsConfigMap(k8sUtils k8sutils.UtilsInterface, vcmp *viper.Viper) {
	log.Infof("Received a config change event for params configmap - all settings %v", vcmp.AllSettings())
	var ParamsConfigMap config.ParamsConfigMap
	err := vcmp.Unmarshal(&ParamsConfigMap)
	if err != nil {
		log.Errorf("Error in unmarshalling the params config: %s", err.Error())
		return
	}

	updateRevProxyLogParams(ParamsConfigMap.LogFormat, ParamsConfigMap.LogLevel)
	config := s.Config()
	log.Infof("Updating reverse proxy port to %s", ParamsConfigMap.Port)
	config.Port = ParamsConfigMap.Port
	err = s.GetRevProxy().UpdateConfig(*config)
	if err != nil {
		log.Errorf("Error in updating the config: %s", err.Error())
	}
}

// EventHandler - callback function which is used by k8sutils
// when an event related to a secret in the namespace being watched
// is received by the informer
func (s *Server) EventHandler(k8sUtils k8sutils.UtilsInterface, secret *corev1.Secret) {
	conf := s.Config().DeepCopy()
	hasChanged := false

	found := conf.IsSecretConfiguredForCerts(secret.Name)
	if found {
		certFileName, err := k8sUtils.GetCertFileFromSecret(secret)
		if err != nil {
			log.Errorf("failed to get cert file from secret (error: %s). ignoring the config change event", err.Error())
			return
		}
		isUpdated := conf.UpdateCerts(secret.Name, certFileName)
		if isUpdated {
			hasChanged = true
		}
	}
	found = conf.IsSecretConfiguredForArrays(secret.Name)
	if found {
		if getEnv(common.EnvReverseProxyUseSecret, "false") == "true" {
			proxySecret, err := config.ReadConfigFromSecret(viper.New())
			if err != nil {
				log.Errorf("error while reading config from raw secret: %v\n", err)
			}

			for _, mgmtServer := range proxySecret.ManagementServerConfig {
				creds := &common.Credentials{
					UserName: mgmtServer.Username,
					Password: mgmtServer.Password,
				}

				isUpdated := conf.UpdateCreds(secret.Name, creds)
				if isUpdated {
					hasChanged = true
				}
			}
		} else {
			creds, err := k8sUtils.GetCredentialsFromSecret(secret)
			if err != nil {
				log.Errorf("failed to get credentials from secret (error: %s). ignoring the config change event", err.Error())
				return
			}
			isUpdated := conf.UpdateCreds(secret.Name, creds)
			if isUpdated {
				hasChanged = true
			}
		}
	}

	if hasChanged {
		err := s.GetRevProxy().UpdateConfig(*conf)
		if err != nil {
			log.Fatalf("Failed to update credentials/certs for the secret(%s)", secret.Name)
		}
		s.SetConfig(conf)
		log.Errorf("Credentials/Certs updated successfully for the secret(%s)", secret.Name)
	}
}

func startServer(k8sUtils k8sutils.UtilsInterface, opts ServerOpts) (*Server, error) {
	server := &Server{
		Opts: opts,
	}

	err := server.Setup(k8sUtils)
	if err != nil {
		log.Fatalf("Failed to setup Server (%s)", err.Error())
		return nil, err
	}

	// Start the Secrets informer
	err = k8sUtils.StartInformer(server.EventHandler)
	if err != nil {
		log.Fatalf("Failed to start informer. (%s)", err.Error())
		return nil, err
	}

	// Start the lock request handler
	utils.InitializeLock()

	// Start the server
	server.Start()

	// Setup the signal handler
	server.SignalHandler(k8sUtils)

	return server, nil
}

func run(ctx context.Context) {
	signal.Ignore()

	// Get the server opts
	opts := getServerOpts()

	// Create an informer
	k8sUtils, err := k8sutils.Init(opts.NameSpace, opts.CertDir, opts.InCluster, time.Second*30)
	if err != nil {
		log.Fatal(err.Error())
	}

	server, err := startServer(k8sUtils, opts)
	if err != nil {
		log.Fatalln("Server start failed")
	}

	// Wait for the server to exit gracefully
	server.WaitGroup.Wait()

	// Sleep for sometime to allow all goroutines to finish logging
	time.Sleep(100 * time.Millisecond)
}

func main() {
	if isLEEnabled := getEnv(common.EnvIsLeaderElectionEnabled, "false"); isLEEnabled == "true" {
		isInCluster := getEnv(common.EnvInClusterConfig, "false")
		kubeClient := k8sutils.KubernetesClient{}
		if err := kubeClient.CreateKubeClient(isInCluster == "true"); err != nil {
			log.Fatalf("Failed to create kube client: [%s]", err.Error())
		}
		le := leaderelection.NewLeaderElection(kubeClient.Clientset, "csi-powermax-reverse-proxy-dellemc-com", run)
		defaultNamespace := getEnv(common.EnvWatchNameSpace, common.DefaultNameSpace)
		le.WithNamespace(defaultNamespace)
		if err := le.Run(); err != nil {
			log.Fatalf("Failed to initialize leader election: [%s]", err.Error())
		}
	} else {
		run(context.TODO())
	}
}
