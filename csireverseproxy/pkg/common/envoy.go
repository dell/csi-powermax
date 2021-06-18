package common

import (
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// Envoy is an interface for failover/failback enabled proxy clients
type Envoy interface {
	SetPrimary(*Proxy) Envoy
	SetBackup(*Proxy) Envoy
	GetPrimary() *Proxy
	GetBackup() *Proxy
	GetActiveProxy() *Proxy
	SetPrimaryHTTPClient(*http.Client) Envoy
	SetBackupHTTPClient(*http.Client) Envoy
	GetPrimaryHTTPClient() *http.Client
	GetBackupHTTPClient() *http.Client
	GetActiveHTTPClient() *http.Client
	RemoveBackupProxy()
	RemoveBackupHTTPClient()
	ConfigureHealthParams(int, int, time.Duration)
	HasHealthDeteriorated() bool
}

// NewEnvoy creates an envoy object which implements Envoy interface
func NewEnvoy(proxy *Proxy) Envoy {
	e := &envoy{
		healthMonitor: NewProxyHealth(),
	}
	e.setPrimary(proxy)
	return e
}

var _ Envoy = new(envoy)

// Envoy
type envoy struct {
	primary       *Proxy
	backup        *Proxy
	primaryHTTP   *http.Client
	backupHTTP    *http.Client
	healthMonitor ProxyHealth
	active        int32
}

func (e *envoy) updateTransport(proxy *Proxy) {
	transport := proxy.ReverseProxy.Transport
	proxy.ReverseProxy.Transport = &Transport{
		RoundTripper:  transport,
		HealthHandler: e.healthHandler,
	}
}

func (e *envoy) updateHTTPClientTransport(client *http.Client) {
	transport := client.Transport
	client.Transport = &Transport{
		RoundTripper:  transport,
		HealthHandler: e.healthHandler,
	}
}

func (e *envoy) setPrimary(proxy *Proxy) Envoy {
	e.updateTransport(proxy)
	e.primary = proxy
	return e
}

// SetPrimary sets the primary proxy client for envoy
func (e *envoy) SetPrimary(proxy *Proxy) Envoy {
	return e.setPrimary(proxy)
}

// SetBackup sets the backup proxy client for envoy
func (e *envoy) SetBackup(proxy *Proxy) Envoy {
	e.updateTransport(proxy)
	e.backup = proxy
	return e
}

// SetPrimaryHTTPClient sets the primary http-client for the envoy
func (e *envoy) SetPrimaryHTTPClient(client *http.Client) Envoy {
	e.updateHTTPClientTransport(client)
	e.primaryHTTP = client
	return e
}

// SetBackupHTTPClient sets the backup http-client for the envoy
func (e *envoy) SetBackupHTTPClient(client *http.Client) Envoy {
	e.updateHTTPClientTransport(client)
	e.backupHTTP = client
	return e
}

// GetPrimaryHTTPClient return the primary http-client
func (e *envoy) GetPrimaryHTTPClient() *http.Client {
	return e.primaryHTTP
}

// GetBackupHTTPClient return the backup http-client
func (e *envoy) GetBackupHTTPClient() *http.Client {
	return e.backupHTTP
}

// GetPrimary returns the primary proxy client
func (e *envoy) GetPrimary() *Proxy {
	return e.primary
}

// GetBackup returns the backup proxy client
func (e *envoy) GetBackup() *Proxy {
	return e.backup
}

// healthHandler - call back method which updates a proxy's health
func (e *envoy) healthHandler(isSuccess bool) {
	if isSuccess {
		e.healthMonitor.ReportSuccess()
	} else {
		if e.healthMonitor.ReportFailure() {
			if atomic.LoadInt32(&e.active) == 0 {
				atomic.AddInt32(&e.active, 1)
				log.Println("Switched to backup proxy")
			} else {
				atomic.AddInt32(&e.active, -1)
				log.Println("Switched back to primary proxy")
			}
		}
	}
}

// GetActiveProxy returns the current active proxy
func (e *envoy) GetActiveProxy() *Proxy {
	if e.active == 0 || e.backup == nil {
		return e.primary
	}
	return e.backup
}

// GetActiveHTTPClient returns the active http-client
func (e *envoy) GetActiveHTTPClient() *http.Client {
	if e.active == 0 || e.backup == nil {
		return e.primaryHTTP
	}
	return e.backupHTTP
}

// RemoveBackupProxy removes the backup proxy client from envoy
func (e *envoy) RemoveBackupProxy() {
	e.backup = nil
}

// RemoveBackupHTTPClient deletes the backup http-client
func (e *envoy) RemoveBackupHTTPClient() {
	e.backupHTTP = nil
}

// ConfigureHealthParams set the threshold params for enovy health
func (e *envoy) ConfigureHealthParams(failureCount, successCount int, failureDuration time.Duration) {
	e.healthMonitor.SetThreshold(failureCount, successCount, failureDuration)
}

// HasHealthDeteriorated checks if evnoy has encountered any error while using primary proxy
func (e *envoy) HasHealthDeteriorated() bool {
	return e.healthMonitor.HasDeteriorated()
}
