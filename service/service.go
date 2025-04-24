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

package service

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dell/gonvme"

	"github.com/dell/csi-powermax/v2/k8sutils"

	"github.com/dell/dell-csi-extensions/podmon"
	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	"google.golang.org/grpc"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gocsi"
	csictx "github.com/dell/gocsi/context"
	"github.com/dell/goiscsi"
	types "github.com/dell/gopowermax/v2/types/v100"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/core"
	migrext "github.com/dell/dell-csi-extensions/migration"
	csiext "github.com/dell/dell-csi-extensions/replication"
	pmax "github.com/dell/gopowermax/v2"
)

// Constants for the service
const (
	Name                       = "csi-powermax.dellemc.com"         // Name is the name of the CSI plug-in.
	ApplicationName            = "CSI Driver for Dell EMC PowerMax" // ApplicationName is the name used to register with Powermax REST APIs
	defaultPrivDir             = "/dev/disk/csi-powermax"
	defaultPmaxTimeout         = 120
	defaultLockCleanupDuration = 4
	csiPrefix                  = "csi-"
	logFields                  = "logFields"
	maxAuthenticateRetryCount  = 4
	CSILogLevelParam           = "CSI_LOG_LEVEL"
	CSILogFormatParam          = "CSI_LOG_FORMAT"
	ArrayStatus                = "/array-status"
	DefaultPodmonPollRate      = 60
	ReplicationContextPrefix   = "powermax"
	ReplicationPrefix          = "replication.storage.dell.com"
	PortGroups                 = "X_CSI_POWERMAX_PORTGROUPS"
	Protocol                   = "X_CSI_TRANSPORT_PROTOCOL"
	// PmaxEndPoint               = "X_CSI_POWERMAX_ENDPOINT"
	ManagedArrays   = "X_CSI_MANAGED_ARRAYS"
	defaultCertFile = "tls.crt"
)

type contextKey string // specific string type used for context keys

var inducedMockReverseProxy bool // for testing only

// Manifest is the SP's manifest.
var Manifest = map[string]string{
	"url":    "http://github.com/dell/csi-powermax",
	"semver": core.SemVer,
	"commit": core.CommitSha32,
	"formed": core.CommitTime.Format(time.RFC1123),
}

// Service is the CSI Mock service provider.
type Service interface {
	csi.ControllerServer
	csi.IdentityServer
	csi.NodeServer
	csiext.ReplicationServer
	migrext.MigrationServer
	BeforeServe(context.Context, *gocsi.StoragePlugin, net.Listener) error
	RegisterAdditionalServers(server *grpc.Server)
}

// Opts defines service configuration options.
type Opts struct {
	Endpoint                   string
	UseProxy                   bool
	ProxyServiceHost           string
	ProxyServicePort           string
	User                       string
	Password                   string
	SystemName                 string
	NodeName                   string
	NodeFullName               string
	TransportProtocol          string
	DriverName                 string
	CHAPUserName               string
	CHAPPassword               string
	Insecure                   bool
	Thick                      bool
	AutoProbe                  bool
	EnableBlock                bool
	EnableCHAP                 bool
	PortGroups                 []string
	ClusterPrefix              string
	ManagedArrays              []string
	DisableCerts               bool   // used for unit testing only
	Lsmod                      string // used for unit testing only
	EnableSnapshotCGDelete     bool   // when snapshot deleted, enable deleting of all snaps in the CG of the snapshot
	EnableListVolumesSnapshots bool   // when listing volumes, include snapshots and volumes
	GrpcMaxThreads             int    // Maximum threads configured in grpc
	NonDefaultRetries          bool   // Indicates if non-default retry values to be used for deletion worker, only for unit testing
	NodeNameTemplate           string
	ModifyHostName             bool
	ReplicationContextPrefix   string // Enables sidecars to read required information from volume context
	ReplicationPrefix          string // Used as a prefix to find out if replication is enabled
	IsHealthMonitorEnabled     bool   // used to check if health monitor for volume is enabled
	IsTopologyControlEnabled   bool   // used to filter topology keys based on user config
	IsVsphereEnabled           bool   // used to check if vSphere is enabled
	VSpherePortGroup           string // port group for vsphere
	VSphereHostName            string // host (initiator group) for vsphere
	VCenterHostURL             string // vCenter host url
	VCenterHostUserName        string // vCenter host username
	VCenterHostPassword        string // vCenter password
	MaxVolumesPerNode          int64  // to specify volume limits
	KubeConfigPath             string // to specify k8s configuration to be used CSI driver
	IsPodmonEnabled            bool   // used to indicate that podmon is enabled
	PodmonPort                 string // to indicates the port to be used for exposing podmon API health
	PodmonPollingFreq          string // indicates the polling frequency to check array connectivity
	TLSCertDir                 string
	StorageArrays              map[string]StorageArrayConfig
}

// StorageArrayConfig represents the configuration of a storage array in the config file
type StorageArrayConfig struct {
	Labels     map[string]interface{} `yaml:"labels,omitempty"`
	Parameters map[string]interface{} `yaml:"parameters,omitempty"`
}

