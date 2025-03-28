/*
 Copyright © 2021 - 2024 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package proxy

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/cache"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/config"
	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/utils"

	log "github.com/sirupsen/logrus"

	types "github.com/dell/gopowermax/v2/types/v100"
	"github.com/gorilla/mux"
)

const clientSymID = "proxyClientSymID"

// Proxy - represents a  Proxy
type Proxy struct {
	config        config.ProxyConfig
	requestID     uint64
	envoyMap      map[string]common.Envoy
	iteratorCache cache.Cache
	mutex         sync.Mutex
}

// NewProxy - Given a proxy config, returns a proxy
func NewProxy(proxyConfig config.ProxyConfig) (*Proxy, error) {
	envoyMap := make(map[string]common.Envoy)
	for arrayID, serverArray := range proxyConfig.GetManagedArraysAndServers() {
		envoy := newEnvoyClient(serverArray)
		envoyMap[arrayID] = envoy
	}
	return &Proxy{
		config:        proxyConfig,
		envoyMap:      envoyMap,
		iteratorCache: cache.New("volume-iterators", 5*time.Minute),
	}, nil
}

func newEnvoyClient(serverArray config.StorageArrayServer) common.Envoy {
	primaryProxy := newReverseProxy(serverArray.PrimaryServer)
	envoy := common.NewEnvoy(&primaryProxy)
	envoy.SetPrimaryHTTPClient(newHTTPClient(serverArray.PrimaryServer))
	if serverArray.BackupServer != nil {
		backupProxy := newReverseProxy(*serverArray.BackupServer)
		backupHTTPClient := newHTTPClient(*serverArray.BackupServer)
		envoy.SetBackup(&backupProxy)
		envoy.SetBackupHTTPClient(backupHTTPClient)
	}
	return envoy
}

func newHTTPClient(mgmtServer config.ManagementServer) *http.Client {
	tlsConfig := newTLSConfig(mgmtServer)
	tr := &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
	}
	client := &http.Client{Transport: tr}
	return client
}

func newReverseProxy(mgmtServer config.ManagementServer) common.Proxy {
	revProxy := httputil.NewSingleHostReverseProxy(&mgmtServer.Endpoint)
	tlsConfig := newTLSConfig(mgmtServer)
	revProxy.Transport = &http.Transport{
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 50,
	}
	proxy := common.Proxy{
		ReverseProxy: revProxy,
		URL:          mgmtServer.Endpoint,
		Limits:       mgmtServer.Limits,
	}
	return proxy
}

func newTLSConfig(mgmtServer config.ManagementServer) *tls.Config {
	tlsConfig := tls.Config{
		InsecureSkipVerify: mgmtServer.SkipCertificateValidation, // #nosec G402 InsecureSkipVerify cannot be false always as expected by gosec, this needs to be configurable
	}
	if !mgmtServer.SkipCertificateValidation {
		caCert, err := os.ReadFile(mgmtServer.CertFile)
		if err != nil {
			log.Fatal(err)
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}
	return &tlsConfig
}

func (revProxy *Proxy) setEnvoy(arrayID string, envoy common.Envoy) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	revProxy.envoyMap[arrayID] = envoy
}

func (revProxy *Proxy) removeEnvoy(arrayID string) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	delete(revProxy.envoyMap, arrayID)
}

func (revProxy *Proxy) getHTTPClient(symID string) (*http.Client, error) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if envoy, ok := revProxy.envoyMap[symID]; ok {
		return envoy.GetActiveHTTPClient(), nil
	}
	return nil, fmt.Errorf("no http client found for the given symid")
}

func (revProxy *Proxy) getProxyByURL(symmURL common.SymmURL) (common.Proxy, error) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	proxy := common.Proxy{}
	if envoy, ok := revProxy.envoyMap[symmURL.SymmetrixID]; ok {
		if envoy.GetPrimary().URL == symmURL.URL {
			return *envoy.GetPrimary(), nil
		} else if envoy.GetBackup().URL == symmURL.URL {
			return *envoy.GetBackup(), nil
		}
	}
	return proxy, fmt.Errorf("no proxy found for this URL")
}

func (revProxy *Proxy) getProxyBySymmID(storageArrayID string) (common.Proxy, error) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	proxy := common.Proxy{}
	storageArrays := revProxy.config.GetStorageArray(storageArrayID)
	if len(storageArrays) != 0 {
		if envoy, ok := revProxy.envoyMap[storageArrayID]; ok {
			return *(envoy.GetActiveProxy()), nil
		}
		return proxy, fmt.Errorf("failed to find reverseproxy for the array id")
	}
	return proxy, fmt.Errorf("failed to find array id in the configuration")
}

func (revProxy *Proxy) getAuthorisedArrays(res http.ResponseWriter, req *http.Request) ([]string, error) {
	username, password, ok := req.BasicAuth()
	if !ok {
		utils.WriteHTTPError(res, "no authorization provided", utils.StatusUnAuthorized)
		return nil, fmt.Errorf("no authorization provided")
	}
	symIDs := revProxy.config.GetAuthorizedArrays(username, password)
	if len(symIDs) == 0 {
		utils.WriteHTTPError(res, "No managed arrays under this user", utils.StatusUnAuthorized)
		return nil, fmt.Errorf("no managed arrays under this user")
	}
	log.Printf("Authorized arrays - %s\n", symIDs)
	return symIDs, nil
}

func (revProxy *Proxy) getRequestID() string {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	requestID := fmt.Sprintf("%d", atomic.AddUint64(&revProxy.requestID, 1))
	return requestID
}

func (revProxy *Proxy) getIteratorByID(iterID string) (common.SymmURL, error) {
	u4p := common.SymmURL{}
	if u4p, ok := revProxy.iteratorCache.Get(iterID); ok {
		return u4p.(common.SymmURL), nil
	}
	return u4p, fmt.Errorf("no symm info found for this iterator")
}

func (revProxy *Proxy) setIteratorID(resp *http.Response, URL url.URL, symID string) (*types.VolumeIterator, error) {
	volumeIterator := &types.VolumeIterator{}
	decoder := json.NewDecoder(resp.Body)
	if err := decoder.Decode(volumeIterator); err != nil {
		return nil, err
	}
	revProxy.iteratorCache.Set(volumeIterator.ID, common.SymmURL{
		SymmetrixID: symID,
		URL:         URL,
	})
	log.Debugf("Added Iterator (%s)", volumeIterator.ID)
	return volumeIterator, nil
}

func (revProxy *Proxy) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := revProxy.getRequestID()
		r.URL.Path = strings.TrimSuffix(r.URL.Path, "/")
		r.Header.Set("RequestID", reqID)
		logMsg := fmt.Sprintf("Request ID: %s - %s %s", reqID, r.Method, r.URL)
		log.Info(logMsg)
		next.ServeHTTP(w, r)
	})
}

func (revProxy *Proxy) getResponseIfAuthorised(res http.ResponseWriter, req *http.Request, symID string) (*http.Response, error) {
	proxy, err := revProxy.getProxyBySymmID(symID)
	if err != nil {
		http.Error(res, err.Error(), 500)
		return nil, err
	}
	client, err := revProxy.getHTTPClient(symID)
	if err != nil {
		http.Error(res, err.Error(), 500)
		return nil, err
	}
	path := proxy.URL.String() + req.URL.Path
	if req.URL.RawQuery != "" {
		path = path + "?" + req.URL.RawQuery
	}
	req, err = http.NewRequest(req.Method, path, req.Body)
	if err != nil {
		http.Error(res, err.Error(), 500)
		return nil, err
	}
	revProxy.modifyHTTPRequest(res, req, proxy.URL)
	lockID := proxy.URL.String()
	requestID := req.Header.Get("RequestID")
	restCall := fmt.Sprintf("%s %s\n", req.Method, req.URL)
	lockType := utils.GetRequestType(req)
	defer utils.Elapsed(requestID, restCall)()
	lock := utils.Lock{
		ID:             lockID,
		RequestID:      requestID,
		LockType:       lockType,
		MaxOutStanding: utils.GetMaxOutStanding(lockType, proxy.Limits),
		MaxActive:      utils.GetMaxActive(lockType, proxy.Limits),
	}
	err = lock.Lock()
	if err != nil {
		log.Error("server busy")
		utils.WriteHTTPError(res, "server busy", utils.StatusProxyBusy)
		return nil, err
	}
	defer lock.Release()
	return client.Do(req)
}

func (revProxy *Proxy) modifyHTTPRequest(res http.ResponseWriter, req *http.Request, targetURL url.URL) {
	req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	creds, err := revProxy.config.GetManagementServerCredentials(targetURL)
	if err != nil {
		utils.WriteHTTPError(res, err.Error(), utils.StatusUnAuthorized)
	}
	req.Header.Set("Authorization", utils.BasicAuth(creds))
	req.Host = targetURL.Host
}

func (revProxy *Proxy) updateEnvoy(arrayID, proxyType string, server config.ManagementServer) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if envoy, ok := revProxy.envoyMap[arrayID]; ok {
		if proxyType == "PRIMARY" {
			proxy := newReverseProxy(server)
			envoy.SetPrimary(&proxy)
			envoy.SetPrimaryHTTPClient(newHTTPClient(server))
		} else if proxyType == "BACKUP" {
			proxy := newReverseProxy(server)
			envoy.SetBackup(&proxy)
			envoy.SetBackupHTTPClient(newHTTPClient(server))
		}
	}
}

func (revProxy *Proxy) setBackupEnvoy(arrayID string, server config.ManagementServer) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if envoy, ok := revProxy.envoyMap[arrayID]; ok {
		proxy := newReverseProxy(server)
		envoy.SetBackup(&proxy)
		envoy.SetBackupHTTPClient(newHTTPClient(server))
	}
}

func (revProxy *Proxy) removeBackupEnvoy(arrayID string) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if envoy, ok := revProxy.envoyMap[arrayID]; ok {
		envoy.RemoveBackupProxy()
		envoy.RemoveBackupHTTPClient()
	}
}

func (revProxy *Proxy) hasServerChanged(oldServer, newServer config.ManagementServer) bool {
	return oldServer.Endpoint != newServer.Endpoint ||
		oldServer.CertFile != newServer.CertFile ||
		oldServer.CredentialSecret != newServer.CredentialSecret ||
		oldServer.Limits != newServer.Limits ||
		oldServer.SkipCertificateValidation != newServer.SkipCertificateValidation ||
		oldServer.Username != newServer.Username ||
		oldServer.Password != newServer.Password
}

// UpdateConfig - Given a new proxy config, updates the Proxy
func (revProxy *Proxy) UpdateConfig(proxyConfig config.ProxyConfig) error {
	if reflect.DeepEqual(revProxy.config, proxyConfig) {
		log.Info("No changes detected in the configuration")
		return nil
	}

	log.Info("Updating proxy config since changes detected in the configuration")
	oldServerArrayMap := revProxy.config.GetManagedArraysAndServers()
	serverArrayMap := proxyConfig.GetManagedArraysAndServers()

	// Update/Add envoy clients
	for arrayID, serverArray := range serverArrayMap {
		if oldServerArray, ok := oldServerArrayMap[arrayID]; ok {
			if revProxy.hasServerChanged(oldServerArray.PrimaryServer, serverArray.PrimaryServer) {
				revProxy.updateEnvoy(arrayID, "PRIMARY", serverArray.PrimaryServer)
			}
			if serverArray.BackupServer == nil && oldServerArray.BackupServer != nil {
				revProxy.removeBackupEnvoy(arrayID)
			} else if serverArray.BackupServer != nil && oldServerArray.BackupServer == nil {
				revProxy.setBackupEnvoy(arrayID, *serverArray.BackupServer)
			} else if serverArray.BackupServer != nil && oldServerArray.BackupServer != nil && revProxy.hasServerChanged(*oldServerArray.BackupServer, *serverArray.BackupServer) {
				revProxy.updateEnvoy(arrayID, "BACKUP", *serverArray.BackupServer)
			}
		} else {
			envoy := newEnvoyClient(serverArray)
			revProxy.setEnvoy(arrayID, envoy)
		}
	}

	// Delete envoy clients
	for arrayID := range oldServerArrayMap {
		if _, ok := serverArrayMap[arrayID]; !ok {
			revProxy.removeEnvoy(arrayID)
		}
	}

	revProxy.updateConfig(proxyConfig)
	if reflect.DeepEqual(revProxy.config, proxyConfig) {
		log.Info("Changes applied successfully")
	}
	return nil
}

func (revProxy *Proxy) updateConfig(proxyConfig config.ProxyConfig) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	revProxy.config = proxyConfig
}

// GetRouter - setups the http handlers and returns a http handler
func (revProxy *Proxy) GetRouter() http.Handler {
	router := mux.NewRouter()
	router.Use(revProxy.symIDMiddleware)
	router.Path(utils.Prefix + "/{version}/sloprovisioning/symmetrix/{symid}/volume").HandlerFunc(revProxy.ServeVolume)
	router.PathPrefix(utils.Prefix + "/{version}/sloprovisioning/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.Prefix + "/{version}/system/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.Prefix + "/{version}/sloprovisioning/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.PrivatePrefix + "/{version}/replication/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.PrivatePrefix + "/{version}/sloprovisioning/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.Prefix + "/{version}/replication/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)

	// endpoints without symmetrix id
	router.HandleFunc(utils.Prefix+"/{version}/sloprovisioning/symmetrix", revProxy.ifNoSymIDInvoke(revProxy.ServeSymmetrix))
	router.HandleFunc(utils.Prefix+"/common/Iterator/{iterId}/page", revProxy.ServeIterator)
	router.HandleFunc(utils.Prefix+"/{version}/system/symmetrix", revProxy.ifNoSymIDInvoke(revProxy.ServeSymmetrix))
	router.HandleFunc(utils.Prefix+"/{version}/system/version", revProxy.ifNoSymIDInvoke(revProxy.ServeVersions))
	router.HandleFunc(utils.Prefix+"/version", revProxy.ifNoSymIDInvoke(revProxy.ServeVersions))
	// performance
	router.HandleFunc(utils.Prefix+"/performance/Array/keys", revProxy.ifNoSymIDInvoke(revProxy.ServePerformance))
	router.HandleFunc(utils.Prefix+"/performance/Volume/metrics", revProxy.ifNoSymIDInvoke(revProxy.ServeVolumePerformance))
	router.HandleFunc(utils.Prefix+"/performance/file/filesystem/metrics", revProxy.ifNoSymIDInvoke(revProxy.ServeFSPerformance))

	// Snapshot
	router.HandleFunc(utils.Prefix+"/{version}/replication/capabilities/symmetrix", revProxy.ifNoSymIDInvoke(revProxy.ServeReplicationCapabilities))

	// file
	router.PathPrefix(utils.InternalPrefix + "/{version}/file/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)
	router.PathPrefix(utils.Prefix + "/{version}/file/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)

	// migration
	router.PathPrefix(utils.Prefix + "/{version}/migration/symmetrix/{symid}").HandlerFunc(revProxy.ServeReverseProxy)

	return revProxy.loggingMiddleware(router)
}

func (revProxy *Proxy) ifNoSymIDInvoke(customHandler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if symid, ok := revProxy.getSymID(r); ok {
			log.Debugf("Invoking revproxy client for %s.", symid)
			revProxy.ServeReverseProxy(w, r)
		} else {
			log.Debug("Invoking the common handler.")
			customHandler(w, r)
		}
	}
}

func (revProxy *Proxy) symIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		vars := mux.Vars(r)
		if symid := r.Header.Get("symid"); symid != "" {
			vars[clientSymID] = symid
			mux.SetURLVars(r, vars)
		}
		next.ServeHTTP(w, r)
	})
}

func (revProxy *Proxy) isAuthorized(res http.ResponseWriter, req *http.Request, storageArrayID string) error {
	username, password, ok := req.BasicAuth()
	if !ok {
		utils.WriteHTTPError(res, "no authorization provided", utils.StatusUnAuthorized)
	}
	_, err := revProxy.config.IsUserAuthorized(username, password, storageArrayID)
	if err != nil {
		utils.WriteHTTPError(res, err.Error(), utils.StatusUnAuthorized)
	}
	return err
}

func (revProxy *Proxy) modifyRequest(res http.ResponseWriter, req *http.Request, targetURL url.URL) {
	// Update the headers to allow for SSL redirection
	req.URL.Host = targetURL.Host
	req.URL.Scheme = targetURL.Scheme
	req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	creds, err := revProxy.config.GetManagementServerCredentials(targetURL)
	if err != nil {
		utils.WriteHTTPError(res, err.Error(), utils.StatusUnAuthorized)
		return
	}
	req.Header.Set("Authorization", utils.BasicAuth(creds))
	req.Host = targetURL.Host
}

func (revProxy *Proxy) getSymID(req *http.Request) (string, bool) {
	vars := mux.Vars(req)
	if symID, ok := vars[clientSymID]; ok {
		return symID, ok
	}
	symID, ok := vars["symid"]
	return symID, ok
}

// ServeReverseProxy - serves a reverse proxy request
func (revProxy *Proxy) ServeReverseProxy(res http.ResponseWriter, req *http.Request) {
	symID, ok := revProxy.getSymID(req)
	if !ok {
		http.Error(res, "symmetrix id missing", 400)
		return
	}
	err := revProxy.isAuthorized(res, req, symID)
	if err != nil {
		return
	}
	proxy, err := revProxy.getProxyBySymmID(symID)
	if err != nil {
		http.Error(res, err.Error(), 500)
	} else {
		revProxy.modifyRequest(res, req, proxy.URL)
		lockID := proxy.URL.String()
		requestID := req.Header.Get("RequestID")
		lockType := utils.GetRequestType(req)
		defer utils.Elapsed(requestID, "Total")()
		lock := utils.Lock{
			ID:             lockID,
			RequestID:      requestID,
			LockType:       lockType,
			MaxOutStanding: utils.GetMaxOutStanding(lockType, proxy.Limits),
			MaxActive:      utils.GetMaxActive(lockType, proxy.Limits),
		}
		err := lock.Lock()
		if err != nil {
			utils.WriteHTTPError(res, "server busy", utils.StatusProxyBusy)
			return
		}
		defer utils.Elapsed(requestID, "Unisphere RESTAPI response")()
		defer lock.Release()
		proxy.ReverseProxy.ServeHTTP(res, req)
	}
}

// ServeVersions - handler function for the version endpoint
func (revProxy *Proxy) ServeVersions(res http.ResponseWriter, req *http.Request) {
	symIDs, err := revProxy.getAuthorisedArrays(res, req)
	if err != nil {
		return
	}
	for _, symID := range symIDs {
		_, err := revProxy.getResponseIfAuthorised(res, req, symID)
		if err != nil {
			log.Errorf("Authorisation step fails for: (%s) symID with error (%s)", symID, err.Error())
		}
	}
}

// ServePerformance - handler function for the performance endpoint
func (revProxy *Proxy) ServePerformance(res http.ResponseWriter, req *http.Request) {
	symIDs, err := revProxy.getAuthorisedArrays(res, req)
	if err != nil {
		return
	}
	for _, symID := range symIDs {
		_, err := revProxy.getResponseIfAuthorised(res, req, symID)
		if err != nil {
			log.Errorf("Authorisation step fails for: (%s) symID with error (%s)", symID, err.Error())
		}
	}
}

// ServeVolumePerformance - handler function for the performance endpoint
func (revProxy *Proxy) ServeVolumePerformance(res http.ResponseWriter, req *http.Request) {
	reqParam := new(types.VolumeMetricsParam)
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(reqParam); err != nil {
		log.Errorf("Decoding fails for metrics req for volume: %s", err.Error())
		utils.WriteHTTPError(res, "failed to decode request", http.StatusInternalServerError)
		return
	}

	resp, err := revProxy.getResponseIfAuthorised(res, req, reqParam.SystemID)
	if err != nil {
		// error response written as part of call to getResponseIfAuthorised
		log.Errorf("Authorization step fails for: (%s) symID with error (%s)", reqParam.SystemID, err.Error())
		return
	}

	defer resp.Body.Close()
	err = utils.IsValidResponse(resp)
	if err != nil {
		log.Errorf("Get performance metrics step fails for: (%s) symID with error (%s)", reqParam.SystemID, err.Error())
		utils.WriteHTTPError(res, err.Error(), resp.StatusCode)
	} else {
		metricsIterator := new(types.VolumeMetricsIterator)
		if err := json.NewDecoder(resp.Body).Decode(metricsIterator); err != nil {
			utils.WriteHTTPError(res, "decoding error: "+err.Error(), 400)
			log.Errorf("decoding error: %s", err.Error())
		}
		utils.WriteHTTPResponse(res, metricsIterator)
	}
}

// ServeFSPerformance - handler function for the performance endpoint
func (revProxy *Proxy) ServeFSPerformance(res http.ResponseWriter, req *http.Request) {
	reqParam := new(types.FileSystemMetricsParam)
	decoder := json.NewDecoder(req.Body)
	if err := decoder.Decode(reqParam); err != nil {
		log.Errorf("Decoding fails for metrics req for volume: %s", err.Error())
		utils.WriteHTTPError(res, "failed to decode request", http.StatusInternalServerError)
		return
	}

	resp, err := revProxy.getResponseIfAuthorised(res, req, reqParam.SystemID)
	if err != nil {
		// error response written as part of call to getResponseIfAuthorised
		log.Errorf("Authorization step fails for: (%s) symID with error (%s)", reqParam.SystemID, err.Error())
		return
	}

	defer resp.Body.Close()
	err = utils.IsValidResponse(resp)
	if err != nil {
		log.Errorf("Get performance metrics step fails for: (%s) symID with error (%s)", reqParam.SystemID, err.Error())
		utils.WriteHTTPError(res, err.Error(), resp.StatusCode)
	} else {
		metricsIterator := new(types.FileSystemMetricsIterator)
		if err := json.NewDecoder(resp.Body).Decode(metricsIterator); err != nil {
			utils.WriteHTTPError(res, "decoding error: "+err.Error(), 400)
			log.Errorf("decoding error: %s", err.Error())
		}
		utils.WriteHTTPResponse(res, metricsIterator)
	}
}

// ServeIterator - handler function for volume iterator endpoint
func (revProxy *Proxy) ServeIterator(res http.ResponseWriter, req *http.Request) {
	vars := mux.Vars(req)
	iterID := vars["iterId"]
	u4p, err := revProxy.getIteratorByID(iterID)
	if err != nil {
		utils.WriteHTTPError(res, "Missing Iterator info", utils.StatusInternalError)
		return
	}
	err = revProxy.isAuthorized(res, req, u4p.SymmetrixID)
	if err != nil {
		return
	}
	proxy, err := revProxy.getProxyByURL(u4p)
	if err != nil {
		utils.WriteHTTPError(res, "No Proxy found", utils.StatusInternalError)
		return
	}
	revProxy.modifyRequest(res, req, u4p.URL)
	lockID := u4p.URL.String()
	requestID := req.Header.Get("RequestID")
	restCall := fmt.Sprintf("%s %s\n", req.Method, req.URL)
	lockType := utils.GetRequestType(req)
	defer utils.Elapsed(requestID, restCall)()
	lock := utils.Lock{
		ID:             lockID,
		RequestID:      requestID,
		LockType:       lockType,
		MaxOutStanding: utils.GetMaxOutStanding(lockType, proxy.Limits),
		MaxActive:      utils.GetMaxActive(lockType, proxy.Limits),
	}
	err = lock.Lock()
	if err != nil {
		utils.WriteHTTPError(res, "failed to obtain lock", utils.StatusInternalError)
	}
	defer lock.Release()
	proxy.ReverseProxy.ServeHTTP(res, req)
}

// ServeSymmetrix - handler function for symmetrix list endpoint
func (revProxy *Proxy) ServeSymmetrix(res http.ResponseWriter, req *http.Request) {
	symIDs, err := revProxy.getAuthorisedArrays(res, req)
	if err != nil {
		return
	}
	allSymmetrixIDList := new(types.SymmetrixIDList)

	for _, symID := range symIDs {
		func() {
			resp, err := revProxy.getResponseIfAuthorised(res, req, symID)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			err = utils.IsValidResponse(resp)
			if err != nil {
				log.Errorf("Get Symmetrix step fails for: (%s) symID with error (%s)", symID, err.Error())
			} else {
				symmetrixList := new(types.SymmetrixIDList)
				if err := json.NewDecoder(resp.Body).Decode(symmetrixList); err != nil {
					utils.WriteHTTPError(res, "decoding error: "+err.Error(), 400)
					log.Errorf("decoding error: %s", err.Error())
				}

				allSymmetrixIDList.SymmetrixIDs = append(allSymmetrixIDList.SymmetrixIDs, symmetrixList.SymmetrixIDs...)
			}
		}()
	}
	if len(allSymmetrixIDList.SymmetrixIDs) == 0 {
		// No valid response found, return Not Found
		utils.WriteHTTPError(res, "No valid response from the unisphere", utils.StatusNotFound)
	} else {
		utils.WriteHTTPResponse(res, allSymmetrixIDList)
	}
}

// ServeReplicationCapabilities - handler function for replicationcapabilities endpoint
func (revProxy *Proxy) ServeReplicationCapabilities(res http.ResponseWriter, req *http.Request) {
	symIDs, err := revProxy.getAuthorisedArrays(res, req)
	if err != nil {
		return
	}
	symRepCapabilities := new(types.SymReplicationCapabilities)
	for _, symID := range symIDs {
		func() {
			resp, err := revProxy.getResponseIfAuthorised(res, req, symID)
			if err != nil {
				return
			}
			defer resp.Body.Close()
			err = utils.IsValidResponse(resp)
			if err != nil {
				log.Errorf("Get replication capabilities step fails for: (%s) symID with error (%s)", symID, err.Error())
			} else {
				symCapabilities := new(types.SymReplicationCapabilities)
				if err := json.NewDecoder(resp.Body).Decode(symCapabilities); err != nil {
					utils.WriteHTTPError(res, "decoding error: "+err.Error(), 400)
					log.Errorf("decoding error: %s", err.Error())
				}

				symRepCapabilities.SymmetrixCapability = append(symRepCapabilities.SymmetrixCapability, symCapabilities.SymmetrixCapability...)
			}
		}()
	}
	if len(symRepCapabilities.SymmetrixCapability) == 0 {
		// No valid response found, return Not Found
		utils.WriteHTTPError(res, "No valid response from the unisphere", utils.StatusNotFound)
	} else {
		utils.WriteHTTPResponse(res, symRepCapabilities)
	}
}

// ServeVolume - handler function for volume endpoint
func (revProxy *Proxy) ServeVolume(res http.ResponseWriter, req *http.Request) {
	symID, _ := revProxy.getSymID(req)
	err := revProxy.isAuthorized(res, req, symID)
	if err != nil {
		return
	}
	resp, err := revProxy.getResponseIfAuthorised(res, req, symID)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	err = utils.IsValidResponse(resp)
	if err != nil {
		utils.WriteHTTPError(res, err.Error(), resp.StatusCode)
		log.Errorf("Get Volume step fails for: (%s) symID with error (%s)", symID, err.Error())
	} else {
		proxy, err := revProxy.getProxyBySymmID(symID)
		if err != nil {
			utils.WriteHTTPError(res, err.Error(), utils.StatusNotFound)
			log.Errorf("Get Proxy for: (%s) symID with error (%s)", symID, err.Error())
			return
		}
		volumeIterator, err := revProxy.setIteratorID(resp, proxy.URL, symID)
		if err != nil {
			utils.WriteHTTPError(res, err.Error(), utils.StatusInternalError)
			log.Errorf("Setting iterator failed for: (%s) symID with error (%s)", symID, err.Error())
		}
		utils.WriteHTTPResponse(res, volumeIterator)
	}
}
