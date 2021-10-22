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

package linkedproxy

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"revproxy/v2/pkg/common"
	"revproxy/v2/pkg/config"
	"revproxy/v2/pkg/utils"
	"strings"
	"sync"
	"sync/atomic"

	log "github.com/sirupsen/logrus"

	"github.com/gorilla/mux"
)

// LinkedProxy represents a Linked Proxy
type LinkedProxy struct {
	Config    config.LinkedProxyConfig
	requestID uint64
	mutex     sync.Mutex
	Envoy     common.Envoy
}

// NewLinkedProxy - Given a proxy config, creates a Linked Proxy
func NewLinkedProxy(proxyConfig config.LinkedProxyConfig) (*LinkedProxy, error) {
	var linkedProxy LinkedProxy
	var primaryProxy, backupProxy *common.Proxy
	var err error
	primaryProxy, err = newReverseProxy(proxyConfig.Primary)
	if err != nil {
		return nil, err
	}
	linkedProxy.Envoy = common.NewEnvoy(primaryProxy)
	if proxyConfig.Backup != nil {
		backupProxy, err = newReverseProxy(proxyConfig.Backup)
		if err != nil {
			return nil, err
		}
	}
	linkedProxy.updateConfig(proxyConfig, nil, backupProxy)
	return &linkedProxy, nil
}

func newReverseProxy(server *config.ManagementServer) (*common.Proxy, error) {
	revProxy := httputil.NewSingleHostReverseProxy(&server.URL)
	// #nosec G402
	tlsConfig := tls.Config{
		InsecureSkipVerify: server.SkipCertificateValidation,
	}
	if !server.SkipCertificateValidation {
		caCert, err := ioutil.ReadFile(server.CertFile)
		if err != nil {
			return nil, err
		}
		caCertPool := x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}
	revProxy.Transport = &http.Transport{
		TLSClientConfig:     &tlsConfig,
		MaxIdleConnsPerHost: 50,
		MaxIdleConns:        100,
	}
	proxy := common.Proxy{
		ReverseProxy: revProxy,
		URL:          server.URL,
		Limits:       server.Limits,
	}
	return &proxy, nil
}

func (revProxy *LinkedProxy) setProxy(proxy *common.Proxy, isPrimary bool) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if isPrimary {
		revProxy.Envoy.SetPrimary(proxy)
	} else {
		revProxy.Envoy.SetBackup(proxy)
	}
}

func (revProxy *LinkedProxy) updateConfig(proxyConfig config.LinkedProxyConfig, primaryProxy *common.Proxy, backupProxy *common.Proxy) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	revProxy.Config = proxyConfig
	if primaryProxy != nil {
		revProxy.Envoy.SetPrimary(primaryProxy)
	}
	if backupProxy != nil {
		revProxy.Envoy.SetBackup(backupProxy)
	}
}

func (revProxy *LinkedProxy) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := revProxy.getRequestID()
		r.URL.Path = strings.TrimSuffix(r.URL.Path, "/")
		r.Header.Set("RequestID", reqID)
		logMsg := fmt.Sprintf("Request ID: %s - %s %s", reqID, r.Method, r.URL)
		log.Info(logMsg)
		next.ServeHTTP(w, r)
	})
}

// GetRouter - sets up the mux router and logging middleware and returns
// back a http handler
func (revProxy *LinkedProxy) GetRouter() http.Handler {
	router := mux.NewRouter()
	router.PathPrefix("/univmax").HandlerFunc(revProxy.ServeReverseProxy)
	return revProxy.loggingMiddleware(router)
}

// UpdateConfig - given a new proxy config, updates the configuration of a LinkedProxy
func (revProxy *LinkedProxy) UpdateConfig(proxyConfig config.ProxyConfig) error {
	linkedProxyConfig := proxyConfig.LinkProxyConfig
	if linkedProxyConfig == nil {
		return fmt.Errorf("LinkProxyConfig can't be nil")
	}
	if reflect.DeepEqual(revProxy.Config, *linkedProxyConfig) {
		log.Info("No changes detected in the configuration")
		return nil
	}
	// Check for changes in primary
	newPrimary := linkedProxyConfig.Primary
	oldPrimary := revProxy.Config.Primary
	var newPrimaryProxy *common.Proxy
	var newBackupProxy *common.Proxy
	var err error
	if !reflect.DeepEqual(*newPrimary, *oldPrimary) {
		log.Infof("Primary URL changed. New URL: %s, Old URL: %s ", newPrimary.URL.Host, oldPrimary.URL.Host)
		// We need to setup new reverseproxy
		newPrimaryProxy, err = newReverseProxy(linkedProxyConfig.Primary)
		if err != nil {
			return err
		}
	}
	if linkedProxyConfig.Backup != nil {
		newBackup := linkedProxyConfig.Backup
		oldBackup := revProxy.Config.Backup
		if oldBackup == nil {
			log.Infof("Backup URL added")
			newBackupProxy, err = newReverseProxy(linkedProxyConfig.Backup)
			if err != nil {
				return err
			}
		} else if !reflect.DeepEqual(*newBackup, *oldBackup) {
			log.Infof("Backup URL changed. New URL: %s, Old URL: %s ", newBackup.URL.Host, oldBackup.URL.Host)
			// We need to setup new reverseproxy
			newBackupProxy, err = newReverseProxy(linkedProxyConfig.Backup)
			if err != nil {
				return err
			}
		}
	}
	revProxy.updateConfig(*linkedProxyConfig, newPrimaryProxy, newBackupProxy)
	return nil
}

func (revProxy *LinkedProxy) getRequestID() string {
	requestID := fmt.Sprintf("%d", atomic.AddUint64(&revProxy.requestID, 1))
	return requestID
}

func (revProxy *LinkedProxy) getProxy() common.Proxy {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	return *(revProxy.Envoy.GetActiveProxy())
}

func (revProxy *LinkedProxy) modifyRequest(req *http.Request, targetURL url.URL) {
	req.URL.Host = targetURL.Host
	req.URL.Scheme = targetURL.Scheme
	req.Header.Set("X-Forwarded-Host", req.Header.Get("Host"))
	req.Host = targetURL.Host
}

// ServeReverseProxy - Serve a reverse proxy for a given url
func (revProxy *LinkedProxy) ServeReverseProxy(res http.ResponseWriter, req *http.Request) {
	proxy := revProxy.getProxy()
	// Update the headers to allow for SSL redirection
	revProxy.modifyRequest(req, proxy.URL)
	lockID := proxy.URL.String()
	requestID := req.Header.Get("RequestID")
	defer utils.Elapsed(requestID, "Total")()
	lockType := utils.GetRequestType(req)
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
	defer lock.Release()
	defer utils.Elapsed(requestID, "Unisphere RESTAPI response")()
	proxy.ReverseProxy.ServeHTTP(res, req)
}