// NodeConfig defines rules for given node
type NodeConfig struct {
	NodeName string   `yaml:"nodeName, omitempty"`
	Rules    []string `yaml:"rules, omitempty"`
}

// TopologyConfig defines set of allow and deny rules for multiple nodes
type TopologyConfig struct {
	AllowedConnections []NodeConfig `yaml:"allowedConnections, omitempty" mapstructure:"allowedConnections"`
	DeniedConnections  []NodeConfig `yaml:"deniedConnections, omitempty" mapstructure:"deniedConnections"`
}

type service struct {
	opts Opts
	mode string
	// amount of time to retry unisphere calls
	pmaxTimeoutSeconds int64
	// replace this with Unisphere client
	adminClient    pmax.Pmax
	deletionWorker *deletionWorker
	iscsiClient    goiscsi.ISCSIinterface
	nvmetcpClient  gonvme.NVMEinterface
	// replace this with Unisphere system if needed
	system                    *interface{}
	privDir                   string
	loggedInArrays            map[string]bool
	loggedInNVMeArrays        map[string]bool
	mutex                     sync.Mutex
	cacheMutex                sync.Mutex
	nodeProbeMutex            sync.Mutex
	probeStatus               *sync.Map
	probeStatusMutex          sync.Mutex
	pollingFrequencyMutex     sync.Mutex
	pollingFrequencyInSeconds int64
	nodeIsInitialized         bool
	useNFS                    bool
	useFC                     bool
	useIscsi                  bool
	useNVMeTCP                bool
	iscsiTargets              map[string][]string
	nvmeTargets               *sync.Map

	// Timeout for storage pool cache
	storagePoolCacheDuration time.Duration
	// only used for testing, indicates if the deletion worked finished populating queue
	waitGroup sync.WaitGroup

	// Gobrick stuff
	fcConnector      fcConnector
	iscsiConnector   iSCSIConnector
	nvmeTCPConnector NVMeTCPConnector
	dBusConn         dBusConn

	sgSvc *storageGroupSvc

	arrayTransportProtocolMap map[string]string // map of array SN to TransportProtocols
	topologyConfig            *TopologyConfig
	allowedTopologyKeys       map[string][]string // map of nodes to allowed topology keys
	deniedTopologyKeys        map[string][]string // map of nodes to denied topology keys

	k8sUtils    k8sutils.UtilsInterface
	snapCleaner *snapCleanupWorker
}

// New returns a new Service.
func New() Service {
	svc := &service{
		loggedInArrays:     map[string]bool{},
		iscsiTargets:       map[string][]string{},
		loggedInNVMeArrays: map[string]bool{},
		nvmeTargets:        new(sync.Map),
	}
	svc.sgSvc = newStorageGroupService(svc)
	svc.pmaxTimeoutSeconds = defaultPmaxTimeout
	svc.probeStatus = new(sync.Map)
	return svc
}

func updateDriverConfigParams(v *viper.Viper) {
	logFormatFromConfig := v.GetString(CSILogFormatParam)
	logFormatFromConfig = strings.ToLower(logFormatFromConfig)
	if v.IsSet(CSILogFormatParam) && logFormatFromConfig != "" {
		log.Infof("Read CSI_LOG_FORMAT: %s from configuration file", logFormatFromConfig)
	}
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
		log.Warningf("Unsupported CSI_LOG_FORMAT: %s supplied. Defaulting to text", logFormatFromConfig)
	}
	level := log.DebugLevel // Use debug as default
	if v.IsSet(CSILogLevelParam) {
		logLevel := v.GetString(CSILogLevelParam)
		if logLevel != "" {
			logLevel = strings.ToLower(logLevel)
			log.Infof("Read CSI_LOG_LEVEL: %s from config file", logLevel)
			var err error

			l, err := log.ParseLevel(logLevel)
			if err != nil {
				log.WithError(err).Errorf("CSI_LOG_LEVEL %s value not recognized, error: %s, Setting to default: %s",
					logLevel, err.Error(), level)
			} else {
				level = l
			}
		}
	} else {
		log.Warning("Couldn't read CSI_LOG_LEVEL from config file. Using debug level as default")
	}
	setLogFormatAndLevel(formatter, level)
	// set X_CSI_LOG_LEVEL so that gocsi doesn't overwrite the loglevel set by us
	_ = os.Setenv(gocsi.EnvVarLogLevel, level.String())
}

func setLogFormatAndLevel(logFormat log.Formatter, level log.Level) {
	log.SetFormatter(logFormat)
	log.Infof("Setting log level to %v", level)
	log.SetLevel(level)
}

