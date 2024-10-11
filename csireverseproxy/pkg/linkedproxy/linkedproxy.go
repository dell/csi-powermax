/*
 Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"revproxy/pkg/common"
	"revproxy/pkg/config"
	"revproxy/pkg/utils"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/gorilla/mux"
)

// LinkedProxy represents a Linked Proxy
type LinkedProxy struct {
	Config      config.LinkedProxyConfig
	requestID   uint64
	active      int32
	Primary     *common.Proxy
	Backup      *common.Proxy
	mutex       sync.Mutex
	ProxyHealth common.ProxyHealth
}

// NewLinkedProxy - Given a proxy config, creates a Linked Proxy
func NewLinkedProxy(proxyConfig config.LinkedProxyConfig) (*LinkedProxy, error) {
	var linkedProxy LinkedProxy
	var primaryProxy, backupProxy *common.Proxy
	var err error
	primaryProxy, err = newReverseProxy(proxyConfig.Primary, linkedProxy.HealthHandler)
	if err != nil {
		return nil, err
	}
	if proxyConfig.Backup != nil {
		backupProxy, err = newReverseProxy(proxyConfig.Backup, linkedProxy.HealthHandler)
		if err != nil {
			return nil, err
		}
	}
	linkedProxy.updateConfig(proxyConfig, primaryProxy, backupProxy)
	linkedProxy.ProxyHealth = common.NewProxyHealth()
	return &linkedProxy, nil
}

func newReverseProxy(server *config.ManagementServer, callback func(bool)) (*common.Proxy, error) {
	revProxy := httputil.NewSingleHostReverseProxy(&server.URL)
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
	revProxyTransport := &http.Transport{
		TLSClientConfig:     &tlsConfig,
		MaxIdleConnsPerHost: 50,
		MaxIdleConns:        100,
	}
	revProxy.Transport = &common.Transport{
		RoundTripper:  revProxyTransport,
		HealthHandler: callback,
	}
	proxy := common.Proxy{
		ReverseProxy: revProxy,
		URL:          server.URL,
		Limits:       server.Limits,
	}
	return &proxy, nil
}

// HealthHandler - call back method which updates a proxy's health
func (revProxy *LinkedProxy) HealthHandler(isSuccess bool) {
	if isSuccess {
		revProxy.ProxyHealth.ReportSuccess()
	} else {
		if revProxy.ProxyHealth.ReportFailure() {
			if atomic.LoadInt32(&revProxy.active) == 0 {
				atomic.AddInt32(&revProxy.active, 1)
				log.Println("Switched to backup proxy")
			} else {
				atomic.AddInt32(&revProxy.active, -1)
				log.Println("Switched back to primary proxy")
			}
		}
	}
}

func (revProxy *LinkedProxy) setProxy(proxy *common.Proxy, isPrimary bool) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	if isPrimary {
		revProxy.Primary = proxy
	} else {
		revProxy.Backup = proxy
	}
}

func (revProxy *LinkedProxy) updateConfig(proxyConfig config.LinkedProxyConfig, primaryProxy *common.Proxy, backupProxy *common.Proxy) {
	revProxy.mutex.Lock()
	defer revProxy.mutex.Unlock()
	revProxy.Config = proxyConfig
	if primaryProxy != nil {
		revProxy.Primary = primaryProxy
	}
	if backupProxy != nil {
		revProxy.Backup = backupProxy
	}
}

func (revProxy *LinkedProxy) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqID := revProxy.getRequestID()
		r.URL.Path = strings.TrimSuffix(r.URL.Path, "/")
		r.Header.Set("RequestID", reqID)
		logMsg := fmt.Sprintf("Request ID: %s - %s %s", reqID, r.Method, r.URL)
		log.Println(logMsg)
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
		log.Println("No changes detected in the configuration")
		return nil
	}
	// Check for changes in primary
	newPrimary := linkedProxyConfig.Primary
	oldPrimary := revProxy.Config.Primary
	var newPrimaryProxy *common.Proxy
	var newBackupProxy *common.Proxy
	var err error
	if !reflect.DeepEqual(*newPrimary, *oldPrimary) {
		log.Printf("Primary URL changed. New URL: %s, Old URL: %s ", newPrimary.URL.Host, oldPrimary.URL.Host)
		// We need to setup new reverseproxy
		newPrimaryProxy, err = newReverseProxy(linkedProxyConfig.Primary, revProxy.HealthHandler)
		if err != nil {
			return err
		}
	}
	if linkedProxyConfig.Backup != nil {
		newBackup := linkedProxyConfig.Backup
		oldBackup := revProxy.Config.Backup
		if oldBackup == nil {
			log.Printf("Backup URL added")
			newBackupProxy, err = newReverseProxy(linkedProxyConfig.Backup, revProxy.HealthHandler)
			if err != nil {
				return err
			}
		} else if !reflect.DeepEqual(*newBackup, *oldBackup) {
			log.Printf("Backup URL changed. New URL: %s, Old URL: %s ", newBackup.URL.Host, oldBackup.URL.Host)
			// We need to setup new reverseproxy
			newBackupProxy, err = newReverseProxy(linkedProxyConfig.Backup, revProxy.HealthHandler)
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
	if revProxy.active == 0 || revProxy.Backup == nil {
		return *revProxy.Primary
	}
	return *revProxy.Backup
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
