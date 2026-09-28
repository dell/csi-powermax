/*
 Copyright © 2021-2026 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dell/csmlog"
	"github.com/dell/gonvme"

	"github.com/dell/csi-powermax/v2/k8sutils"
	"github.com/dell/dell-csi-extensions/podmon"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"
	"golang.org/x/sync/singleflight"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	"google.golang.org/grpc"

	drivermetrics "github.com/dell/csi-powermax/v2/pkg/metrics"
	"github.com/dell/csi-powermax/v2/service/collectors"
	csmserver "github.com/dell/csm-metrics-common/pkg/server"
	"github.com/dell/gocsi"
	csictx "github.com/dell/gocsi/context"
	"github.com/dell/goiscsi"
	api "github.com/dell/gopowermax/v2/api"
	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/prometheus/client_golang/prometheus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/core"
	migrext "github.com/dell/dell-csi-extensions/migration"
	csiext "github.com/dell/dell-csi-extensions/replication"
	pmax "github.com/dell/gopowermax/v2"
	coordinationv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	typedcorev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/record"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Constants for the service
const (
	Name            = "csi-powermax.dellemc.com"         // Name is the name of the CSI plug-in.
	ApplicationName = "CSI Driver for Dell EMC PowerMax" // ApplicationName is the name used to register with Powermax REST APIs
	// ProxyAuthTokenHeader is the HTTP header for shared auth token
	ProxyAuthTokenHeader       = "X-Proxy-Auth-Token" // #nosec G101 -- header name, not a credential
	defaultPrivDir             = "/dev/disk/csi-powermax"
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
	ManagedArrays        = "X_CSI_MANAGED_ARRAYS"
	defaultCertFile      = "tls.crt"
	defaultSgVolumeLimit = 4000
)

type contextKey string // specific string type used for context keys

var inducedMockReverseProxy bool // for testing only

// Update when the manifest version changes.
var ManifestSemver string

// Manifest is the SP's manifest.
var Manifest = map[string]string{
	"semver": ManifestSemver,
	"formed": core.CommitTime.Format(time.RFC1123),
}

var sgVolumeLimit = defaultSgVolumeLimit

// PodmonAPIToken is the shared secret token for authenticating podmon API requests.
// This variable is package-scoped; each driver binary maintains its own instance.
var PodmonAPIToken string

// Service is the CSI Mock service provider.
type Service interface {
	csi.ControllerServer
	csi.GroupControllerServer
	csi.IdentityServer
	csi.NodeServer
	csiext.ReplicationServer
	migrext.MigrationServer
	BeforeServe(context.Context, *gocsi.StoragePlugin, net.Listener) error
	RegisterAdditionalServers(server *grpc.Server)
}

// Opts defines service configuration options.
type Opts struct {
	Endpoint                          string
	UseProxy                          bool
	ProxyServiceHost                  string
	ProxyServicePort                  string
	User                              string
	Password                          string `json:"-"`
	SystemName                        string
	NodeName                          string
	NodeFullName                      string
	TransportProtocol                 string
	DriverName                        string
	CHAPUserName                      string
	CHAPPassword                      string
	Insecure                          bool
	Thick                             bool
	AutoProbe                         bool
	EnableBlock                       bool
	EnableCHAP                        bool
	PortGroups                        []string
	ClusterPrefix                     string
	ManagedArrays                     []string
	DisableCerts                      bool   // used for unit testing only
	Lsmod                             string // used for unit testing only
	EnableSnapshotCGDelete            bool   // when snapshot deleted, enable deleting of all snaps in the CG of the snapshot
	EnableListVolumesSnapshots        bool   // when listing volumes, include snapshots and volumes
	GrpcMaxThreads                    int    // Maximum threads configured in grpc
	NonDefaultRetries                 bool   // Indicates if non-default retry values to be used for deletion worker, only for unit testing
	NodeNameTemplate                  string
	ModifyHostName                    bool
	ReplicationContextPrefix          string // Enables sidecars to read required information from volume context
	ReplicationPrefix                 string // Used as a prefix to find out if replication is enabled
	IsHealthMonitorEnabled            bool   // used to check if health monitor for volume is enabled
	IsCSIAddonsReplicationEnabled     bool   // used to check if CSI-Addons replication/volume group servers should be registered
	IsTopologyControlEnabled          bool   // used to filter topology keys based on user config
	IsVsphereEnabled                  bool   // used to check if vSphere is enabled
	VSpherePortGroup                  string // port group for vsphere
	VSphereHostName                   string // host (initiator group) for vsphere
	VCenterHostURL                    string // vCenter host url
	VCenterHostUserName               string // vCenter host username
	VCenterHostPassword               string // vCenter password
	MaxVolumesPerNode                 int64  // to specify volume limits
	KubeConfigPath                    string // to specify k8s configuration to be used CSI driver
	IsPodmonEnabled                   bool   // used to indicate that podmon is enabled
	PodmonPort                        string // to indicates the port to be used for exposing podmon API health
	PodmonPollingFreq                 string // indicates the polling frequency to check array connectivity
	PodmonAPIToken                    string `json:"-"` // shared secret for podmon API authentication; never serialized
	TLSCertDir                        string
	StorageArrays                     map[string]StorageArrayConfig
	dynamicSGEnabled                  bool
	sgVolumeLimit                     int
	IsMetroSiteFailureHandlingEnabled bool          // enables SRDF/Metro site-failure handling
	MetroStateCheckTimeout            time.Duration // per-call timeout for CheckMetroState (default 15s)
	MetroQueueWarningThreshold        int           // deferred-op queue warning threshold (0 → default 75)
	MetroQueueHardLimit               int           // deferred-op queue hard limit (0 → default 100)
	MetroReconciliationBackoff        time.Duration // reconciliation base backoff (0 → default 5s)
	// FsCheckEnabled enables file system check before mount
	FsCheckEnabled bool
	// FsCheckMode is the FS check operation mode: "checkOnly" or "checkAndRepair"
	FsCheckMode string
	// HostManagementMode controls how the driver manages host objects:
	// "create" (default) creates new hosts, "adopt" discovers and adopts
	// pre-existing BFS hosts by WWPN match.
	HostManagementMode string
	// HostAdoptionMinOverlapRatio controls the minimum overlap ratio for
	// host adoption validation. Valid values: 0.0 to 1.0. Default is 0.5 for
	// directional relaxation with strict majority. For 2-WWPN systems, this
	// is treated as 1.0 (100% coverage required).
	HostAdoptionMinOverlapRatio float64
	// NVMeTCPConnMode selects who owns NVMe/TCP fabric sessions:
	// "driver" (default) has the driver discover and connect, "host" has the
	// driver use host-established sessions only. Deployment-wide; it applies to
	// every array in X_CSI_MANAGED_ARRAYS.
	NVMeTCPConnMode string
}

// adoptedHostInfo stores information about a BFS host adopted by the driver.
type adoptedHostInfo struct {
	HostID   string        // original host name on the array (preserved, not renamed)
	Protocol string        // transport protocol used (e.g., "FC")
	BootLUNs []bootLUNInfo // detected boot/pre-existing LUN storage groups (populated by detectBootLUNs)
}

// StorageArrayConfig represents the configuration of a storage array in the config file
type StorageArrayConfig struct {
	Labels     map[string]interface{} `yaml:"labels,omitempty"`
	Parameters map[string]interface{} `yaml:"parameters,omitempty"`
}

// hasMetroSiteLabels returns true if both the local and remote arrays
// have at least one label configured in their StorageArrayConfig,
// indicating a non-uniform (site-aware) Metro deployment.
func (s *service) hasMetroSiteLabels(localSymID, remoteSymID string) bool {
	localCfg, localOk := s.opts.StorageArrays[localSymID]
	remoteCfg, remoteOk := s.opts.StorageArrays[remoteSymID]
	if !localOk || !remoteOk {
		return false
	}
	return len(localCfg.Labels) > 0 && len(remoteCfg.Labels) > 0
}

// shouldSkipRemotePublish checks whether the remote array publish should
// be skipped for a Metro volume. Returns true if remoteSymID is empty
// or if the node's host object does not exist on the remote array
// (indicating non-uniform connectivity).
func (s *service) shouldSkipRemotePublish(ctx context.Context, pmaxClient pmax.Pmax, remoteSymID, hostID string) (bool, error) {
	if remoteSymID == "" {
		csmlog.WithContext(ctx).Debugf("shouldSkipRemotePublish: skipping (no remote/target array specified) for host %s", hostID)
		return true, nil
	}
	csmlog.WithContext(ctx).Debugf("shouldSkipRemotePublish: checking if host %s exists on array %s", hostID, remoteSymID)
	_, err := pmaxClient.GetHostByID(ctx, remoteSymID, hostID)
	if err != nil {
		csmlog.WithContext(ctx).Infof("Host %s not found on array %s (non-uniform Metro), publish on this array will be skipped: %s",
			hostID, remoteSymID, err.Error())
		return true, nil
	}
	csmlog.WithContext(ctx).Debugf("Host %s found on array %s, publish on this array will proceed", hostID, remoteSymID)
	return false, nil
}

// nodeHasHostOnArray checks whether any of the node's protocol-specific host IDs
// (iSCSI, FC, NVMeTCP) exist on the given array. Returns false if the host list
// cannot be retrieved or no matching host is found.
func (s *service) nodeHasHostOnArray(ctx context.Context, pmaxClient pmax.Pmax, arrayID, nodeID string) bool {
	iscsiHostID, _, _ := s.GetISCSIHostSGAndMVIDFromNodeID(nodeID)
	fcHostID, _, _ := s.GetFCHostSGAndMVIDFromNodeID(nodeID)
	nvmeHostID, _, _ := s.GetNVMETCPHostSGAndMVIDFromNodeID(nodeID)

	// BFS host adoption: check if this array has an adopted host for this node.
	// Best effort — this is a reachability probe, and a lookup failure simply
	// leaves the standard derived host names in play.
	if adoptedID := s.adoptedHostIDOrEmpty(ctx, arrayID, nodeID); adoptedID != "" {
		csmlog.WithContext(ctx).Debugf("nodeHasHostOnArray: using adopted host %s for array %s", adoptedID, arrayID)
		fcHostID = adoptedID
	}

	hostList, err := pmaxClient.GetHostList(ctx, arrayID)
	if err != nil {
		csmlog.Infof("Could not retrieve host list from array %s for node %s: %s", arrayID, nodeID, err.Error())
		return false
	}
	for _, h := range hostList.HostIDs {
		if h == iscsiHostID || h == fcHostID || h == nvmeHostID {
			return true
		}
	}
	return false
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
	// satisfies the Service interface and provides unimplemented defaults to functions not implemented
	csi.UnimplementedControllerServer
	csi.UnimplementedGroupControllerServer
	csi.UnimplementedIdentityServer
	csi.UnimplementedNodeServer
	csiext.UnimplementedReplicationServer

	opts Opts
	mode string
	// replace this with Unisphere client
	adminClient    pmax.Pmax
	adminClient104 pmax.Pmax
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
	authToken                 string // shared auth token for reverse proxy authentication

	// Timeout for storage pool cache
	storagePoolCacheDuration time.Duration
	metricsRegistry          prometheus.Gatherer
	metricsServer            *csmserver.MetricsServer
	metricsCtxCancel         context.CancelFunc
	metricsWg                sync.WaitGroup
	healthCollectorWg        sync.WaitGroup
	metricsShutdownMutex     sync.Mutex
	// only used for testing, indicates if the deletion worked finished populating queue
	waitGroup sync.WaitGroup

	// Gobrick stuff
	fcConnector      fcConnector
	iscsiConnector   iSCSIConnector
	nvmeTCPConnector NVMeTCPConnector
	dBusConn         dBusConn

	sgSvc *storageGroupSvc

	arrayTransportProtocolMap map[string]string // map of array SN to TransportProtocols
	// adoptedHosts maps array SN to adopted BFS host info. It is written by
	// nodeHostSetup and read by NodeGetInfo and the controller-side resolvers,
	// which can run concurrently, so all access goes through adoptedHostsMutex.
	adoptedHosts        map[string]adoptedHostInfo
	adoptedHostsMutex   sync.RWMutex
	topologyConfig      *TopologyConfig
	allowedTopologyKeys map[string][]string // map of nodes to allowed topology keys
	deniedTopologyKeys  map[string][]string // map of nodes to denied topology keys

	k8sUtils         k8sutils.UtilsInterface
	snapCleaner      *snapCleanupWorker
	spaceReclaimMgr  *SpaceReclamationManager
	volumeJournal    *symmetrix.VolumeJournal
	siteStateTracker *symmetrix.SiteStateTracker
	metroStateCache  *symmetrix.MetroStateCache

	// ownership isolation fields for VolumeJournal CRD
	driverName  string // Name of the driver instance (from CR)
	instanceUID string // Unique identifier of the CSM instance (from CR)

	// metroStateCheckGroup deduplicates concurrent CheckMetroState calls for
	// the same array pair so only one Unisphere round-trip happens.
	metroStateCheckGroup singleflight.Group

	// metroReconcileInFlight guards against concurrent reconciliation runs
	// for the same array so that a second trigger while one is in progress
	// does not replay the same operations twice
	metroReconcileInFlight sync.Map

	// metroEventRecorder is a dedicated Kubernetes EventRecorder for Metro
	// site-failure events, named "csi-powermax-controller".
	// Initialised lazily in initMetroEventRecorder.
	// metroEventBroadcaster is stored so Stop() can call Shutdown() to drain
	// the broadcaster goroutines and prevent a goroutine leak.
	metroEventRecorder     record.EventRecorder
	metroEventBroadcaster  record.EventBroadcaster
	metroEventRecorderOnce sync.Once

	versionCache     *versionCache
	versionCacheOnce sync.Once
	collectorManager *collectors.CollectorManager
	healthCollector  *collectors.PMXDriverHealthCollector

	// capCache tracks per-array capacity utilization/availability for
	// multi-array zone selection. Populated by the background capacity
	// poller started in BeforeServe. nil until the poller is started
	// (e.g. in most existing unit tests), in which case selectArray
	// falls back to a deterministic choice.
	capCache              *capacityCache
	capacityPollInterval  time.Duration
	capacityThresholdFull float64
	capacityPollerCancel  context.CancelFunc

	// multiArrayMetrics exposes the per-array Prometheus metrics.
	// nil when metrics are disabled; all update methods are nil-safe.
	multiArrayMetrics *multiArrayMetrics
}

// New returns a new Service.
func New() Service {
	svc := &service{
		loggedInArrays:     map[string]bool{},
		iscsiTargets:       map[string][]string{},
		loggedInNVMeArrays: map[string]bool{},
		nvmeTargets:        new(sync.Map),
		versionCache:       newVersionCache(),
		volumeJournal:      symmetrix.NewVolumeJournal(),
		siteStateTracker:   symmetrix.NewSiteStateTracker(),
		metroStateCache:    symmetrix.NewMetroStateCache(0),
	}
	svc.sgSvc = newStorageGroupService(svc)
	svc.probeStatus = new(sync.Map)
	return svc
}

func updateDriverConfigParams(v *viper.Viper) {
	// default log format is json unless we read otherwise
	logFormat := "json"
	if v.IsSet(CSILogFormatParam) {
		logFormat = strings.ToLower(v.GetString(CSILogFormatParam))
		if logFormat == "" || (logFormat != "json" && logFormat != "text") {
			csmlog.Info("CSI_LOG_FORMAT not specified or invalid, setting to default (JSON)")
			logFormat = "json"
		}
	}

	level := csmlog.InfoLevel
	if v.IsSet(CSILogLevelParam) {
		logLevel := v.GetString(CSILogLevelParam)
		if logLevel != "" {
			logLevel = strings.ToLower(logLevel)

			var err error

			l, err := csmlog.ParseLevel(logLevel)
			if err != nil {
				csmlog.Errorf("LOG_LEVEL %s value not recognized, setting to default (info): %s ", logLevel, err.Error())
				level = csmlog.InfoLevel
			} else {
				level = l
			}
		}
	}

	setLogFormatAndLevel(logFormat, level)
}

func setLogFormatAndLevel(format string, level csmlog.Level) {
	csmlog.SetFormat(format)
	csmlog.SetLevel(level)
	csmlog.WithFields(csmlog.Fields{
		csmlog.FieldComponent: "driver",
		csmlog.FieldOperation: "ConfigChange",
		"log_level":           level.String(),
		"log_format":          format,
	}).Info("log level and format applied")
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
//
// Multiple entries sharing the same zone label are all accepted (multi-array
// zones are supported natively). A malformed entry (not a map, or missing a
// valid non-empty "storagearrayid") is logged with enough detail to identify
// it and skipped — it does not abort processing of the remaining entries and
// does not panic.
func GetStorageArrays(secretParams *viper.Viper, opts *Opts) {
	if secretParams.Get("storagearrays") == nil {
		csmlog.Info("No storage arrays declared.")
		return
	}
	storageArrays, ok := secretParams.Get("storagearrays").([]interface{})
	if !ok {
		csmlog.Errorf("storagearrays entry is malformed: expected a list of array configurations")
		return
	}

	if len(storageArrays) == 0 {
		csmlog.Info("No storage array declared.")
		return
	}

	for i, storageArray := range storageArrays {
		storageArrayMap, ok := storageArray.(map[string]interface{})
		if !ok {
			csmlog.Errorf("storagearrays entry %d is malformed: expected a map, skipping entry", i)
			continue
		}

		storageArrayID, ok := storageArrayMap["storagearrayid"].(string)
		if !ok || storageArrayID == "" {
			csmlog.Errorf("storagearrays entry %d is missing a valid 'storagearrayid' field, skipping entry", i)
			continue
		}

		labels, ok := storageArrayMap["labels"].(map[string]interface{})
		if storageArrayMap["labels"] != nil && !ok {
			csmlog.Errorf("storage array %s has a malformed 'labels' field, skipping entry", storageArrayID)
			continue
		}
		if labels == nil {
			labels = make(map[string]interface{})
		}

		parameters, ok := storageArrayMap["parameters"].(map[string]interface{})
		if storageArrayMap["parameters"] != nil && !ok {
			csmlog.Errorf("storage array %s has a malformed 'parameters' field, skipping entry", storageArrayID)
			continue
		}
		if parameters == nil {
			parameters = make(map[string]interface{})
		}

		opts.StorageArrays[storageArrayID] = StorageArrayConfig{
			Labels:     labels,
			Parameters: parameters,
		}
	}

	if len(opts.StorageArrays) == 0 {
		csmlog.Warn("No valid storage arrays remained after validation.")
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

		csmlog.WithContext(ctx).WithFields(fields).Infof("configured %s", s.getDriverName())
	}()
	// setting array related data to envs. by reading it from config-map - Needs refactoring
	if err := setArrayConfigEnvs(ctx); err != nil {
		csmlog.WithContext(ctx).Errorf("Failed to set array config envs: %v", err)
	}

	configFilePath, ok := csictx.LookupEnv(ctx, EnvConfigFilePath)
	if !ok {
		csmlog.WithContext(ctx).Warnf("Unable to read X_CSI_POWERMAX_CONFIG_PATH from env. Continuing with default values")
	}

	paramsViper := viper.New()
	paramsViper.SetConfigFile(configFilePath)
	paramsViper.SetConfigType("yaml")

	err := paramsViper.ReadInConfig()
	// if unable to read configuration file, set defaults
	if err != nil {
		csmlog.WithContext(ctx).Errorf("Unable to read config file: %v", err)
		setLogFormatAndLevel("json", csmlog.InfoLevel)
	} else {
		updateDriverConfigParams(paramsViper)
	}
	paramsViper.WatchConfig()
	paramsViper.OnConfigChange(func(e fsnotify.Event) {
		csmlog.Info("Received event for config file change: " + e.Name)
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
			csmlog.WithContext(ctx).Warnf("Unable to read X_CSI_POWERMAX_TOPOLOGY_CONFIG_PATH from env. Continuing with default topology keys")
		} else {
			s.topologyConfig, err = ReadConfig(topoConfigFilePath)
			if err != nil {
				csmlog.WithContext(ctx).Warnf("continuing with default topology keys")
			} else {
				csmlog.WithContext(ctx).Debug("processing topology config map")
				s.ParseConfig()
			}
		}
	}
	opts := Opts{}
	if ep, ok := csictx.LookupEnv(ctx, EnvDriverName); ok {
		opts.DriverName = ep
	}

	// read ownership isolation fields for VolumeJournal CRD
	if instanceUID, ok := csictx.LookupEnv(ctx, EnvDriverInstanceUID); ok {
		s.instanceUID = instanceUID
	}
	s.driverName = opts.DriverName

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
			csmlog.WithContext(ctx).Errorf("Secret mandated, but secret file not found %s.", err)
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
			csmlog.WithContext(ctx).Info("No management servers found.")
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
		csmlog.WithContext(ctx).Error("No managed arrays specified")
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
			csmlog.WithContext(ctx).Warnf("error while parsing env variable '%s', %s, defaulting to 0", EnvMaxVolumesPerNode, err)
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

	// Load podmon API token for authenticating requests to node podmon API endpoints
	if podmonAPIToken, ok := csictx.LookupEnv(ctx, EnvPodmonAPIToken); ok && strings.TrimSpace(podmonAPIToken) != "" {
		opts.PodmonAPIToken = strings.TrimSpace(podmonAPIToken)
		PodmonAPIToken = opts.PodmonAPIToken // keep package-level var in sync for HTTP middleware
	} else if opts.IsPodmonEnabled {
		csmlog.Warnf("%s is not set; podmon API endpoints will not require authentication", EnvPodmonAPIToken)
	}

	if tlsCertDir, ok := csictx.LookupEnv(ctx, EnvTLSCertDirName); ok {
		opts.TLSCertDir = tlsCertDir
	}

	// Load shared auth token if configured
	if tokenFile, ok := csictx.LookupEnv(ctx, EnvProxyAuthTokenFile); ok {
		tokenBytes, err := os.ReadFile(filepath.Clean(tokenFile))
		if err != nil {
			csmlog.Warnf("Proxy auth token file %s not found or not readable, continuing without auth token: %v", tokenFile, err)
			// Continue deployment without auth token - don't fail
		} else {
			token := strings.TrimSpace(string(tokenBytes))
			if token == "" {
				csmlog.Warnf("Proxy auth token file %s is empty, continuing without auth token", tokenFile)
				// Continue deployment without auth token - don't fail
			} else {
				s.authToken = token
				csmlog.Info("Proxy auth token loaded successfully")
			}
		}
	}

	opts.TransportProtocol = s.getTransportProtocolFromEnv()
	opts.ProxyServiceHost, opts.ProxyServicePort, opts.UseProxy = s.getProxySettingsFromEnv()
	if !opts.UseProxy && !inducedMockReverseProxy {
		err := fmt.Errorf("CSI reverseproxy service host or port not found, CSI reverseproxy not installed properly")
		csmlog.WithContext(ctx).Error(err.Error())
		return err
	}
	opts.GrpcMaxThreads = 4
	if maxThreads, ok := csictx.LookupEnv(ctx, EnvGrpcMaxThreads); ok {
		maxIntThreads, err := strconv.Atoi(maxThreads)
		if err == nil {
			csmlog.WithContext(ctx).Debug(fmt.Sprintf("setting GrpcMaxThreads to %d", maxIntThreads))
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
			csmlog.WithContext(ctx).Errorf("Invalid Cluster Prefix specified, exceeds maximum length of %d characters", MaxClusterPrefixLength)
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
				csmlog.WithContext(ctx).Debugf("invalid boolean value (%s) for %s. defaulting to false", v, n)
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
	opts.IsCSIAddonsReplicationEnabled = pb(EnvCSIAddonsReplicationEnabled)
	opts.IsTopologyControlEnabled = pb(EnvTopologyFilterEnabled)
	opts.IsPodmonEnabled = pb(EnvPodmonEnabled)
	opts.IsMetroSiteFailureHandlingEnabled = pb(EnvMetroSiteFailureHandlingEnabled)
	opts.MetroStateCheckTimeout = 15 * time.Second // default per FR-1.1
	if timeoutStr, ok := csictx.LookupEnv(ctx, EnvMetroStateCheckTimeout); ok && timeoutStr != "" {
		if secs, err := strconv.Atoi(timeoutStr); err == nil && secs >= 5 && secs <= 120 {
			opts.MetroStateCheckTimeout = time.Duration(secs) * time.Second
		} else {
			csmlog.Warnf("Invalid %s value %q; expected 5-120 seconds; using default 15s", EnvMetroStateCheckTimeout, timeoutStr)
		}
	}
	// Parse Metro queue and reconciliation tuning parameters.
	if warnStr, ok := csictx.LookupEnv(ctx, EnvMetroQueueWarningThreshold); ok && warnStr != "" {
		if v, err := strconv.Atoi(warnStr); err == nil && v > 0 {
			opts.MetroQueueWarningThreshold = v
		} else {
			csmlog.Warnf("Invalid %s value %q; using default", EnvMetroQueueWarningThreshold, warnStr)
		}
	}
	if limitStr, ok := csictx.LookupEnv(ctx, EnvMetroQueueHardLimit); ok && limitStr != "" {
		if v, err := strconv.Atoi(limitStr); err == nil && v > 0 {
			opts.MetroQueueHardLimit = v
		} else {
			csmlog.Warnf("Invalid %s value %q; using default", EnvMetroQueueHardLimit, limitStr)
		}
	}
	if backoffStr, ok := csictx.LookupEnv(ctx, EnvMetroReconciliationBackoff); ok && backoffStr != "" {
		if secs, err := strconv.Atoi(backoffStr); err == nil && secs > 0 {
			opts.MetroReconciliationBackoff = time.Duration(secs) * time.Second
		} else {
			csmlog.Warnf("Invalid %s value %q; using default 5s", EnvMetroReconciliationBackoff, backoffStr)
		}
	} else {
		opts.MetroReconciliationBackoff = 5 * time.Second // default per spec
	}
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
		csmlog.WithContext(ctx).Infof("Node will have access to the following arrays: %v", s.opts.ManagedArrays)
		if len(s.opts.ManagedArrays) < len(s.opts.StorageArrays) {
			csmlog.WithContext(ctx).Infof("Non-uniform Metro mode detected: node manages %d of %d configured arrays",
				len(s.opts.ManagedArrays), len(s.opts.StorageArrays))
		}
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

	// Host management mode configuration. Must be resolved before nodeStartup so
	// that adoption runs on the first node initialization pass.
	hostMgmtMode, _ := csictx.LookupEnv(ctx, EnvHostManagementMode)
	s.opts.HostManagementMode, err = resolveHostManagementMode(hostMgmtMode)
	if err != nil {
		return err
	}
	csmlog.WithContext(ctx).Infof("Host management mode: %s", s.opts.HostManagementMode)

	// NVMe/TCP connectivity mode. Must be resolved before nodeStartup so that the
	// node service knows whether it may establish fabric sessions.
	nvmeTCPConnMode, _ := csictx.LookupEnv(ctx, EnvNVMeTCPConnMode)
	s.opts.NVMeTCPConnMode, err = resolveNVMeTCPConnMode(nvmeTCPConnMode)
	if err != nil {
		return err
	}
	csmlog.WithContext(ctx).Infof("NVMe/TCP connectivity mode: %s", s.opts.NVMeTCPConnMode)

	// Host adoption minimum overlap ratio configuration.
	overlapRatio, _ := csictx.LookupEnv(ctx, EnvHostAdoptionMinOverlapRatio)
	s.opts.HostAdoptionMinOverlapRatio, err = resolveHostAdoptionMinOverlapRatio(overlapRatio)
	if err != nil {
		return err
	}
	csmlog.WithContext(ctx).Infof("Host adoption minimum overlap ratio: %.2f", s.opts.HostAdoptionMinOverlapRatio)

	// In adopt mode the advertised node ID is the full node name, which produces
	// longer derived object names than the short name used in create mode.
	if s.opts.HostManagementMode == HostMgmtModeAdopt && s.opts.NodeName != "" {
		if err := s.validateDerivedObjectNameLength(); err != nil {
			return err
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

	if dynamicSGEnabled, ok := csictx.LookupEnv(ctx, EnvDynamicSGEnabled); ok && dynamicSGEnabled == "true" {
		s.opts.dynamicSGEnabled = true
	}
	csmlog.WithContext(ctx).Infof("Dynamic SG enabled: %v", s.opts.dynamicSGEnabled)

	// Initialize Metro journal only in the controller plugin.
	// The node plugin does not defer operations and has no RBAC for
	// VolumeJournal CRDs — calling initMetroJournal on the node would
	// produce spurious 403-Forbidden warnings on every pod startup.
	if s.opts.IsMetroSiteFailureHandlingEnabled && s.isController() {
		s.initMetroJournal(ctx)
	}

	if metricsEnabled() && s.metricsServer == nil {
		s.metricsRegistry = DriverMetricsRegistry()
		arrayID := DefaultMetricsArrayID()
		reg := DriverMetricsRegistry()

		if s.opts.IsMetroSiteFailureHandlingEnabled {
			RegisterMetroMetrics(reg)
		}

		// Create a cancellable context for metrics goroutines
		metricsCtx, metricsCancel := context.WithCancel(ctx)
		s.metricsCtxCancel = metricsCancel

		// Note: Operation metrics are registered by the interceptor in provider.go
		// No need to register them here - the interceptor handles registration

		// Get Kubernetes metrics client
		var metricsClient metricsv.Interface
		if s.k8sUtils != nil {
			metricsClient = s.k8sUtils.GetMetricsClient()
		}
		// Get runtime config for metrics collection
		runtimeConfig := collectors.GetRuntimeConfig()

		// Validate TLS configuration using the shared library helper before starting any goroutines
		certFile, keyFile := metricsTLSFiles()
		if _, err := csmserver.TLSConfig(certFile, keyFile); err != nil {
			csmlog.Errorf("invalid TLS configuration for metrics server: %v", err)
			return err
		}

		// Register the per-array metrics on the same registry and seed the
		// multi-array-zone adoption gauge from the current configuration.
		// Per-array capacity/availability gauges are refreshed by the
		// capacity poller (controller only). Initialize this BEFORE starting
		// the capacity poller to avoid a data race on s.multiArrayMetrics.
		s.multiArrayMetrics = newMultiArrayMetrics(reg)
		s.multiArrayMetrics.setMultiArrayZones(countMultiArrayZones(s.opts.StorageArrays))

		s.healthCollector = collectors.NewDriverHealthCollector(reg, arrayID, s.k8sUtils, metricsClient, s.adminClient)
		s.healthCollectorWg.Add(1)
		go func() {
			defer s.healthCollectorWg.Done()
			ticker := time.NewTicker(runtimeConfig.Interval)
			defer ticker.Stop()
			_ = s.healthCollector.Collect(metricsCtx)
			for {
				select {
				case <-metricsCtx.Done():
					return
				case <-ticker.C:
					_ = s.healthCollector.Collect(metricsCtx)
				}
			}
		}()
		// Note: API call metrics are now collected by the reverse proxy observer
		// No client instrumentation needed in the driver
		if s.isController() && s.adminClient != nil && s.collectorManager == nil {
			s.collectorManager = collectors.NewCollectorManager(runtimeConfig.Interval)

			// Configure MetricsRuntime for the manager
			s.collectorManager.SetRuntimeConfig(runtimeConfig)

			// Wire stale metric callback from collector manager to metrics server
			// This enables dynamic updates to dell_powermax_metrics_stale gauge when
			// circuit breaker opens or metrics collection fails
			s.collectorManager.SetStaleSetter(func(arrayID string, stale bool) {
				if s.metricsServer != nil {
					s.metricsServer.SetStale([]string{arrayID}, stale)
				}
			})

			// Create K8sVolumeValidator for PV validation
			var volumeValidator collectors.VolumeValidator
			if s.k8sUtils != nil {
				k8sClient := s.k8sUtils.GetClient()
				if k8sClient != nil {
					volumeValidator = collectors.NewK8sVolumeValidator(k8sClient, Name)
					csmlog.Infof("Created K8sVolumeValidator for PV validation")
				} else {
					csmlog.Warnf("K8sVolumeValidator: kubernetes client not available, skipping PV validation")
				}
			}

			for _, aid := range s.opts.ManagedArrays {
				// Get MetricsRuntime for this array from the manager
				runtime := s.collectorManager.GetRuntime(aid)

				volAdapter := collectors.NewPmaxVolumeAdapterWithRuntime(s.adminClient, aid, s.opts.ClusterPrefix, volumeValidator, runtime)
				mvAdapter := &collectors.PmaxMVAdapter{Client: s.adminClient, ArrayID: aid, ClusterPrefix: s.opts.ClusterPrefix, Validator: volumeValidator}
				sgAdapter := collectors.NewPmaxSGAdapterWithRuntime(s.adminClient, aid, s.opts.ClusterPrefix, volumeValidator, runtime)
				volumeCollector, err := collectors.NewVolumeCollector(volAdapter, reg, aid, volumeValidator)
				if err != nil {
					csmlog.Errorf("Failed to create volume collector: %v", err)
				}
				s.collectorManager.Register(
					aid,
					collectors.NewStorageGroupCollector(sgAdapter, reg, aid),
					collectors.NewSRPCollector(&collectors.PmaxSRPAdapter{Client: s.adminClient, ArrayID: aid}, reg, aid),
					collectors.NewMaskingViewCollector(mvAdapter, reg, aid),
					volumeCollector,
				)
			}
		}
		certFile, keyFile = metricsTLSFiles()

		cfg := csmserver.Config{
			Port:            ":" + strconv.Itoa(metricsPort()),
			Registry:        s.metricsRegistry,
			StaleMetricName: "dell_powermax_metrics_stale",
			StaleLabels:     []string{"array_id"},
			CertFile:        certFile,
			KeyFile:         keyFile,
		}

		s.metricsServer = csmserver.NewMetricsServer(cfg)

		// Start collector goroutines only after s.metricsServer is initialized,
		// since the stale setter closure captures and reads s.metricsServer.
		if s.collectorManager != nil {
			s.collectorManager.Start(metricsCtx)
		}

		// Initialize stale metric for all arrays to 0 (fresh data)
		if s.arrayTransportProtocolMap != nil {
			for arrayID := range s.arrayTransportProtocolMap {
				s.metricsServer.SetStale([]string{arrayID}, false)
			}
		}

		proto := "HTTP"
		if csmserver.IsTLSEnabled(certFile, keyFile) {
			proto = "HTTPS"
		}
		csmlog.Infof("Starting %s metrics server on %s", proto, metricsPort())

		s.metricsWg.Add(1)
		go func() {
			defer s.metricsWg.Done()
			if err := s.metricsServer.Start(metricsCtx); err != nil {
				csmlog.Warnf("Metrics server on %s stopped: %v", metricsPort(), err)
			}
		}()
	}

	// Start the multi-array zone capacity poller. Only the controller
	// service selects arrays for provisioning, so only the controller needs
	// live capacity/availability data. Safe to start with zero or one
	// configured array; selectArray treats a single-array zone as the
	// degenerate case of multi-array selection. Started AFTER metrics
	// initialization to ensure s.multiArrayMetrics is set before the
	// poller goroutine reads it.
	if s.isController() && len(s.opts.StorageArrays) > 0 {
		arrayIDs := make([]string, 0, len(s.opts.StorageArrays))
		for arrayID := range s.opts.StorageArrays {
			arrayIDs = append(arrayIDs, arrayID)
		}
		s.capacityPollerCancel = s.startCapacityPoller(ctx, arrayIDs)
	}

	if s.opts.dynamicSGEnabled && s.isController() {
		SgVolLimitStr, ok := csictx.LookupEnv(ctx, EnvSGVolumeLimit)
		if ok {
			sgVolLimit, err := strconv.Atoi(SgVolLimitStr)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("unable to parse %s as duration: %s", EnvSGVolumeLimit, err.Error())
			} else {
				sgVolumeLimit = sgVolLimit
			}
		}
		csmlog.WithContext(ctx).Infof("%s set to %v", EnvSGVolumeLimit, sgVolumeLimit)
	}

	// File system check configuration
	if fsCheckEnabled, ok := csictx.LookupEnv(ctx, EnvFsCheckEnabled); ok {
		b, err := strconv.ParseBool(fsCheckEnabled)
		if err != nil {
			csmlog.WithContext(ctx).Warnf("Invalid value %q for %s, defaulting to false", fsCheckEnabled, EnvFsCheckEnabled)
			s.opts.FsCheckEnabled = false
		} else {
			s.opts.FsCheckEnabled = b
		}
	}
	csmlog.WithContext(ctx).Infof("FS check enabled: %v", s.opts.FsCheckEnabled)

	s.opts.FsCheckMode = "checkOnly" // default
	if fsCheckMode, ok := csictx.LookupEnv(ctx, EnvFsCheckMode); ok {
		switch fsCheckMode {
		case "checkOnly", "checkAndRepair":
			s.opts.FsCheckMode = fsCheckMode
		default:
			csmlog.WithContext(ctx).Warnf("Invalid value %q for %s, defaulting to %q", fsCheckMode, EnvFsCheckMode, "checkOnly")
		}
	}
	csmlog.WithContext(ctx).Infof("FS check mode: %s", s.opts.FsCheckMode)

	// Initialize space reclamation for node mode
	if s.isNode() && s.k8sUtils != nil {
		initSpaceReclamation(ctx, s, s.k8sUtils.GetClient())
		csmlog.WithContext(ctx).Infof("Space reclamation initialized")
	}

	return nil
}

// ParseConfig will make respective allowed and denied list as per the topology config
func (s *service) ParseConfig() {
	// make allowed list
	s.allowedTopologyKeys = readNodesRules(s.topologyConfig.AllowedConnections)
	// make denied list
	s.deniedTopologyKeys = readNodesRules(s.topologyConfig.DeniedConnections)

	csmlog.Infof("proccessed allowed list: (%+v)", s.allowedTopologyKeys)
	csmlog.Infof("proccessed denied list: (%+v)", s.deniedTopologyKeys)
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
				csmlog.Warnf("incorrect config for %s skipping rule (%s)", nodeName, rule)
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
		csmlog.Errorf("unable to read topology config file: %s", err.Error())
		return nil, err
	}
	var config TopologyConfig
	err = topoViper.Unmarshal(&config)
	if err != nil {
		csmlog.Errorf("unable to unmarshal topology config: %s", err.Error())
		return nil, err
	}
	return &config, nil
}

func (s *service) RegisterAdditionalServers(server *grpc.Server) {
	csiext.RegisterReplicationServer(server, s)
	migrext.RegisterMigrationServer(server, s)
	podmon.RegisterPodmonServer(server, s)

	// CSI-Addons servers are gated on X_CSI_CSIADDONS_REPLICATION_ENABLED
	// so existing deployments are completely unaffected by default.
	if s.opts.IsCSIAddonsReplicationEnabled {
		RegisterCSIAddonsIdentityServer(server, NewCSIAddonsIdentityServer(s))
		RegisterCSIAddonsReplicationServer(server, NewCSIAddonsReplicationServer(s))
		RegisterCSIAddonsVolumeGroupServer(server, NewCSIAddonsVolumeGroupServer(s))
		csmlog.Info("CSI-Addons identity, replication and volume group servers registered")
	}
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
					csmlog.Warn("Either ServiceHost and ServicePort is set to empty")
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
		csmlog.Errorf("Invalid transport protocol: %s, valid values AUTO, FC, ISCSI or NVMETCP", tp)
		return ""
	}
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

	var requestObserver api.RequestObserver
	if metricsEnabled() {
		reg := DriverMetricsRegistry()
		// Create per-array observers
		observer, err := drivermetrics.NewMultiplexingObserver(reg, s.opts.ManagedArrays)
		if err != nil {
			csmlog.WithContext(ctx).Warnf("Failed to initialize PowerMax API observer: %v", err)
		} else {
			requestObserver = observer
			csmlog.WithContext(ctx).Infof("Created per-array observers for %d arrays", len(s.opts.ManagedArrays))
		}
	}

	// Create our PowerMax API client, if needed
	if s.adminClient == nil {
		applicationName := ApplicationName + "/" + "v" + ManifestSemver
		tlsCertFile := filepath.Join(s.opts.TLSCertDir, defaultCertFile)
		c, err := pmax.NewClientWithArgs(endPoint, applicationName, s.opts.Insecure, !s.opts.DisableCerts, tlsCertFile)
		if err != nil {
			return status.Errorf(codes.FailedPrecondition,
				"unable to create PowerMax client: %s", err.Error())
		}
		if requestObserver != nil {
			c.SetRequestObserver(requestObserver)
		}
		// Set the proxy auth token header if configured, so all requests to the
		// reverse proxy include the shared auth token for authentication
		if s.authToken != "" {
			headers := make(http.Header)
			headers.Set(ProxyAuthTokenHeader, s.authToken)
			c.SetCustomHTTPHeaders(headers)
			csmlog.Info("Proxy auth token header set on PowerMax client")
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

			csmlog.WithContext(ctx).Infof("Error authenticating : %s", err)
			time.Sleep(10 * time.Second)
		}
		if err != nil {
			s.adminClient = nil
			return status.Errorf(codes.FailedPrecondition,
				"unable to login to Unisphere: %s", err.Error())
		}

		// Create a separate 10.4-specific client for CreateVolume/PublishVolume operations
		c104, err := pmax.NewClientWithArgs(endPoint, applicationName, s.opts.Insecure, !s.opts.DisableCerts, tlsCertFile)
		if err != nil {
			csmlog.WithContext(ctx).Warnf("unable to create 10.4 PowerMax client: %s, will use main client", err.Error())
			s.adminClient104 = s.adminClient
		} else {
			// Set the proxy auth token header on the 10.4 client as well
			if s.authToken != "" {
				headers := make(http.Header)
				headers.Set(ProxyAuthTokenHeader, s.authToken)
				c104.SetCustomHTTPHeaders(headers)
			}
			for i := 0; i < maxAuthenticateRetryCount; i++ {
				err = c104.Authenticate(ctx, &pmax.ConfigConnect{
					Endpoint: endPoint,
					Username: s.opts.User,
					Password: s.opts.Password,
					Version:  "104",
				})
				if err == nil {
					break
				}
				csmlog.WithContext(ctx).Infof("Error authenticating 10.4 client: %s", err)
				time.Sleep(10 * time.Second)
			}
			if err != nil {
				csmlog.WithContext(ctx).Warnf("unable to authenticate 10.4 client: %s, will use main client", err.Error())
				s.adminClient104 = s.adminClient
			} else {
				s.adminClient104 = c104
				csmlog.WithContext(ctx).Infof("10.4 client created and authenticated successfully")
			}
		}

		// Filter out a list of locally connected list of arrays, and
		// initialize the PowerMax client for those array only
		managedArrays := make([]string, 0, len(s.opts.ManagedArrays))
		csmlog.WithContext(ctx).Infof("Managed arrays - %v", s.opts.ManagedArrays)
		for _, array := range s.opts.ManagedArrays {
			symmetrix, err := s.adminClient.GetSymmetrixByID(ctx, array)
			if err != nil {
				csmlog.WithContext(ctx).Errorf("Failed to fetch details for array: %s. Reason: [%s]", array, err.Error())
			} else {
				if symmetrix.Local {
					managedArrays = append(managedArrays, array)
				}
			}
		}
		if len(managedArrays) == 0 {
			csmlog.WithContext(ctx).Error("None of the managed arrays specified are locally connected")
			os.Exit(1)
		}
		s.opts.ManagedArrays = managedArrays
		err = symmetrix.Initialize(s.opts.ManagedArrays, s.adminClient)
		if err != nil {
			return err
		}
		if s.opts.IsMetroSiteFailureHandlingEnabled {
			symmetrix.InstallMetroHealthWatchers(s.opts.ManagedArrays, s.adminClient)
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

func (s *service) buildCSIVolume(vol *types.VolumeEnhanced) *csi.Volume {
	vi := &csi.Volume{
		VolumeId:      vol.ID,
		CapacityBytes: int64(vol.CapCyl) * cylinderSizeInBytes,
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

// resolveHostManagementMode validates the configured host management mode against the
// allowed values, defaulting to "create" when unset so existing deployments keep their
// current behaviour (FR-4.1).
func resolveHostManagementMode(value string) (string, error) {
	if value == "" {
		return HostMgmtModeDefault, nil
	}
	switch mode := strings.ToLower(value); mode {
	case HostMgmtModeCreate, HostMgmtModeAdopt:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid hostManagementMode %q for %s; valid values are %q and %q",
			value, EnvHostManagementMode, HostMgmtModeCreate, HostMgmtModeAdopt)
	}
}

// resolveHostAdoptionMinOverlapRatio validates the configured minimum WWPN overlap
// ratio, defaulting to 0.5 when unset (FR-4.2).
func resolveHostAdoptionMinOverlapRatio(value string) (float64, error) {
	if value == "" {
		return DefaultHostAdoptionMinOverlapRatio, nil
	}
	ratio, err := strconv.ParseFloat(value, 64)
	if err != nil || ratio < 0.0 || ratio > 1.0 {
		return 0, fmt.Errorf("invalid value %q for %s: must be between 0.0 and 1.0",
			value, EnvHostAdoptionMinOverlapRatio)
	}
	return ratio, nil
}

// csiNodeID returns the node identifier the driver advertises through NodeGetInfo.
// In "adopt" mode the controller must resolve the Kubernetes Node object from this
// value to read the adopted-host topology labels, so the full node name is used.
// The choice is derived from static configuration rather than from runtime adoption
// results, so the node ID stays stable across restarts and partial adoption
// failures. Every node-side host/storage-group/masking-view derivation must use
// this helper so that the names the node creates match the names the controller
// derives for the same node.
func (s *service) csiNodeID() string {
	if s.opts.HostManagementMode == HostMgmtModeAdopt && s.opts.NodeFullName != "" {
		return s.opts.NodeFullName
	}
	return s.opts.NodeName
}

// k8sNodeName returns the name of the Kubernetes Node object this driver instance
// runs on. Used when emitting Events against the Node.
func (s *service) k8sNodeName() string {
	if s.opts.NodeFullName != "" {
		return s.opts.NodeFullName
	}
	return s.opts.NodeName
}

// validateDerivedObjectNameLength verifies that the CSI object names derived from
// the advertised node ID fit within the PowerMax object name limit. In "adopt" mode
// the node ID is the full node name, which can be considerably longer than the short
// name used elsewhere, so a long FQDN would otherwise produce names the array
// silently rejects at publish time.
func (s *service) validateDerivedObjectNameLength() error {
	nodeID := strings.ReplaceAll(s.csiNodeID(), ".", "-")
	// The longest derived name is the NVMe/TCP storage group:
	// CsiNoSrpSGPrefix + clusterPrefix + "-" + nodeID + NVMETCPSuffix
	longest := len(CsiNoSrpSGPrefix) + len(s.getClusterPrefix()) + 1 + len(nodeID) + len(NVMETCPSuffix)
	if longest > MaxPowerMaxObjectNameLength {
		return fmt.Errorf("node name %q produces PowerMax object names of %d characters, exceeding the %d character limit; "+
			"shorten the node name or set %s to a shorter value",
			s.csiNodeID(), longest, MaxPowerMaxObjectNameLength, EnvNodeName)
	}
	return nil
}

func (s *service) isDynamicSGEnabled() bool {
	return s.opts.dynamicSGEnabled
}

func (s *service) isMetroSiteFailureHandlingEnabled() bool {
	return s.opts.IsMetroSiteFailureHandlingEnabled
}

// metroEventTypeWarning / metroEventTypeNormal are package-level aliases for
// the corev1 event type constants.  Defining them here avoids importing
// k8s.io/api/core/v1 in controller.go solely for a string constant, while
// keeping all emitMetroEvent call-sites consistent (no bare string literals).
const (
	metroEventTypeWarning = string(corev1.EventTypeWarning) // "Warning"
	metroEventTypeNormal  = string(corev1.EventTypeNormal)  // "Normal"
)

// initMetroEventRecorder creates a dedicated Kubernetes EventRecorder for
// Metro site-failure events with component "csi-powermax-controller".
// No-op when already initialised or when running outside a cluster.
// separate recorder so Metro events are not conflated with fscheck events.
func (s *service) initMetroEventRecorder() {
	s.metroEventRecorderOnce.Do(func() {
		if s.metroEventRecorder != nil {
			return
		}
		config, err := rest.InClusterConfig()
		if err != nil {
			csmlog.Warnf("Metro event recorder: in-cluster config unavailable (%v) — events will not be posted", err)
			return
		}
		clientset, err := kubernetes.NewForConfig(config)
		if err != nil {
			csmlog.Warnf("Metro event recorder: kubernetes client unavailable (%v) — events will not be posted", err)
			return
		}
		scheme := runtime.NewScheme()
		if addErr := corev1.AddToScheme(scheme); addErr != nil {
			csmlog.Warnf("Metro event recorder: scheme setup failed (%v) — events will not be posted", addErr)
			return
		}
		bc := record.NewBroadcaster()
		bc.StartRecordingToSink(&typedcorev1.EventSinkImpl{Interface: clientset.CoreV1().Events("")})
		s.metroEventRecorder = bc.NewRecorder(scheme, corev1.EventSource{Component: "csi-powermax-controller"})
		// store the broadcaster so Stop() can call Shutdown() to
		// drain its internal goroutines and prevent a goroutine leak.
		s.metroEventBroadcaster = bc
	})
}

// emitMetroEvent posts a Kubernetes event on the controller Pod.
// emitting on the Pod (namespace-scoped) makes Metro events
// visible via standard Kubernetes tooling without cluster-admin access:
//
//	kubectl get events -n <driver-namespace>
//	kubectl describe pod <controller-pod> -n <driver-namespace>
//
// The Pod name and driver namespace are injected by the Helm/Operator via the
// POD_NAME and X_CSI_DRIVER_NAMESPACE downward-API env vars. Falls back to a
// Node-scoped event when those vars are not set (unit tests, out-of-cluster).
// No-op when the recorder is unavailable.
func (s *service) emitMetroEvent(eventType, reason, messageFmt string, args ...interface{}) {
	s.initMetroEventRecorder()
	if s.metroEventRecorder == nil {
		return
	}

	podName := os.Getenv(EnvPodName)
	namespace := os.Getenv(EnvDriverNamespace)

	var ref *corev1.ObjectReference
	if podName != "" && namespace != "" {
		// emit on the controller Pod so events are namespace-scoped
		// and visible via `kubectl get events -n <namespace>`.
		ref = &corev1.ObjectReference{
			Kind:       "Pod",
			Name:       podName,
			Namespace:  namespace,
			APIVersion: "v1",
		}
	} else {
		// Fallback: emit on the Node (unit tests, out-of-cluster).
		ref = &corev1.ObjectReference{
			Kind:       "Node",
			Name:       s.opts.NodeName,
			APIVersion: "v1",
		}
	}
	s.metroEventRecorder.Eventf(ref, eventType, reason, messageFmt, args...)
}

// initMetroJournal configures the deferred-operation journal for Metro
// site-failure handling. Must only be called from the controller plugin
// (guarded by isController() in BeforeServe).
// It applies tunable thresholds, upgrades to a CRD-backed journal when a
// Kubernetes dynamic client is available, and syncs existing CRD entries.
func (s *service) initMetroJournal(ctx context.Context) {
	// Initialise the Metro event recorder
	s.initMetroEventRecorder()

	s.volumeJournal.SetThresholds(s.opts.MetroQueueWarningThreshold, s.opts.MetroQueueHardLimit)
	if s.opts.MetroReconciliationBackoff > 0 {
		s.volumeJournal.SetReconciliationBackoff(s.opts.MetroReconciliationBackoff)
	}
	if s.driverName == "" || s.instanceUID == "" {
		err := errors.New("PowerMax Metro journal ownership identity is incomplete")
		csmlog.WithContext(ctx).Errorf("Metro journal: %v — Metro site failure handling disabled", err)
		s.emitMetroEvent(metroEventTypeWarning, "MetroOwnershipFailure", "%v", err)
		s.volumeJournal = nil
		return
	}
	dynClient, dynErr := k8sutils.CreateDynamicClient(s.opts.KubeConfigPath)
	if dynErr != nil {
		csmlog.WithContext(ctx).Errorf("Metro journal: dynamic client unavailable (%v) — Metro site failure handling disabled in production", dynErr)
		s.emitMetroEvent(metroEventTypeWarning, "MetroDurabilityFailure",
			"VolumeJournal CRD initialization failed: %v. Metro site failure handling disabled in production. Check RBAC permissions and API server connectivity.",
			dynErr)
		// Mark feature as unavailable - deferOperation will reject operations
		s.volumeJournal = nil
		return
	}
	crdJournal := symmetrix.NewCRDVolumeJournalForOwner(dynClient, s.driverName, s.instanceUID)
	crdJournal.SetThresholds(s.opts.MetroQueueWarningThreshold, s.opts.MetroQueueHardLimit)
	if s.opts.MetroReconciliationBackoff > 0 {
		crdJournal.SetReconciliationBackoff(s.opts.MetroReconciliationBackoff)
	}
	// SyncFromCRD with retry on transient failures to ensure durability (AC-004).
	// use context-aware sleep so startup cancellation is respected.
	maxRetries := 3
	baseBackoff := 2 * time.Second
	var lastSyncErr error
	for i := 0; i < maxRetries; i++ {
		if syncErr := crdJournal.SyncFromCRD(ctx); syncErr != nil {
			lastSyncErr = syncErr
			if i < maxRetries-1 {
				backoff := baseBackoff * time.Duration(1<<uint(i))
				csmlog.WithContext(ctx).Warnf("Metro journal: CRD sync attempt %d/%d failed (retrying in %v): %v", i+1, maxRetries, backoff, syncErr)
				select {
				case <-time.After(backoff):
				case <-ctx.Done():
					csmlog.WithContext(ctx).Errorf("Metro journal: startup context cancelled during CRD sync — Metro site failure handling disabled in production")
					s.volumeJournal = nil
					return
				}
			}
		} else {
			s.volumeJournal = crdJournal
			csmlog.WithContext(ctx).Info("Metro journal: using CRD-backed VolumeJournal for persistence")
			return
		}
	}
	// All retries failed — disable Metro site failure handling in production (Issue 4 fix).
	csmlog.WithContext(ctx).Errorf("Metro journal: CRD sync failed after %d attempts — Metro site failure handling disabled in production: %v", maxRetries, lastSyncErr)
	s.emitMetroEvent(metroEventTypeWarning, "MetroDurabilityFailure",
		"VolumeJournal CRD sync failed after %d attempts: %v. Metro site failure handling disabled in production. Check RBAC permissions and API server connectivity.",
		maxRetries, lastSyncErr)
	s.volumeJournal = nil
}

// metroReconcileLeaseDuration is how long a reconciliation Lease is held.
// It must exceed the 10-minute reconciliation context timeout so an in-progress
// reconciliation is never evicted by a peer that detects an expired lease.
const metroReconcileLeaseDuration = 12 * time.Minute

// metroLeaseNameForArray returns the Kubernetes Lease name for a given arrayID.
// Names are sanitized to conform to RFC 1123 label rules.
func metroLeaseNameForArray(arrayID string) string {
	name := "csi-pmax-metro-reconcile-" + strings.ToLower(arrayID)
	// Kubernetes Lease names must be ≤ 253 chars, lowercase alphanumeric or '-'.
	var safe []byte
	for _, c := range []byte(name) {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
			safe = append(safe, c)
		} else {
			safe = append(safe, '-')
		}
	}
	if len(safe) > 253 {
		safe = safe[:253]
	}
	return string(safe)
}

// tryAcquireMetroReconcileLease attempts to acquire a Kubernetes Lease object
// for the given arrayID to prevent concurrent reconciliation across controller
// replicas. Returns (true, releaseFunc) when the lease is acquired,
// or (false, nil) when another pod holds an unexpired lease.
//
// The lease is namespace-scoped in the driver's own namespace so it is
// automatically garbage-collected when the controller pod is deleted.
// If the Kubernetes client is unavailable (unit tests, out-of-cluster),
// the function returns (true, no-op) so reconciliation still proceeds.
func (s *service) tryAcquireMetroReconcileLease(ctx context.Context, arrayID string) (bool, func()) {
	// Guard: k8sUtils may be nil in unit tests or when running out-of-cluster.
	if s.k8sUtils == nil {
		return true, func() {}
	}
	client := s.k8sUtils.GetClient()
	if client == nil {
		// No k8s client — allow reconciliation (single-process test scenario).
		return true, func() {}
	}

	namespace := os.Getenv(EnvDriverNamespace)
	if namespace == "" {
		namespace = "default"
	}
	podName := os.Getenv(EnvPodName)
	if podName == "" {
		podName = s.opts.NodeName
	}

	leaseName := metroLeaseNameForArray(arrayID)
	now := metav1.NewMicroTime(time.Now())
	durationSec := int32(metroReconcileLeaseDuration.Seconds())

	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity:       &podName,
			LeaseDurationSeconds: &durationSec,
			AcquireTime:          &now,
			RenewTime:            &now,
		},
	}

	// Attempt to create the Lease (succeeds when no peer holds it).
	_, createErr := client.CoordinationV1().Leases(namespace).Create(ctx, lease, metav1.CreateOptions{})
	if createErr == nil {
		// We own the lease; return a release function that deletes it.
		release := func() {
			delCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if err := client.CoordinationV1().Leases(namespace).Delete(delCtx, leaseName, metav1.DeleteOptions{}); err != nil {
				csmlog.Warnf("tryAcquireMetroReconcileLease: failed to release lease %s: %v", leaseName, err)
			}
		}
		return true, release
	}

	if !k8serrors.IsAlreadyExists(createErr) {
		// fail-closed when lease acquisition fails due to RBAC or API errors.
		// Do not proceed with reconciliation without distributed lock to prevent
		// multiple controller replicas from executing the same operations.
		csmlog.Errorf("tryAcquireMetroReconcileLease: create lease %s/%s failed (%v) — reconciliation blocked; verify coordination.k8s.io RBAC permissions", namespace, leaseName, createErr)
		// Emit a Kubernetes event so cluster operators notice the lock failure.
		s.emitMetroEvent(metroEventTypeWarning, "MetroLeaseError",
			"tryAcquireMetroReconcileLease: failed to create lease %s/%s: %v — reconciliation blocked; verify coordination.k8s.io RBAC permissions",
			namespace, leaseName, createErr)
		return false, nil
	}

	// Lease already exists — read it to check if it has expired.
	existing, getErr := client.CoordinationV1().Leases(namespace).Get(ctx, leaseName, metav1.GetOptions{})
	if getErr != nil {
		csmlog.Errorf("tryAcquireMetroReconcileLease: get lease %s/%s failed (%v) — reconciliation blocked", namespace, leaseName, getErr)
		// Emit a Kubernetes event so cluster operators can diagnose the API error.
		s.emitMetroEvent(metroEventTypeWarning, "MetroLeaseError",
			"tryAcquireMetroReconcileLease: failed to read lease %s/%s: %v — reconciliation blocked",
			namespace, leaseName, getErr)
		return false, nil
	}

	// If the lease holder is ourselves (pod restart), take it over.
	if existing.Spec.HolderIdentity != nil && *existing.Spec.HolderIdentity == podName {
		return true, func() {
			delCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = client.CoordinationV1().Leases(namespace).Delete(delCtx, leaseName, metav1.DeleteOptions{})
		}
	}

	// Check if the lease has expired (peer may have crashed).
	if existing.Spec.RenewTime != nil && existing.Spec.LeaseDurationSeconds != nil {
		expiresAt := existing.Spec.RenewTime.Add(time.Duration(*existing.Spec.LeaseDurationSeconds) * time.Second)
		if time.Now().Before(expiresAt) {
			holder := "<unknown>"
			if existing.Spec.HolderIdentity != nil {
				holder = *existing.Spec.HolderIdentity
			}
			csmlog.WithContext(ctx).Infof("Metro reconciliation for array %s already held by pod %s — skipping", arrayID, holder)
			return false, nil
		}
	}

	// Lease is expired — take it over by updating the holder identity.
	existing.Spec.HolderIdentity = &podName
	existing.Spec.LeaseDurationSeconds = &durationSec
	existing.Spec.AcquireTime = &now
	existing.Spec.RenewTime = &now
	if _, updateErr := client.CoordinationV1().Leases(namespace).Update(ctx, existing, metav1.UpdateOptions{}); updateErr != nil {
		csmlog.Warnf("tryAcquireMetroReconcileLease: takeover of expired lease %s/%s failed (%v); skipping reconciliation", namespace, leaseName, updateErr)
		return false, nil
	}
	release := func() {
		delCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = client.CoordinationV1().Leases(namespace).Delete(delCtx, leaseName, metav1.DeleteOptions{})
	}
	return true, release
}

// logMetroStateCheck runs CheckMetroState for site-state tracking and
// Witness/Bias-based winner detection.
//
// Uses a 30-second TTL cache; on a cache hit the winner is re-applied
// synchronously before the CSI handler proceeds.
//
// On a cache miss the check runs synchronously (blocking the CSI RPC for at
// most MetroStateCheckTimeout, default 15 s).  A singleflight group ensures
// that concurrent cache-miss calls for the same array pair share a single
// Unisphere round-trip rather than issuing N parallel requests.
//
// rdfGroupNo is the RDF group for this volume (from volume context). Pass ""
// when unavailable — CheckMetroState will resolve it via GetRDFGroupList.
//
// Returns an error when both arrays are unreachable or Device Bias R1 failure
// is detected (Issue 6 fix).
func (s *service) logMetroStateCheck(ctx context.Context, operation, localSymID, remoteSymID, localRDFGroupNo string, remoteRDFGroupNos ...string) error {
	// Guard: metroStateCache is initialised lazily; if nil (unit test or pre-init
	// call), skip the check entirely.
	if s.metroStateCache == nil {
		return nil
	}

	// if rdfGroupNo is empty, we cannot safely determine Metro state.
	// CheckMetroState calls GetRDFGroupByID("") which fails and — when only one
	// array responds — produces a false "local array unreachable" classification.
	// Skip the check here and rely on future calls where the volume context
	// carries the RDF group number (e.g. from CreateVolume or the volume spec).
	if localRDFGroupNo == "" {
		csmlog.WithContext(ctx).Debugf("[%s] logMetroStateCheck: skipping for %s/%s — RDF group number unavailable (non-Metro volume or context not yet populated)", operation, localSymID, remoteSymID)
		return nil
	}
	remoteRDFGroupNo := localRDFGroupNo
	if len(remoteRDFGroupNos) > 0 && remoteRDFGroupNos[0] != "" {
		remoteRDFGroupNo = remoteRDFGroupNos[0]
	}

	// Fast path: cached result.
	if cachedState, ok, cachedErr := s.metroStateCache.Get(localSymID, remoteSymID); ok {
		if cachedErr != nil {
			// errors are cached with a short MetroStateErrorCacheTTL (5 s)
			// via PutError. Do NOT immediately invalidate here — that would cause
			// every sequential CSI RPC to fire a fresh 15-second Unisphere check
			// when both arrays are down, creating a severe latency storm.
			// Instead, let the short TTL expire naturally; the next call after the
			// TTL will re-run the check and detect recovery.
			if errors.Is(cachedErr, symmetrix.ErrBothArraysUnreachable) {
				return status.Error(codes.Unavailable, "both local and remote arrays are unreachable")
			}
			if errors.Is(cachedErr, symmetrix.ErrDeviceBiasR1Failure) {
				return status.Error(codes.FailedPrecondition, "Device Bias R1-side failure: manual R1 restoration required")
			}
			return nil
		}
		// Re-apply winner from cache to keep routing current.
		if cachedState != nil && cachedState.WinnerSymID != "" {
			symmetrix.SetMetroWinner(localSymID, remoteSymID, cachedState.WinnerSymID)
		}
		return nil
	}

	// Cache miss: run the check synchronously so the winner is applied before
	// this function returns. singleflight deduplicates concurrent callers.
	sfKey := localSymID + ":" + remoteSymID
	var checkErr error
	s.metroStateCheckGroup.Do(sfKey, func() (interface{}, error) { //nolint:errcheck
		checkErr = s.doMetroStateCheck(ctx, operation, localSymID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo)
		return nil, checkErr
	})
	return checkErr
}

// doMetroStateCheck performs the actual CheckMetroState call, updates caches,
// emits events, and triggers reconciliation.
// correctly detects loser recovery and passes loser array ID to
// triggerReconciliation.
// triggerReconciliation is called with the loser (offline) array ID,
// which is the key used when deferring operations.
// returns error when both arrays are unreachable or Device Bias R1 failure.
func (s *service) doMetroStateCheck(ctx context.Context, operation, localSymID, remoteSymID, localRDFGroupNo string, remoteRDFGroupNos ...string) error {
	timeout := s.opts.MetroStateCheckTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	checkCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	localClient := symmetrix.GetArrayClient(localSymID)
	remoteClient := symmetrix.GetArrayClient(remoteSymID)
	remoteRDFGroupNo := localRDFGroupNo
	if len(remoteRDFGroupNos) > 0 && remoteRDFGroupNos[0] != "" {
		remoteRDFGroupNo = remoteRDFGroupNos[0]
	}
	state, err := symmetrix.CheckMetroStateWithRDFGroupsAndTimeout(checkCtx, localClient, remoteClient, localSymID, remoteSymID, localRDFGroupNo, remoteRDFGroupNo, timeout)
	if err != nil {
		csmlog.Warnf("[%s] Metro state check: %v", operation, err)
		// cache errors with the short MetroStateErrorCacheTTL so that
		// sequential CSI RPCs don't each trigger a fresh 15-second Unisphere call.
		s.metroStateCache.PutError(localSymID, remoteSymID, err)
		s.siteStateTracker.UpdateState(localSymID, symmetrix.SiteUnreachable)
		s.siteStateTracker.UpdateState(remoteSymID, symmetrix.SiteUnreachable)
		// emit Kubernetes event when both arrays are unreachable (FR-1.3).
		if errors.Is(err, symmetrix.ErrBothArraysUnreachable) {
			s.emitMetroEvent(metroEventTypeWarning, "MetroBothArraysUnreachable",
				"Metro state check for %s/%s: no winner can be determined — both arrays are unreachable. Error: %v",
				localSymID, remoteSymID, err)
			return status.Error(codes.Unavailable, "both local and remote arrays are unreachable")
		}
		if errors.Is(err, symmetrix.ErrDeviceBiasR1Failure) {
			s.emitMetroEvent(metroEventTypeWarning, "MetroDeviceBiasR1Failure",
				"Device Bias R1-side failure detected: manual R1 restoration required. "+
					"Consider migrating to a Witness configuration for improved automatic failover capability. "+
					"Array pair: %s/%s. Error: %v",
				localSymID, remoteSymID, err)
			return status.Error(codes.FailedPrecondition, "Device Bias R1-side failure: manual R1 restoration required")
		}
		return nil
	}

	// Cache the successful result.
	s.metroStateCache.Put(localSymID, remoteSymID, state, nil)

	fields := csmlog.Fields{
		"winner":           state.WinnerSymID,
		"loser":            state.LoserSymID,
		"witnessEffective": state.WitnessEffective,
		"biasConfigured":   state.BiasConfigured,
		"deviceBiasR1Fail": state.DeviceBiasR1Failure,
	}
	csmlog.WithFields(fields).Infof("[%s] Metro site-failure state check", operation)

	// Update the metroClient's winner designation so subsequent CSI
	// operations route to the surviving site (AC-002/AC-003).
	if state.WinnerSymID != "" {
		symmetrix.SetMetroWinner(localSymID, remoteSymID, state.WinnerSymID)
	}

	if state.DeviceBiasR1Failure {
		csmlog.Warnf("[%s] Device Bias R1-side failure detected; R1 array %s requires manual restoration. Consider configuring a Witness for automatic failover.", operation, state.LoserSymID)
		s.emitMetroEvent(metroEventTypeWarning, "MetroDeviceBiasR1Failure",
			"Device Bias R1-side failure on array %s: host access to Metro volumes is lost. Restore R1 array to recover. Recommendation: configure a Witness for automatic failover.",
			state.LoserSymID)
	}

	// Emit K8s Warning event for Metro site failure detection (AC-008).
	if state.LoserUnreachable {
		s.emitMetroEvent(metroEventTypeWarning, "MetroSiteFailure",
			"Metro site failure detected: winner=%s, loser=%s, witnessEffective=%v, biasConfigured=%v",
			state.WinnerSymID, state.LoserSymID, state.WitnessEffective, state.BiasConfigured)
	}

	// only increment the site-failure counter when a failure is
	// actually observed, not on every healthy check.
	if state.LoserUnreachable && MetroSiteFailures != nil {
		MetroSiteFailures.WithLabelValues(state.LoserSymID, "loser").Inc()
	}

	// Track site state and trigger reconciliation when the loser
	// transitions from Unreachable → Reachable (i.e., recovery detected).
	//
	// Algorithm:
	//   1. Mark winner as reachable (always).
	//   2. If the loser is currently unreachable, mark it unreachable.
	//   3. If the loser was previously unreachable and is now implicitly
	//      reachable (LoserUnreachable=false after being true), fire
	//      reconciliation for the loser's array ID.
	//
	// pass the loser's array ID — deferred ops are keyed by the
	// offline (loser) array, NOT the winner.
	s.siteStateTracker.UpdateState(state.WinnerSymID, symmetrix.SiteReachable)
	if state.LoserUnreachable {
		s.siteStateTracker.UpdateState(state.LoserSymID, symmetrix.SiteUnreachable)
	} else {
		// Loser is not currently marked unreachable by this check.
		// If it was previously unreachable, it just recovered.
		loserRecovered := s.siteStateTracker.UpdateState(state.LoserSymID, symmetrix.SiteReachable)
		if loserRecovered {
			loserID := state.LoserSymID
			go func() { // #nosec G118 -- reconciliation must outlive the CSI RPC
				reconcileCtx, rcancel := context.WithTimeout(context.Background(), 10*time.Minute)
				defer rcancel()
				s.triggerReconciliation(reconcileCtx, loserID)
			}()
		}
	}
	return nil
}

// reconcileDeviceCleanup deletes a volume on the recovered remote array.
// The VolumeID encodes both the local and remote devices; op.ArrayID is
// the recovered (previously offline) array.
func (s *service) reconcileDeviceCleanup(ctx context.Context, op symmetrix.DeferredOperation) error {
	volName, localSymID, localDevID, remoteSymID, remoteDevID, err := s.parseCsiID(op.VolumeID)
	if err != nil {
		return fmt.Errorf("reconcileDeviceCleanup: parseCsiID(%s): %w", op.VolumeID, err)
	}

	// Choose the correct device based on which array was recovered.
	targetSymID, targetDevID := localSymID, localDevID
	if op.ArrayID == remoteSymID && remoteDevID != "" {
		targetSymID, targetDevID = remoteSymID, remoteDevID
	}

	pmaxClient, err := s.GetPowerMaxClient(targetSymID)
	if err != nil {
		return fmt.Errorf("reconcileDeviceCleanup: GetPowerMaxClient(%s): %w", targetSymID, err)
	}
	csmlog.WithContext(ctx).Infof("Reconciliation: deleting device %s/%s for volume %s", targetSymID, targetDevID, op.VolumeID)
	err = s.deleteVolume(ctx, "reconcile", targetSymID, volName, targetDevID, op.VolumeID, pmaxClient)
	if err != nil {
		// treat all "not found" variants as success for idempotency.
		// deleteVolume handles the primary "Could not find" pattern from Unisphere,
		// but the device may also be absent with alternative error strings if it was
		// removed manually, by a prior partial reconciliation, or by the CO issuing
		// DeleteVolume independently. Rather than burning through all 10 retry
		// attempts, recognise the device-gone condition and succeed immediately.
		errStr := err.Error()
		if strings.Contains(errStr, notFound) || strings.Contains(errStr, errorNotFound) {
			csmlog.WithContext(ctx).Infof("reconcileDeviceCleanup: device %s/%s already deleted (idempotent success)", targetSymID, targetDevID)
			return nil
		}
	}
	return err
}

// reconcileMetroPairing re-establishes the SRDF Metro pairing for a volume
// whose link was severed during a site failure.
//
// the SG name is read from op.StorageGroupName (stored at deferral
// time) rather than rebuilt with a potentially wrong format (missing namespace).
//
// (FR-3.2): the current SRDF SG state is inspected before calling
// Establish, and after, to implement the Metro state machine:
//   - ActiveActive / ActiveBias → already healthy; no-op.
//   - SyncInProg → transitioning; return retriable error.
//   - Split / Mixed / Invalid / Unknown → unsafe; return ErrUnsafeRDFState
//     (causes hard failure + removal from journal).
//   - Suspended → attempt Establish, then verify ActiveActive.
func (s *service) reconcileMetroPairing(ctx context.Context, op symmetrix.DeferredOperation) error {
	_, localSymID, localDevID, remoteSymID, _, err := s.parseCsiID(op.VolumeID)
	if err != nil {
		return fmt.Errorf("reconcileMetroPairing: parseCsiID(%s): %w", op.VolumeID, err)
	}

	pmaxClient, err := s.GetPowerMaxClient(localSymID, remoteSymID)
	if err != nil {
		return fmt.Errorf("reconcileMetroPairing: GetPowerMaxClient(%s): %w", localSymID, err)
	}

	// use the exact SG name recorded at deferral time.  Fall back to
	// the no-namespace heuristic only when upgrading from an old journal entry
	// that did not store StorageGroupName.
	sgName := op.StorageGroupName
	if sgName == "" {
		if op.RDFGroupNo == "" {
			return fmt.Errorf("reconcileMetroPairing: both StorageGroupName and RDFGroupNo are empty for volume %s", op.VolumeID)
		}
		// Legacy fallback (journal entry predates StorageGroupName field).
		sgName = CsiRepSGPrefix + op.RDFGroupNo + "-" + Metro
		csmlog.WithContext(ctx).Warnf("reconcileMetroPairing: StorageGroupName not stored — using heuristic SG name %q for volume %s (may fail if namespace was used)", sgName, op.VolumeID)
	}

	// Check if this is a degraded volume (remote device doesn't exist)
	// by attempting to query the RDF pair state. If the SG doesn't have RDF info,
	// we need to create the remote device, remote SG, and establish the Metro relationship.
	sgRDF, stateErr := pmaxClient.GetStorageGroupRDFInfo(ctx, localSymID, sgName, op.RDFGroupNo)
	if stateErr != nil {
		// SG doesn't have RDF info (degraded volume) or array unreachable
		// Try to determine if this is a connectivity error vs. missing RDF config
		if isConnectivityError(stateErr) {
			return fmt.Errorf("reconcileMetroPairing: array unreachable, will retry: %w", stateErr)
		}
		// Likely a degraded volume - proceed with full Metro setup
		csmlog.WithContext(ctx).Infof("reconcileMetroPairing: no RDF info found for SG %s (degraded volume), proceeding with full Metro setup", sgName)
		return s.setupDegradedVolumeAsMetro(ctx, op, localSymID, localDevID, remoteSymID, sgName, pmaxClient)
	}

	var rdfState string
	if len(sgRDF.States) > 0 {
		rdfState = sgRDF.States[0]
	}

	switch rdfState {
	case ActiveActive, ActiveBias:
		// Already in a healthy Metro state — the pairing was re-established
		// by a previous attempt or never broke.  Remove from journal.
		csmlog.WithContext(ctx).Infof("reconcileMetroPairing: volume %s SG %s already in state %q — no action needed", op.VolumeID, sgName, rdfState)
		return nil

	case SyncInProgress:
		// Transition state — wait and retry via the normal backoff path.
		return fmt.Errorf("reconcileMetroPairing: SRDF SG %s is in transitional state %q — will retry", sgName, rdfState)

	case Split, Invalid, "Mixed", "Unknown", "":
		// Unsafe state (FR-3.2 / NFR-2): hard failure, do not retry.
		return &symmetrix.UnsafeRDFStateError{State: rdfState}

	default:
		// Suspended or other recoverable state — proceed with Establish.
	}

	csmlog.WithContext(ctx).Infof("reconcileMetroPairing: re-establishing Metro pairing for volume %s (SG=%s, RDFGroup=%s, currentState=%q)",
		op.VolumeID, sgName, op.RDFGroupNo, rdfState)
	if establishErr := s.Establish(ctx, localSymID, sgName, op.RDFGroupNo, false, pmaxClient); establishErr != nil {
		return fmt.Errorf("reconcileMetroPairing: Establish(%s): %w", sgName, establishErr)
	}

	// FR-3.1: verify the pair reached ActiveActive after Establish.
	verifyRDF, verifyErr := pmaxClient.GetStorageGroupRDFInfo(ctx, localSymID, sgName, op.RDFGroupNo)
	if verifyErr != nil {
		return fmt.Errorf("reconcileMetroPairing: post-Establish GetStorageGroupRDFInfo(%s): %w", sgName, verifyErr)
	}
	var postState string
	if len(verifyRDF.States) > 0 {
		postState = verifyRDF.States[0]
	}
	if postState != ActiveActive && postState != ActiveBias {
		return fmt.Errorf("reconcileMetroPairing: Establish completed but SG %s is in state %q (expected ActiveActive/ActiveBias) — will retry", sgName, postState)
	}
	csmlog.WithContext(ctx).Infof("reconcileMetroPairing: volume %s SG %s successfully re-established (state=%q)", op.VolumeID, sgName, postState)
	return nil
}

// setupDegradedVolumeAsMetro performs the complete Metro setup for a volume
// that was created in degraded mode (local only) while the remote array was offline.
// This creates the remote device, remote storage group, and establishes the SRDF Metro relationship.
func (s *service) setupDegradedVolumeAsMetro(ctx context.Context, op symmetrix.DeferredOperation, localSymID, localDevID, remoteSymID, sgName string, pmaxClient pmax.Pmax) error {
	csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: setting up Metro for degraded volume %s (local=%s, remote=%s)", op.VolumeID, localSymID, remoteSymID)

	// Step 1: Create the remote storage group if it doesn't exist
	remoteSGName := op.RemoteStorageGroupName
	if remoteSGName == "" {
		// Fallback to heuristic if not stored
		remoteSGName = CsiRepSGPrefix + op.RemoteRDFGroupNo + "-" + Metro
		csmlog.WithContext(ctx).Warnf("setupDegradedVolumeAsMetro: RemoteStorageGroupName not stored, using heuristic %q", remoteSGName)
	}

	createdRemoteSG := false
	createdRemoteVolume := false
	rollback := func() {
		if createdRemoteVolume {
			if err := pmaxClient.DeleteVolume(ctx, remoteSymID, localDevID); err != nil {
				csmlog.WithContext(ctx).Warnf("setupDegradedVolumeAsMetro: rollback failed to delete remote volume %s/%s: %v", remoteSymID, localDevID, err)
			}
		}
		if createdRemoteSG {
			if err := pmaxClient.DeleteStorageGroup(ctx, remoteSymID, remoteSGName); err != nil {
				csmlog.WithContext(ctx).Warnf("setupDegradedVolumeAsMetro: rollback failed to delete remote storage group %s/%s: %v", remoteSymID, remoteSGName, err)
			}
		}
	}
	fail := func(err error) error {
		rollback()
		return err
	}

	// Check if remote SG exists, create if not.
	_, sgErr := pmaxClient.GetStorageGroup(ctx, remoteSymID, remoteSGName)
	if sgErr != nil {
		csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: creating remote storage group %s on array %s", remoteSGName, remoteSymID)
		_, createErr := pmaxClient.CreateStorageGroup(ctx, remoteSymID, remoteSGName, op.RemoteSRP, op.RemoteServiceLevel, false, nil)
		if createErr != nil {
			return fmt.Errorf("setupDegradedVolumeAsMetro: failed to create remote storage group %s: %w", remoteSGName, createErr)
		}
		createdRemoteSG = true
	}

	// Step 2: Create the remote device (replica of local device).
	// The exact local cylinder count is required for safe recovery. Never
	// derive cylinders from the legacy capacity field because that field is
	// not expressed in a unit suitable for conversion here.
	if op.RequiredCylinders <= 0 {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: missing required cylinder count for volume %s", op.VolumeID))
	}
	requiredCylinders := op.RequiredCylinders

	// Create remote volume in the remote storage group. New journal records
	// carry the exact cylinder count and can safely probe for an existing
	// device before creating it, making recovery idempotent.
	remoteVolID := localDevID // Use same device ID for simplicity
	remoteVolumeExists := false
	if _, getErr := pmaxClient.GetVolumeByID(ctx, remoteSymID, remoteVolID); getErr == nil {
		remoteVolumeExists = true
	} else if !strings.Contains(strings.ToLower(getErr.Error()), "not found") {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: failed to check remote volume %s/%s: %w", remoteSymID, remoteVolID, getErr))
	}
	if !remoteVolumeExists {
		_, createVolErr := pmaxClient.CreateVolumeInStorageGroupS(ctx, remoteSymID, remoteSGName, remoteVolID, requiredCylinders, nil, nil)
		if createVolErr != nil {
			return fail(fmt.Errorf("setupDegradedVolumeAsMetro: failed to create remote volume %s in SG %s: %w", remoteVolID, remoteSGName, createVolErr))
		}
		createdRemoteVolume = true
	}

	// Step 3: Establish the RDF pair between local and remote volumes
	// Use the existing Establish function to create the SRDF Metro relationship
	// This is the standard API for establishing Metro pairing between arrays
	csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: establishing RDF pair between %s:%s and %s:%s", localSymID, localDevID, remoteSymID, remoteVolID)
	if establishErr := s.Establish(ctx, localSymID, sgName, op.RDFGroupNo, false, pmaxClient); establishErr != nil {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: failed to establish RDF pair: %w", establishErr))
	}

	// Step 4: Protect the local storage group with RDF info
	csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: protecting local SG %s with RDF info", sgName)
	if protectErr := s.ProtectStorageGroup(ctx, localSymID, remoteSymID, sgName, remoteSGName, "", op.RDFGroupNo, op.ReplicationMode, op.VolumeID, "", false, pmaxClient); protectErr != nil {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: failed to protect local storage group: %w", protectErr))
	}

	// Step 5: Establish the Metro relationship
	csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: establishing Metro relationship for SG %s", sgName)
	if establishErr := s.Establish(ctx, localSymID, sgName, op.RDFGroupNo, false, pmaxClient); establishErr != nil {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: failed to establish Metro relationship: %w", establishErr))
	}

	// Step 6: Verify the pair reached ActiveActive
	verifyRDF, verifyErr := pmaxClient.GetStorageGroupRDFInfo(ctx, localSymID, sgName, op.RDFGroupNo)
	if verifyErr != nil {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: post-setup GetStorageGroupRDFInfo failed: %w", verifyErr))
	}
	var postState string
	if len(verifyRDF.States) > 0 {
		postState = verifyRDF.States[0]
	}
	if postState != ActiveActive && postState != ActiveBias {
		return fail(fmt.Errorf("setupDegradedVolumeAsMetro: setup completed but SG %s is in state %q (expected ActiveActive/ActiveBias)", sgName, postState))
	}

	csmlog.WithContext(ctx).Infof("setupDegradedVolumeAsMetro: volume %s successfully upgraded to Metro protection (state=%q)", op.VolumeID, postState)
	return nil
}

// deferOperation is the single authoritative path for creating a deferred
// operation during Metro site failures. It:
//  1. Checks if the feature is available (volumeJournal not nil) - rejects if disabled (Issue 4 fix).
//  2. Checks the queue depth before creating, emitting a warning log when the
//     warning threshold is reached (and a Prometheus counter for alerting).
//  3. Calls VolumeJournal.CreateDeferredOperation, which enforces the hard
//     limit by returning ErrQueueFull when the queue is at capacity.
//  4. On success, increments MetroDeferredOpsTotal and updates
//     MetroDeferredOpsQueueDepth so Prometheus metrics stay accurate.
func (s *service) deferOperation(ctx context.Context, op symmetrix.DeferredOperation) (string, error) {
	// Reject operations if durable storage is unavailable
	if s.volumeJournal == nil {
		csmlog.WithContext(ctx).Errorf("deferOperation: Metro site failure handling disabled (CRD journal unavailable) — rejecting %s for volume %s", op.OperationType, op.VolumeID)
		return "", errors.New("Metro site failure handling disabled: CRD journal unavailable")
	}
	// Apply the journal owner at the single enqueue boundary so every
	// operation, including DeviceCleanup records, carries complete ownership.
	if op.DriverName == "" {
		op.DriverName = s.driverName
	}
	if op.InstanceUID == "" {
		op.InstanceUID = s.instanceUID
	}

	// Pre-check queue status to emit warning before the hard-limit is hit.
	qs := s.volumeJournal.GetQueueStatus()
	if qs.AtLimit {
		csmlog.WithContext(ctx).Errorf(
			"Metro deferred-op queue is full (%d ops, limit %d); rejecting new %s for volume %s",
			qs.Count, symmetrix.QueueHardLimit, op.OperationType, op.VolumeID,
		)
		s.emitMetroEvent(metroEventTypeWarning, "MetroQueueFull",
			"Metro deferred-op queue full (%d ops, limit %d); rejecting %s for volume %s",
			qs.Count, symmetrix.QueueHardLimit, op.OperationType, op.VolumeID)
		return "", symmetrix.ErrQueueFull
	}
	if qs.AtWarning {
		csmlog.WithContext(ctx).Warnf(
			"Metro deferred-op queue is nearing capacity (%d ops, oldest %s); "+
				"consider investigating site connectivity. Op: %s, Volume: %s",
			qs.Count, qs.OldestAge, op.OperationType, op.VolumeID,
		)
		s.emitMetroEvent(metroEventTypeWarning, "MetroQueueDepthWarning",
			"Metro deferred-op queue at 75%% threshold (%d ops, oldest %s); investigate site connectivity",
			qs.Count, qs.OldestAge)
		if MetroSiteFailures != nil {
			MetroSiteFailures.WithLabelValues("queue-warning", "threshold").Inc()
		}
	}

	token, err := s.volumeJournal.CreateDeferredOperation(ctx, op)
	if err != nil {
		return "", err
	}

	// Update Prometheus metrics after successful enqueue.
	if MetroDeferredOpsTotal != nil {
		MetroDeferredOpsTotal.WithLabelValues(string(op.OperationType)).Inc()
	}
	s.updateMetroQueueMetrics()
	return token, nil
}

// updateMetroQueueMetrics refreshes queue-depth and degraded-volume gauges
// from the current journal state (L-4 fix: MetroDegradedVolumes is derived
// from pending OpMetroPairing entries rather than tracked as a separate counter).
// Metrics are broken down by Metro array pair (local_array / remote_array labels)
// so that operators can identify which pair is experiencing the site failure in
// multi-Metro deployments (WARN-2 fix).
func (s *service) updateMetroQueueMetrics() {
	if MetroDeferredOpsQueueDepth == nil && MetroDegradedVolumes == nil {
		return
	}
	all := s.volumeJournal.GetAllOperations()

	// Group operations by the offline array ID.
	type pairCounts struct{ total, degraded int }
	byArray := make(map[string]*pairCounts)
	for _, op := range all {
		pc, ok := byArray[op.ArrayID]
		if !ok {
			pc = &pairCounts{}
			byArray[op.ArrayID] = pc
		}
		pc.total++
		if op.OperationType == symmetrix.OpMetroPairing {
			pc.degraded++
		}
	}

	// Emit per-pair gauge values. For each offline array look up its partner
	// from the registered Metro clients in the symmetrix package.
	for arrayID, pc := range byArray {
		partner := symmetrix.GetMetroPartner(arrayID)
		if MetroDeferredOpsQueueDepth != nil {
			MetroDeferredOpsQueueDepth.WithLabelValues(arrayID, partner).Set(float64(pc.total))
		}
		if MetroDegradedVolumes != nil {
			MetroDegradedVolumes.WithLabelValues(arrayID, partner).Set(float64(pc.degraded))
		}
	}
}

// triggerReconciliation replays deferred operations for a recovered array.
// a singleflight-style guard ensures only one reconciliation run
// is active per arrayID at a time within this pod; a concurrent trigger is a no-op.
// a Kubernetes Lease is acquired before reconciling so that peer
// controller replicas (default: 2) do not replay the same operations simultaneously,
// which would cause ordering violations and duplicate Unisphere API calls.
func (s *service) triggerReconciliation(ctx context.Context, arrayID string) {
	// Pod-local guard: prevent duplicate runs within this pod.
	if _, loaded := s.metroReconcileInFlight.LoadOrStore(arrayID, struct{}{}); loaded {
		csmlog.WithContext(ctx).Infof("Metro reconciliation for array %s already in flight — skipping duplicate trigger", arrayID)
		return
	}
	defer s.metroReconcileInFlight.Delete(arrayID)

	// acquire a cross-replica Kubernetes Lease to serialise
	// reconciliation across all controller pods for this array.
	acquired, releaseLease := s.tryAcquireMetroReconcileLease(ctx, arrayID)
	if !acquired {
		return
	}
	defer releaseLease()

	// Resolve the partner array for per-pair Prometheus labels (WARN-2/WARN-3).
	partnerArray := symmetrix.GetMetroPartner(arrayID)

	csmlog.WithContext(ctx).Infof("Metro reconciliation triggered for recovered array %s", arrayID)
	s.emitMetroEvent(metroEventTypeNormal, "MetroReconciliationStarted",
		"Metro reconciliation started for recovered array %s", arrayID)

	// Record wall-clock time for the NFR-3 SLO histogram (WARN-3).
	reconcileStart := time.Now()

	results := symmetrix.ReconcileDeferredOperations(ctx, s.volumeJournal, arrayID, func(ctx context.Context, op symmetrix.DeferredOperation) error {
		switch op.OperationType {
		case symmetrix.OpMetroPairing:
			return s.reconcileMetroPairing(ctx, op)
		case symmetrix.OpDeviceCleanup:
			return s.reconcileDeviceCleanup(ctx, op)
		default:
			return fmt.Errorf("unknown deferred operation type: %s", op.OperationType)
		}
	})
	succeeded, failed, unsafeCount, maxRetryCount := 0, 0, 0, 0
	for _, r := range results {
		switch {
		case r.Success:
			succeeded++
			if MetroReconciliationTotal != nil {
				MetroReconciliationTotal.WithLabelValues("success").Inc()
			}
		case r.UnsafeState:
			// hard-failure for unsafe SRDF state — emit dedicated event.
			unsafeCount++
			s.emitMetroEvent(metroEventTypeWarning, "MetroUnsafeRDFState",
				"Reconciliation for volume (token=%s) on array %s aborted: %v. Manual intervention required.",
				r.Token, arrayID, r.Error)
			if MetroReconciliationTotal != nil {
				MetroReconciliationTotal.WithLabelValues("unsafe_state").Inc()
			}
		case r.MaxRetries:
			maxRetryCount++
			// emit actionable event for max-retry exhaustion.
			s.emitMetroEvent(metroEventTypeWarning, "MetroReconciliationMaxRetries",
				"Reconciliation for token %s on array %s exhausted max retries (%d); manual intervention may be required",
				r.Token, arrayID, symmetrix.MaxReconciliationRetries)
			if MetroReconciliationTotal != nil {
				MetroReconciliationTotal.WithLabelValues("max_retries").Inc()
			}
		default:
			failed++
			if MetroReconciliationTotal != nil {
				MetroReconciliationTotal.WithLabelValues("failure").Inc()
			}
		}
	}

	// Observe reconciliation duration for NFR-3 SLO verification (WARN-3).
	// result label matches the dominant outcome of the run.
	if MetroReconcileDuration != nil {
		reconcileResult := "success"
		if failed+unsafeCount+maxRetryCount > 0 {
			reconcileResult = "failure"
		}
		MetroReconcileDuration.WithLabelValues(arrayID, partnerArray, reconcileResult).Observe(time.Since(reconcileStart).Seconds())
	}

	// Update queue-depth and degraded-volumes metrics after reconciliation (L-4).
	s.updateMetroQueueMetrics()
	csmlog.WithContext(ctx).Infof("Metro reconciliation for array %s: %d succeeded, %d failed, %d unsafe-state, %d max-retries out of %d total",
		arrayID, succeeded, failed, unsafeCount, maxRetryCount, len(results))

	// Emit Kubernetes events for reconciliation outcomes (AC-008/AC-009).
	total := failed + unsafeCount + maxRetryCount
	if total > 0 {
		s.emitMetroEvent(metroEventTypeWarning, "MetroReconciliationFailed",
			"Metro reconciliation for array %s: %d succeeded, %d failed, %d unsafe-state, %d max-retries out of %d total",
			arrayID, succeeded, failed, unsafeCount, maxRetryCount, len(results))
	} else if succeeded > 0 {
		// Enhanced reconciliation completion event to confirm
		// return to ActiveActive/ActiveBias state and absence of remaining deferred operations
		qs := s.volumeJournal.GetQueueStatusForArray(arrayID)
		s.emitMetroEvent(metroEventTypeNormal, "MetroReconciliationCompleted",
			"Metro reconciliation for array %s completed successfully: %d operations reconciled. "+
				"Remaining deferred operations: %d. Metro state returned to ActiveActive/ActiveBias.",
			arrayID, succeeded, qs.Count)
	}
}

func (s *service) getReplicationPrefix() string {
	return s.opts.ReplicationPrefix
}

func (s *service) getReplicationContextPrefix() string {
	return s.opts.ReplicationContextPrefix
}

func (s *service) isSnapshotLicensed(ctx context.Context, symID string, pmaxClient pmax.Pmax) error {
	return s.IsSnapshotLicensed(ctx, symID, pmaxClient)
}

func (s *service) getDynamicSG(ctx context.Context, arrayID, baseSGName string) (string, bool, error) {
	return getDynamicSG(ctx, arrayID, baseSGName, s)
}

func (s *service) getStorageArrayLabels(arrayID string) map[string]string {
	if array, ok := s.opts.StorageArrays[arrayID]; ok {
		labels := make(map[string]string)
		for k, v := range array.Labels {
			labels[k] = v.(string)
		}
		return labels
	}
	return nil
}

func (s *service) isBlockEnabled() bool {
	return s.opts.EnableBlock
}

func setLogFields(ctx context.Context, fields csmlog.Fields) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, contextKey(logFields), fields)
}

func getLogFields(ctx context.Context) csmlog.Fields {
	fields, ok := ctx.Value(contextKey(logFields)).(csmlog.Fields)
	if !ok {
		fields = csmlog.Fields{}
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
			csmlog.WithContext(ctx).Debugf("use pollingFrequency as %d seconds", pollingFrequency)
			s.pollingFrequencyInSeconds = pollingFrequency
			return s.pollingFrequencyInSeconds
		}
	}
	csmlog.WithContext(ctx).Debugf("use default pollingFrequency as %d seconds", DefaultPodmonPollRate)
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
	csmlog.WithContext(ctx).Info("---------Inside setArrayConfigEnvs function----------")
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
		csmlog.WithContext(ctx).Errorf("unable to read array config file: %s", err.Error())
		setLogFormatAndLevel("json", csmlog.InfoLevel)
	}
	portgroups := paramsViper.GetString(PortGroups)
	if portgroups != "" {
		csmlog.WithContext(ctx).Info("Read PortGroups from config file: " + portgroups)
		_ = os.Setenv(PortGroups, portgroups)
	}
	protocol := paramsViper.GetString(Protocol)
	if protocol != "" {
		csmlog.WithContext(ctx).Info("Read protocol from config file: " + protocol)
		_ = os.Setenv(Protocol, protocol)
	}
	endpoint := paramsViper.GetString(EnvEndpoint)
	if endpoint != "" {
		// Clean up endpoint by removing spaces or trailing slash
		endpoint = strings.TrimSpace(endpoint)
		if strings.HasSuffix(endpoint, "/") {
			endpoint = strings.TrimRight(endpoint, "/")
		}
		csmlog.WithContext(ctx).Info("Read endpoint from config file: " + endpoint)
		_ = os.Setenv(EnvEndpoint, endpoint)
	}
	managedArrays := paramsViper.GetString(ManagedArrays)
	if managedArrays != "" {
		csmlog.WithContext(ctx).Info("Managed arrays from config file: " + managedArrays)
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
			csmlog.WithContext(ctx).Errorf("Secret mandated, but secret file not found %s", err)
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
		csmlog.Warnf("failed to get node labels: '%s'", err.Error())
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
			} else {
				csmlog.Warnf("Skipping unreachable array %s: zone labels do not match node labels for node %s", arrayID, s.opts.NodeFullName)
			}
		} else {
			unzonedArrays = append(unzonedArrays, arrayID)
		}
	}

	if len(zonedArrays) >= 1 {
		return zonedArrays
	}

	if len(unzonedArrays) == 0 && len(storageArrays) > 0 {
		csmlog.Errorf("No arrays are reachable for node %s: all %d configured arrays have zone labels but none match node labels",
			s.opts.NodeFullName, len(storageArrays))
	}

	return unzonedArrays
}

// Stop stops all background goroutines for metrics collection
func (s *service) Stop() {
	s.metricsShutdownMutex.Lock()
	defer s.metricsShutdownMutex.Unlock()

	// Stop the multi-array zone capacity poller, if running.
	if s.capacityPollerCancel != nil {
		csmlog.Info("Stopping capacity poller...")
		s.capacityPollerCancel()
		s.capacityPollerCancel = nil
	}

	// Cancel the metrics context to signal all goroutines to stop
	if s.metricsCtxCancel != nil {
		csmlog.Info("Stopping metrics collection...")
		s.metricsCtxCancel()
	}

	// Wait for health collector goroutine to exit
	s.healthCollectorWg.Wait()
	csmlog.Debug("Health collector stopped")

	// Stop all array collectors and wait for them to exit
	if s.collectorManager != nil {
		s.collectorManager.StopAll()
		csmlog.Debug("Collector manager stopped")
	}

	// Wait for metrics server goroutine to exit
	s.metricsWg.Wait()
	csmlog.Debug("Metrics server stopped")

	// shut down the Metro event broadcaster to drain its goroutines.
	// The broadcaster is initialised lazily; if it was never used this is a no-op.
	if s.metroEventBroadcaster != nil {
		csmlog.Info("Shutting down Metro event broadcaster...")
		s.metroEventBroadcaster.Shutdown()
		s.metroEventBroadcaster = nil
	}

	// Clean up references
	if s.metricsCtxCancel != nil {
		s.metricsCtxCancel = nil
	}
	if s.metricsServer != nil {
		s.metricsServer = nil
	}
	if s.collectorManager != nil {
		s.collectorManager = nil
	}
	csmlog.Info("Metrics collection stopped successfully")
}