// GetStorageArrays retrieves storage arrays from the provided secret parameters
// and populates the opts with the corresponding configurations.
//
// secretParams: A Viper instance containing the secret parameters.
// opts: A pointer to an Opts struct where the storage array configurations will be stored.
//
// If no storage arrays are declared, it logs "No storage array declared."
// If storage arrays are declared but empty, it logs "No storage arrays found."
// Otherwise, it processes each storage array, extracting labels and parameters.
func GetStorageArrays(secretParams *viper.Viper, opts *Opts) {
	if secretParams.Get("storagearrays") == nil {
		log.Println("No storage arrays declared.")
		return
	}
	storageArrays := secretParams.Get("storagearrays").([]interface{})

	if len(storageArrays) == 0 {
		log.Println("No storage array declared.")
	} else {
		for _, storageArray := range storageArrays {
			storageArrayMap := storageArray.(map[string]interface{})
			storageArrayID := storageArrayMap["storagearrayid"].(string)
			if storageArrayMap["labels"] == nil {
				storageArrayMap["labels"] = make(map[string]interface{})
			}
			if storageArrayMap["parameters"] == nil {
				storageArrayMap["parameters"] = make(map[string]interface{})
			}
			storageArrayConfig := StorageArrayConfig{
				Labels:     storageArrayMap["labels"].(map[string]interface{}),
				Parameters: storageArrayMap["parameters"].(map[string]interface{}),
			}
			opts.StorageArrays[storageArrayID] = storageArrayConfig
		}
	}
}

