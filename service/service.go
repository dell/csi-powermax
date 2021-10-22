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

package service

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/fsnotify/fsnotify"
	"github.com/spf13/viper"

	"github.com/dell/csi-powermax/v2/pkg/symmetrix"

	"google.golang.org/grpc"

	"sync"
	"time"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/dell/gocsi"
	csictx "github.com/dell/gocsi/context"
	"github.com/dell/goiscsi"
	"github.com/dell/gopowermax/types/v90"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/dell/csi-powermax/v2/core"
	csiext "github.com/dell/dell-csi-extensions/replication"
	pmax "github.com/dell/gopowermax"
)

// Constants for the service
const (
	Name                       = "csi-powermax.dellemc.com"         // Name is the name of the CSI plug-in.
	ApplicationName            = "CSI Driver for Dell EMC PowerMax" // ApplicationName is the name used to register with Powermax REST APIs
	defaultPrivDir             = "/dev/disk/csi-powermax"
	defaultPmaxTimeout         = 120
	defaultLockCleanupDuration = 4
	defaultU4PVersion          = "91" // defaultU4PVersion should be reset to base supported endpoint version for the CSI driver release
	csiPrefix                  = "csi-"
	logFields                  = "logFields"
	maxAuthenticateRetryCount  = 4
	CSILogLevelParam           = "CSI_LOG_LEVEL"
	CSILogFormatParam          = "CSI_LOG_FORMAT"
)

type contextKey string // specific string type used for context keys

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
	IsReverseProxyEnabled      bool
	ReplicationContextPrefix   string // Enables sidecars to read required information from volume context
	ReplicationPrefix          string // Used as a prefix to find out if replication is enabled
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
	// replace this with Unisphere system if needed
	system            *interface{}
	privDir           string
	loggedInArrays    map[string]bool
	mutex             sync.Mutex
	cacheMutex        sync.Mutex
	nodeProbeMutex    sync.Mutex
	nodeIsInitialized bool
	// Timeout for storage pool cache
	storagePoolCacheDuration time.Duration
	// only used for testing, indicates if the deletion worked finished populating queue
	waitGroup sync.WaitGroup

	// Gobrick stuff
	fcConnector               fcConnector
	iscsiConnector            iSCSIConnector
	dBusConn                  dBusConn
	arrayTransportProtocolMap map[string]string // map of array SN to IscsiTransportProtocol or FcTransportProtocol

	sgSvc *storageGroupSvc
}