func (s *service) BeforeServe(
	ctx context.Context, _ *gocsi.StoragePlugin, _ net.Listener,
) error {
	defer func() {
		fields := map[string]interface{}{
			"endpoint":                 s.opts.Endpoint,
			"useProxy":                 s.opts.UseProxy,
			"ProxyServiceHost":         s.opts.ProxyServiceHost,
			"ProxyServicePort":         s.opts.ProxyServicePort,
			"user":                     s.opts.User,
			"password":                 "",
			"systemname":               s.opts.SystemName,
			"nodename":                 s.opts.NodeName,
			"insecure":                 s.opts.Insecure,
			"thickprovision":           s.opts.Thick,
			"privatedir":               s.privDir,
			"autoprobe":                s.opts.AutoProbe,
			"enableblock":              s.opts.EnableBlock,
			"enablechap":               s.opts.EnableCHAP,
			"portgroups":               s.opts.PortGroups,
			"clusterprefix":            s.opts.ClusterPrefix,
			"transport":                s.opts.TransportProtocol,
			"mode":                     s.mode,
			"drivername":               s.opts.DriverName,
			"iscsichapuser":            s.opts.CHAPUserName,
			"iscsichappassword":        "",
			"nodenametemplate":         s.opts.NodeNameTemplate,
			"modifyHostName":           s.opts.ModifyHostName,
			"replicationContextPreix":  s.opts.ReplicationContextPrefix,
			"replicationPrefix":        s.opts.ReplicationPrefix,
			"isHealthMonitorEnabled":   s.opts.IsHealthMonitorEnabled,
			"isTopologyControlEnabled": s.opts.IsTopologyControlEnabled,
			"isVsphereEnabled":         s.opts.IsVsphereEnabled,
			"VspherePortGroups":        s.opts.VSpherePortGroup,
			"VsphereHostNames":         s.opts.VSphereHostName,
			"VsphereHostURL":           s.opts.VCenterHostURL,
			"VsphereHostUsername":      s.opts.VCenterHostUserName,
			"isPodmonEnabled":          s.opts.IsPodmonEnabled,
			"PodmonPort":               s.opts.PodmonPort,
			"PodmonFrequency":          s.opts.PodmonPollingFreq,
		}

		if s.opts.Password != "" {
			fields["password"] = "******"
		}
		if s.opts.CHAPPassword != "" {
			fields["iscsichappassword"] = "******"
		}

		log.WithFields(fields).Infof("configured %s", s.getDriverName())
	}()
	// setting array related data to envs. by reading it from config-map - Needs refactoring
	if err := setArrayConfigEnvs(ctx); err != nil {
		log.Errorf("Failed to set array config envs: %v", err)
	}

	configFilePath, ok := csictx.LookupEnv(ctx, EnvConfigFilePath)
	if !ok {
		log.Warningf("Unable to read X_CSI_POWERMAX_CONFIG_PATH from env. Continuing with default values")
	}

	paramsViper := viper.New()
	paramsViper.SetConfigFile(configFilePath)
	paramsViper.SetConfigType("yaml")

	err := paramsViper.ReadInConfig()
	// if unable to read configuration file, set defaults
	if err != nil {
		log.WithError(err).Error("unable to read config file")
		setLogFormatAndLevel(&log.TextFormatter{
			DisableColors: true,
			FullTimestamp: true,
		}, log.DebugLevel)
		// set X_CSI_LOG_LEVEL so that gocsi doesn't overwrite the loglevel set by us
		_ = os.Setenv(gocsi.EnvVarLogLevel, log.DebugLevel.String())
	} else {
		updateDriverConfigParams(paramsViper)
	}
	paramsViper.WatchConfig()
	paramsViper.OnConfigChange(func(e fsnotify.Event) {
		log.Println("Received event for config file change:", e.Name)
		updateDriverConfigParams(paramsViper)
	})

	s.StartLockManager(defaultLockCleanupDuration * time.Hour)
	if lockWorker == nil {
		lockWorker = new(lockWorkers)
	}
	s.storagePoolCacheDuration = StoragePoolCacheDuration
	// get the SP's operating mode.
	s.mode = csictx.Getenv(ctx, gocsi.EnvVarMode)

	if s.isNode() {
		// Reading Topology filters from the config file
		topoConfigFilePath, ok := csictx.LookupEnv(ctx, EnvTopoConfigFilePath)
		if !ok {
			log.Warningf("Unable to read X_CSI_POWERMAX_TOPOLOGY_CONFIG_PATH from env. Continuing with default topology keys")
		} else {
			s.topologyConfig, err = ReadConfig(topoConfigFilePath)
			if err != nil {
				log.Warningf("continuing with default topology keys")
			} else {
				log.Debug("processing topology config map")
				s.ParseConfig()
			}
		}
	}
	opts := Opts{}
	if ep, ok := csictx.LookupEnv(ctx, EnvDriverName); ok {
		opts.DriverName = ep
	}

	if user, ok := csictx.LookupEnv(ctx, EnvUser); ok {
		opts.User = user
	}
	if opts.User == "" {
		opts.User = "admin"
	}
	if pw, ok := csictx.LookupEnv(ctx, EnvPassword); ok {
		opts.Password = pw
	}

	if useSecret, ok := csictx.LookupEnv(ctx, EnvRevProxyUseSecret); ok && useSecret == "true" {

		secretPath := csictx.Getenv(ctx, EnvRevProxySecretPath)
		secretNameFromPath := filepath.Base(secretPath)
		secretPathFromPath := filepath.Dir(secretPath)

		secretParams := viper.New()
		secretParams.SetConfigName(secretNameFromPath)
		secretParams.SetConfigType("yaml")
		secretParams.AddConfigPath(secretPathFromPath)

		err := secretParams.ReadInConfig()
		if err != nil {
			log.Errorf("Secret mandated, but secret file not found %s.", err)
		}

		// Access the managementservers key (which is a slice of maps)
		managementServers := secretParams.Get("managementservers").([]interface{})
		// Ensure there's at least one server and extract username/password
		if len(managementServers) > 0 {
			// Access the first element of the managementServers slice, which is a map
			server := managementServers[0].(map[string]interface{})

			// Extract the username and password
			User := server["username"].(string)
			Password := server["password"].(string)

			opts.User = User
			opts.Password = Password
		} else {
			log.Println("No management servers found.")
		}

		opts.StorageArrays = make(map[string]StorageArrayConfig)
		GetStorageArrays(secretParams, &opts)
	}

	if chapuser, ok := csictx.LookupEnv(ctx, EnvISCSICHAPUserName); ok {
		opts.CHAPUserName = chapuser
	}
	if pw, ok := csictx.LookupEnv(ctx, EnvISCSICHAPPassword); ok {
		opts.CHAPPassword = pw
	}
	if nt, ok := csictx.LookupEnv(ctx, EnvNodeNameTemplate); ok {
		opts.NodeNameTemplate = nt
	}

	if name, ok := csictx.LookupEnv(ctx, EnvNodeName); ok {
		shortHostName := strings.Split(name, ".")[0]
		opts.NodeName = shortHostName
		opts.NodeFullName = name
	}
	if portgroups, ok := csictx.LookupEnv(ctx, EnvPortGroups); ok {
		tempList, err := s.parseCommaSeperatedList(portgroups)
		if err != nil {
			return fmt.Errorf("invalid value for %s", EnvPortGroups)
		}
		opts.PortGroups = tempList
	}

	if arrays, ok := csictx.LookupEnv(ctx, EnvManagedArrays); ok {
		opts.ManagedArrays, _ = s.parseCommaSeperatedList(arrays)
	} else {
		log.Error("No managed arrays specified")
		os.Exit(1)
	}

	if kubeConfigPath, ok := csictx.LookupEnv(ctx, EnvKubeConfigPath); ok {
		opts.KubeConfigPath = kubeConfigPath
	}

	// set default values for replication prefix since replicator sidecar is not needed for Metro feature.
	if replicationContextPrefix, ok := csictx.LookupEnv(ctx, EnvReplicationContextPrefix); ok {
		opts.ReplicationContextPrefix = replicationContextPrefix
	} else {
		opts.ReplicationContextPrefix = ReplicationContextPrefix
	}
	if replicationPrefix, ok := csictx.LookupEnv(ctx, EnvReplicationPrefix); ok {
		opts.ReplicationPrefix = replicationPrefix
	} else {
		opts.ReplicationPrefix = ReplicationPrefix
	}

	if MaxVolumesPerNode, ok := csictx.LookupEnv(ctx, EnvMaxVolumesPerNode); ok {
		val, err := strconv.ParseInt(MaxVolumesPerNode, 10, 64)
		if err != nil {
			log.Warningf("error while parsing env variable '%s', %s, defaulting to 0", EnvMaxVolumesPerNode, err)
			opts.MaxVolumesPerNode = 0
		} else {
			opts.MaxVolumesPerNode = val
		}
	}

	if podmonPort, ok := csictx.LookupEnv(ctx, EnvPodmonArrayConnectivityAPIPORT); ok {
		opts.PodmonPort = fmt.Sprintf(":%s", podmonPort)
	}

	if podmonPollRate, ok := csictx.LookupEnv(ctx, EnvPodmonArrayConnectivityPollRate); ok {
		opts.PodmonPollingFreq = podmonPollRate
	}

	if tlsCertDir, ok := csictx.LookupEnv(ctx, EnvTLSCertDirName); ok {
		opts.TLSCertDir = tlsCertDir
	}

	opts.TransportProtocol = s.getTransportProtocolFromEnv()
	opts.ProxyServiceHost, opts.ProxyServicePort, opts.UseProxy = s.getProxySettingsFromEnv()
	if !opts.UseProxy && !inducedMockReverseProxy {
		err := fmt.Errorf("CSI reverseproxy service host or port not found, CSI reverseproxy not installed properly")
		log.Error(err.Error())
		return err
	}
	opts.GrpcMaxThreads = 4
	if maxThreads, ok := csictx.LookupEnv(ctx, EnvGrpcMaxThreads); ok {
		maxIntThreads, err := strconv.Atoi(maxThreads)
		if err == nil {
			log.Debug(fmt.Sprintf("setting GrpcMaxThreads to %d", maxIntThreads))
			opts.GrpcMaxThreads = maxIntThreads
		}
	}

	if pd, ok := csictx.LookupEnv(ctx, "X_CSI_PRIVATE_MOUNT_DIR"); ok {
		s.privDir = pd
	}
	if s.privDir == "" {
		s.privDir = defaultPrivDir
	}

	if prefix, ok := csictx.LookupEnv(ctx, EnvClusterPrefix); ok {
		if len(prefix) > MaxClusterPrefixLength {
			log.Errorf("Invalid Cluster Prefix specified, exceeds maximum length of %d characters", MaxClusterPrefixLength)
			return fmt.Errorf("Invalid Cluster Prefix specified, exceeds maximum length of %d characters", MaxClusterPrefixLength)
		}
		opts.ClusterPrefix = prefix
	} else {
		return fmt.Errorf("No Cluster Prefix was specified")
	}

	// pb parses an environment variable into a boolean value. If an error
	// is encountered, default is set to false, and error is logged
	pb := func(n string) bool {
		if v, ok := csictx.LookupEnv(ctx, n); ok {
			b, err := strconv.ParseBool(v)
			if err != nil {
				log.WithField(n, v).Debug(
					"invalid boolean value. defaulting to false")
				return false
			}
			return b
		}
		return false
	}
	// isBoolEnvVar checks an environment variable to see if it is
	// "true" or "false" or "TRUE" or "FALSE". If so, it returns true.
	// If not, or if the environment variable is not set, returns false
	isBoolEnvVar := func(n string) bool {
		if v, ok := csictx.LookupEnv(ctx, n); ok {
			v = strings.ToLower(v)
			if v == "true" || v == "false" {
				return true
			}
		}
		return false
	}

	opts.Insecure = pb(EnvSkipCertificateValidation)
	opts.Thick = pb(EnvThick)
	opts.AutoProbe = pb(EnvAutoProbe)
	if isBoolEnvVar(EnvEnableBlock) {
		opts.EnableBlock = pb(EnvEnableBlock)
	} else { // defaults to EnableBlock true
		opts.EnableBlock = true
	}
	opts.EnableCHAP = pb(EnvEnableCHAP)
	opts.ModifyHostName = pb(EnvModifyHostName)
	opts.IsHealthMonitorEnabled = pb(EnvHealthMonitorEnabled)
	opts.IsTopologyControlEnabled = pb(EnvTopologyFilterEnabled)
	opts.IsPodmonEnabled = pb(EnvPodmonEnabled)
	opts.IsVsphereEnabled = pb(EnvVSphereEnabled)
	if opts.IsVsphereEnabled {
		// read port group
		if vPG, ok := csictx.LookupEnv(ctx, EnvVSpherePortGroup); ok {
			opts.VSpherePortGroup = vPG
		}
		// read host (initiator group)
		if vHN, ok := csictx.LookupEnv(ctx, EnvVSphereHostName); ok {
			opts.VSphereHostName = vHN
		}
		// read vCenter host url
		if vURL, ok := csictx.LookupEnv(ctx, EnvVCHost); ok {
			opts.VCenterHostURL = vURL
		}
		// read vCenter host username
		if vUN, ok := csictx.LookupEnv(ctx, EnvVCUsername); ok {
			opts.VCenterHostUserName = vUN
		}
		// read vCenter host password
		if vPWD, ok := csictx.LookupEnv(ctx, EnvVCPassword); ok {
			opts.VCenterHostPassword = vPWD
		}
	}
	s.opts = opts

	// setup the k8sClient
	if s.k8sUtils == nil {
		s.k8sUtils, err = k8sutils.Init(s.opts.KubeConfigPath)
		if err != nil {
			return fmt.Errorf("error creating k8sClient %s", err.Error())
		}
	}

	// setup the iscsi client
	iscsiOpts := make(map[string]string, 0)
	if chroot, ok := csictx.LookupEnv(ctx, EnvNodeChroot); ok {
		iscsiOpts[goiscsi.ChrootDirectory] = chroot
	}
	s.iscsiClient = goiscsi.NewLinuxISCSI(iscsiOpts)

	// setup the nvme client
	nvmetcpOpts := make(map[string]string, 0)
	if chroot, ok := csictx.LookupEnv(ctx, EnvNodeChroot); ok {
		nvmetcpOpts[gonvme.ChrootDirectory] = chroot
	}
	s.nvmetcpClient = gonvme.NewNVMe(nvmetcpOpts)

	if s.isNode() && len(s.opts.StorageArrays) > 0 {
		s.opts.ManagedArrays = s.filterArraysByZoneInfo(s.opts.StorageArrays)
		log.Infof("Node will have access to the following arrays: %v", s.opts.ManagedArrays)
	}

	if _, ok := csictx.LookupEnv(ctx, "X_CSI_POWERMAX_NO_PROBE_ON_START"); !ok {
		if s.isController() {
			if err := s.controllerProbe(ctx); err != nil {
				return err
			}
		}

		if s.isNode() {
			if err := s.nodeProbe(ctx); err != nil {
				return err
			}
		}
	}

	if s.isNode() {
		if err := s.nodeStartup(ctx); err != nil {
			return err
		}
	}

	if s.isController() {
		s.NewDeletionWorker(s.opts.ClusterPrefix, s.opts.ManagedArrays)
	}

	if s.isController() {
		s.startSnapCleanupWorker() // #nosec G20
		if s.snapCleaner == nil {
			s.snapCleaner = new(snapCleanupWorker)
		}
	}

	return nil
}