// New returns a new Service.
func New() Service {
	svc := &service{
		loggedInArrays: map[string]bool{},
	}
	svc.sgSvc = newStorageGroupService(svc)
	svc.pmaxTimeoutSeconds = defaultPmaxTimeout
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

func (s *service) BeforeServe(
	ctx context.Context, sp *gocsi.StoragePlugin, lis net.Listener) error {

	defer func() {
		fields := map[string]interface{}{
			"endpoint":                s.opts.Endpoint,
			"useProxy":                s.opts.UseProxy,
			"ProxyServiceHost":        s.opts.ProxyServiceHost,
			"ProxyServicePort":        s.opts.ProxyServicePort,
			"user":                    s.opts.User,
			"password":                "",
			"systemname":              s.opts.SystemName,
			"nodename":                s.opts.NodeName,
			"insecure":                s.opts.Insecure,
			"thickprovision":          s.opts.Thick,
			"privatedir":              s.privDir,
			"autoprobe":               s.opts.AutoProbe,
			"enableblock":             s.opts.EnableBlock,
			"enablechap":              s.opts.EnableCHAP,
			"portgroups":              s.opts.PortGroups,
			"clusterprefix":           s.opts.ClusterPrefix,
			"transport":               s.opts.TransportProtocol,
			"mode":                    s.mode,
			"drivername":              s.opts.DriverName,
			"iscsichapuser":           s.opts.CHAPUserName,
			"iscsichappassword":       "",
			"nodenametemplate":        s.opts.NodeNameTemplate,
			"modifyHostName":          s.opts.ModifyHostName,
			"replicationContextPreix": s.opts.ReplicationContextPrefix,
			"replicationPrefix":       s.opts.ReplicationPrefix,
		}

		if s.opts.Password != "" {
			fields["password"] = "******"
		}
		if s.opts.CHAPPassword != "" {
			fields["iscsichappassword"] = "******"
		}

		log.WithFields(fields).Infof("configured %s", s.getDriverName())
	}()

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

	opts := Opts{}
	if ep, ok := csictx.LookupEnv(ctx, EnvDriverName); ok {
		opts.DriverName = ep
	}
	if ep, ok := csictx.LookupEnv(ctx, EnvEndpoint); ok {
		opts.Endpoint = ep
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
	}
	if portgroups, ok := csictx.LookupEnv(ctx, EnvPortGroups); ok {
		tempList, err := s.parseCommaSeperatedList(portgroups)
		if err != nil {
			return fmt.Errorf("Invalid value for %s", EnvPortGroups)
		}
		opts.PortGroups = tempList
	}

	if arrays, ok := csictx.LookupEnv(ctx, EnvManagedArrays); ok {
		opts.ManagedArrays, _ = s.parseCommaSeperatedList(arrays)
	} else {
		log.Error("No managed arrays specified")
		os.Exit(1)
	}

	if replicationContextPrefix, ok := csictx.LookupEnv(ctx, EnvReplicationContextPrefix); ok {
		opts.ReplicationContextPrefix = replicationContextPrefix
	}
	if replicationPrefix, ok := csictx.LookupEnv(ctx, EnvReplicationPrefix); ok {
		opts.ReplicationPrefix = replicationPrefix
	}

	opts.TransportProtocol = s.getTransportProtocolFromEnv()
	opts.ProxyServiceHost, opts.ProxyServicePort, opts.UseProxy = s.getProxySettingsFromEnv()
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
	if !opts.UseProxy {
		// If proxy is not set, then check if the env indicating that
		// reverseproxy is enabled is set
		opts.IsReverseProxyEnabled = pb(EnvProxyEnabled)
	}
	s.opts = opts

	// setup the iscsi client
	iscsiOpts := make(map[string]string, 0)
	if chroot, ok := csictx.LookupEnv(ctx, EnvISCSIChroot); ok {
		iscsiOpts[goiscsi.ChrootDirectory] = chroot
	}
	s.iscsiClient = goiscsi.NewLinuxISCSI(iscsiOpts)

	// seed the random methods
	rand.Seed(time.Now().Unix())

	if _, ok := csictx.LookupEnv(ctx, "X_CSI_POWERMAX_NO_PROBE_ON_START"); !ok {
		// Do a controller probe
		if !strings.EqualFold(s.mode, "node") {
			if err := s.controllerProbe(ctx); err != nil {
				return err
			}
		}

		// Do a node probe
		if !strings.EqualFold(s.mode, "controller") {
			if err := s.nodeProbe(ctx); err != nil {
				return err
			}
		}
	}

	// If this is a node, run the node startup logic
	if !strings.EqualFold(s.mode, "controller") {
		if err := s.nodeStartup(ctx); err != nil {
			return err
		}
	}

	// Start the deletion worker thread
	log.Printf("s.mode: %s\n", s.mode)
	if !strings.EqualFold(s.mode, "node") {
		/*symIDs, err := s.adminClient.GetSymmetrixIDList()
		if err != nil {
			return err
		}
		if len(symIDs.SymmetrixIDs) == 0 {
			errMsg := "no arrays connected to the unisphere"
			log.Println(errMsg)
			return fmt.Errorf("%s", errMsg)
		}*/
		s.NewDeletionWorker(s.opts.ClusterPrefix, s.opts.ManagedArrays)
	}

	// Start the snapshot housekeeping worker thread
	if !strings.EqualFold(s.mode, "node") {
		s.startSnapCleanupWorker() // #nosec G20
		if snapCleaner == nil {
			snapCleaner = new(snapCleanupWorker)
		}
	}

	return nil
}

func (s *service) RegisterAdditionalServers(server *grpc.Server) {
	csiext.RegisterReplicationServer(server, s)
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
			// Change it to uppercase
			proxyServiceName = strings.ToUpper(proxyServiceName)
			// Change all "-" to underscores
			proxyServiceName = strings.Replace(proxyServiceName, "-", "_", -1)
			serviceHostEnv := fmt.Sprintf("%s_SERVICE_HOST", proxyServiceName)
			servicePortEnv := fmt.Sprintf("%s_SERVICE_PORT", proxyServiceName)
			if sh, ok := csictx.LookupEnv(context.Background(), serviceHostEnv); ok {
				serviceHost = sh
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
	}
	return "", "", false
}

func (s *service) getTransportProtocolFromEnv() string {
	transportProtocol := ""
	if tp, ok := csictx.LookupEnv(context.Background(), EnvPreferredTransportProtocol); ok {
		tp = strings.ToUpper(tp)
		switch tp {
		case "FIBRE":
			tp = "FC"
			break
		case "FC":
			break
		case "ISCSI":
			break
		case "":
			break
		default:
			log.Errorf("Invalid transport protocol: %s, valid values FC or ISCSI", tp)
			return ""
		}
		transportProtocol = tp
	}
	return transportProtocol
}

// get the amount of time to retry pmax calls
func (s *service) GetPmaxTimeoutSeconds() int64 {
	return s.pmaxTimeoutSeconds
}

// set the maximum amount of time to retry pmax calls
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
		c, err := pmax.NewClientWithArgs(
			endPoint, defaultU4PVersion, applicationName, s.opts.Insecure, !s.opts.DisableCerts)
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
				Version:  defaultU4PVersion,
			})
			if err == nil {
				break
			}
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
		for _, array := range s.opts.ManagedArrays {
			symmetrix, err := s.adminClient.GetSymmetrixByID(ctx, array)
			if err != nil {
				log.Errorf("Failed to fetch details for array: %s. [%s]", array, err.Error())
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
	if ctx == nil {
		return log.Fields{}
	}
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