// ParseConfig will make respective allowed and denied list as per the topology config
func (s *service) ParseConfig() {
	// make allowed list
	s.allowedTopologyKeys = readNodesRules(s.topologyConfig.AllowedConnections)
	// make denied list
	s.deniedTopologyKeys = readNodesRules(s.topologyConfig.DeniedConnections)

	log.Infof("proccessed allowed list: (%+v)", s.allowedTopologyKeys)
	log.Infof("proccessed denied list: (%+v)", s.deniedTopologyKeys)
}

func (s *service) isNode() bool {
	return strings.EqualFold(s.mode, "node")
}

func (s *service) isController() bool {
	return strings.EqualFold(s.mode, "controller")
}

func readNodesRules(connections []NodeConfig) map[string][]string {
	keys := map[string][]string{}
	for _, nodeConfig := range connections {
		nodeName := nodeConfig.NodeName
		var arrayToConTyp []string
		for _, rule := range nodeConfig.Rules {
			arrayHW := strings.Split(rule, ":")
			array := arrayHW[0]
			if array == "*" {
				array = ""
			}
			if len(arrayHW) < 2 {
				log.Warningf("incorrect config for %s skipping rule (%s)", nodeName, rule)
				continue
			}
			hws := strings.Split(arrayHW[1], "/")
			for _, typ := range hws {
				if typ == "*" {
					typ = ""
				}
				arrayToConTyp = append(arrayToConTyp, array+"."+strings.ToLower(typ))
			}
			keys[nodeName] = arrayToConTyp
		}
	}
	return keys
}

// ReadConfig will read topology configmap on the default path into TopologyConfig struct
func ReadConfig(configPath string) (*TopologyConfig, error) {
	topoViper := viper.New()
	topoViper.SetConfigFile(configPath)
	topoViper.SetConfigType("yaml")

	err := topoViper.ReadInConfig()
	// if unable to read configuration file, set defaults
	if err != nil {
		log.WithError(err).Error("unable to read topology config file")
		return nil, err
	}
	var config TopologyConfig
	err = topoViper.Unmarshal(&config)
	if err != nil {
		log.WithError(err).Error("unable to unmarshal topology config")
		return nil, err
	}
	return &config, nil
}

func (s *service) RegisterAdditionalServers(server *grpc.Server) {
	csiext.RegisterReplicationServer(server, s)
	migrext.RegisterMigrationServer(server, s)
	podmon.RegisterPodmonServer(server, s)
}

func (s *service) getProxySettingsFromEnv() (string, string, bool) {
	serviceHost := ""
	servicePort := ""
	if proxySidecarPort, ok := csictx.LookupEnv(context.Background(), EnvSidecarProxyPort); ok {
		serviceHost = "0.0.0.0"
		servicePort = proxySidecarPort
		return serviceHost, servicePort, true
	}
	if proxyServiceName, ok := csictx.LookupEnv(context.Background(), EnvUnisphereProxyServiceName); ok {
		if proxyServiceName != "none" {
			serviceHost = proxyServiceName
			// Change it to uppercase
			proxyServiceName = strings.ToUpper(proxyServiceName)
			// Change all "-" to underscores
			proxyServiceName = strings.Replace(proxyServiceName, "-", "_", -1)
			servicePortEnv := fmt.Sprintf("%s_SERVICE_PORT", proxyServiceName)
			if sp, ok := csictx.LookupEnv(context.Background(), servicePortEnv); ok {
				servicePort = sp
				if serviceHost == "" || servicePort == "" {
					log.Warning("Either ServiceHost and ServicePort is set to empty")
					return "", "", false
				}
				return serviceHost, servicePort, true
			}
		}
	}
	return "", "", false
}

func (s *service) getTransportProtocolFromEnv() string {
	tp, ok := csictx.LookupEnv(context.Background(), EnvPreferredTransportProtocol)
	if !ok {
		return ""
	}
	tp = strings.ToUpper(tp)
	switch tp {
	case "FIBRE":
		return "FC"
	case "FC", "ISCSI", "NVMETCP", "":
		return tp
	case "AUTO":
		return ""
	default:
		log.Errorf("Invalid transport protocol: %s, valid values AUTO, FC, ISCSI or NVMETCP", tp)
		return ""
	}
}

// get the amount of time to retry pmax calls
func (s *service) GetPmaxTimeoutSeconds() int64 {
	return s.pmaxTimeoutSeconds
}

// SetPmaxTimeoutSeconds sets the maximum amount of time to retry pmax calls
func (s *service) SetPmaxTimeoutSeconds(seconds int64) {
	s.pmaxTimeoutSeconds = seconds
}

// parseCommaSeperatedList validates and splits a comma seperated list
func (s *service) parseCommaSeperatedList(values string) ([]string, error) {
	results := make([]string, 0)
	st := strings.Split(values, ",")
	for i := range st {
		t := strings.TrimSpace(st[i])
		if t != "" {
			results = append(results, t)
		}
	}
	return results, nil
}

func (s *service) createPowerMaxClients(ctx context.Context) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	endPoint := ""
	if s.opts.UseProxy {
		endPoint = fmt.Sprintf("https://%s:%s", s.opts.ProxyServiceHost, s.opts.ProxyServicePort)
	} else {
		endPoint = s.opts.Endpoint
	}

	// Create our PowerMax API client, if needed
	if s.adminClient == nil {
		applicationName := ApplicationName + "/" + "v" + core.SemVer
		tlsCertFile := filepath.Join(s.opts.TLSCertDir, defaultCertFile)
		c, err := pmax.NewClientWithArgs(endPoint, applicationName, s.opts.Insecure, !s.opts.DisableCerts, tlsCertFile)
		if err != nil {
			return status.Errorf(codes.FailedPrecondition,
				"unable to create PowerMax client: %s", err.Error())
		}
		s.adminClient = c

		for i := 0; i < maxAuthenticateRetryCount; i++ {
			err = s.adminClient.Authenticate(ctx, &pmax.ConfigConnect{
				Endpoint: endPoint,
				Username: s.opts.User,
				Password: s.opts.Password,
			})
			if err == nil {
				break
			}

			log.Infof("Error authenticating : %s", err)
			time.Sleep(10 * time.Second)
		}
		if err != nil {
			s.adminClient = nil
			return status.Errorf(codes.FailedPrecondition,
				"unable to login to Unisphere: %s", err.Error())
		}

		// Filter out a list of locally connected list of arrays, and
		// initialize the PowerMax client for those array only
		managedArrays := make([]string, 0, len(s.opts.ManagedArrays))
		log.Infof("Managed arrays - %v", s.opts.ManagedArrays)
		for _, array := range s.opts.ManagedArrays {
			symmetrix, err := s.adminClient.GetSymmetrixByID(ctx, array)
			if err != nil {
				log.Errorf("Failed to fetch details for array: %s. Reason: [%s]", array, err.Error())
			} else {
				if symmetrix.Local {
					managedArrays = append(managedArrays, array)
				}
			}
		}
		if len(managedArrays) == 0 {
			log.Error("None of the managed arrays specified are locally connected")
			os.Exit(1)
		}
		s.opts.ManagedArrays = managedArrays
		err = symmetrix.Initialize(s.opts.ManagedArrays, s.adminClient)
		if err != nil {
			return err
		}
	}

	return nil
}

// TODO Revist for additional attributes
func (s *service) getCSIVolume(vol *types.Volume) *csi.Volume {
	vi := &csi.Volume{
		VolumeId:      vol.VolumeID,
		CapacityBytes: int64(vol.CapacityCYL) * cylinderSizeInBytes,
	}
	return vi
}

func (s *service) getClusterPrefix() string {
	return s.opts.ClusterPrefix
}

func (s *service) getDriverName() string {
	if s.opts.DriverName == "" {
		return Name
	}
	return s.opts.DriverName
}

func setLogFields(ctx context.Context, fields log.Fields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey(logFields), fields)
}

func getLogFields(ctx context.Context) log.Fields {
	fields, ok := ctx.Value(contextKey(logFields)).(log.Fields)
	if !ok {
		fields = log.Fields{}
	}

	csiReqID, ok := ctx.Value(csictx.RequestIDKey).(string)
	if !ok {
		return fields
	}

	fields["RequestID"] = csiReqID
	return fields
}

// SetPollingFrequency reads the pollingFrequency from Env, sets default vale if ENV not found
func (s *service) SetPollingFrequency(ctx context.Context) int64 {
	var pollingFrequency int64
	s.pollingFrequencyMutex.Lock()
	defer s.pollingFrequencyMutex.Unlock()
	if pollRateEnv, ok := csictx.LookupEnv(ctx, EnvPodmonArrayConnectivityPollRate); ok {
		if pollingFrequency, _ = strconv.ParseInt(pollRateEnv, 10, 32); pollingFrequency != 0 {
			log.Debugf("use pollingFrequency as %d seconds", pollingFrequency)
			s.pollingFrequencyInSeconds = pollingFrequency
			return s.pollingFrequencyInSeconds
		}
	}
	log.Debugf("use default pollingFrequency as %d seconds", DefaultPodmonPollRate)
	s.pollingFrequencyInSeconds = DefaultPodmonPollRate
	return s.pollingFrequencyInSeconds
}

// GetPollingFrequency returns the pollingFrequency
func (s *service) GetPollingFrequency() int64 {
	s.pollingFrequencyMutex.Lock()
	defer s.pollingFrequencyMutex.Unlock()
	return s.pollingFrequencyInSeconds
}

func setArrayConfigEnvs(ctx context.Context) error {
	log.Info("---------Inside setArrayConfigEnvs function----------")
	// set additional driver configs moved from envs.
	configFilePath, ok := csictx.LookupEnv(ctx, EnvArrayConfigPath)
	if !ok {
		return errors.New("unable to read X_CSI_POWERMAX_ARRAY_CONFIG_PATH from env")
	}

	paramsViper := viper.New()
	paramsViper.SetConfigFile(configFilePath)
	paramsViper.SetConfigType("yaml")

	err := paramsViper.ReadInConfig()
	// if unable to read configuration file, set defaults
	if err != nil {
		log.WithError(err).Error("unable to read array config file")
		setLogFormatAndLevel(&log.TextFormatter{
			DisableColors: true,
			FullTimestamp: true,
		}, log.DebugLevel)
	}
	portgroups := paramsViper.GetString(PortGroups)
	if portgroups != "" {
		log.Info("Read PortGroups from config file:", portgroups)
		_ = os.Setenv(PortGroups, portgroups)
	}
	protocol := paramsViper.GetString(Protocol)
	if protocol != "" {
		log.Info("Read protocol from config file:", protocol)
		_ = os.Setenv(Protocol, protocol)
	}
	endpoint := paramsViper.GetString(EnvEndpoint)
	if endpoint != "" {
		log.Info("Read endpoint from config file:", endpoint)
		_ = os.Setenv(EnvEndpoint, endpoint)
	}
	managedArrays := paramsViper.GetString(ManagedArrays)
	if managedArrays != "" {
		log.Info("Managed arrays from config file:", managedArrays)
		_ = os.Setenv(ManagedArrays, managedArrays)
	}

	if useSecret, ok := csictx.LookupEnv(ctx, EnvRevProxyUseSecret); ok && useSecret == "true" {

		secretPath := csictx.Getenv(ctx, EnvRevProxySecretPath)
		secretNameFromPath := filepath.Base(secretPath)
		secretPathFromPath := filepath.Dir(secretPath)

		secretParams := viper.New()
		secretParams.SetConfigName(secretNameFromPath)
		secretParams.SetConfigType("yaml")
		secretParams.AddConfigPath(secretPathFromPath)

		err := secretParams.ReadInConfig()
		if err != nil {
			log.Errorf("Secret mandated, but secret file not found %s", err)
		}

		// Access the managementservers key (which is a slice of maps)
		managementServers := secretParams.Get("managementservers").([]interface{})

		// Ensure there's at least one server and extract username/password
		if len(managementServers) > 0 {
			// Access the first element of the managementServers slice, which is a map
			server := managementServers[0].(map[string]interface{})

			// Extract the username and password
			endpoint = server["endpoint"].(string)
		} else {
			fmt.Println("No management servers found.")
		}
	}

	return nil
}

func (s *service) filterArraysByZoneInfo(storageArrays map[string]StorageArrayConfig) []string {
	zonedArrays := make([]string, 0, 1)
	unzonedArrays := make([]string, 0, len(storageArrays))

	nodeLabels, err := s.k8sUtils.GetNodeLabels(s.opts.NodeFullName)
	if err != nil {
		log.Warnf("failed to get node labels: '%s'", err.Error())
	}

	for arrayID, arrayConfig := range storageArrays {
		keepArray := true
		arrayLabels := arrayConfig.Labels
		if len(arrayLabels) != 0 {
			for arrayLabelKey, arrayLabelVal := range arrayLabels {
				if nodeLabelVal, ok := nodeLabels[arrayLabelKey]; !ok || nodeLabelVal != arrayLabelVal.(string) {
					keepArray = false
					break
				}
			}

			if keepArray {
				zonedArrays = append(zonedArrays, arrayID)
			}
		} else {
			unzonedArrays = append(unzonedArrays, arrayID)
		}
	}

	if len(zonedArrays) == 1 {
		return zonedArrays
	}

	if len(zonedArrays) > 1 {
		log.Error("More than one zoned arrays found, only one zoned array per node is supported")
		return []string{}
	}

	return unzonedArrays
}
