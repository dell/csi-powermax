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

package symmetrix

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	log "github.com/sirupsen/logrus"

	pmax "github.com/dell/gopowermax"
)

var metroClients sync.Map

const (
	failoverThereshold   int           = 5
	failureTimeThreshold time.Duration = 2 * time.Minute
)

func init() {
	metroClients = sync.Map{}
}

type metroClient struct {
	primaryArray   string
	secondaryArray string
	activeArray    string
	failureCount   int
	lastFailure    time.Time
	mx             sync.Mutex
}

func (m *metroClient) getActiveArray() string {
	m.mx.Lock()
	defer m.mx.Unlock()
	if m.failureCount >= failoverThereshold {
		if m.activeArray == m.primaryArray {
			m.activeArray = m.secondaryArray
		} else {
			m.activeArray = m.primaryArray
		}
		m.failureCount = 0
		log.Infof("Failing over to the array: %s", m.activeArray)
	}
	return m.activeArray
}

func (m *metroClient) setErrorWatcher(powermaxClient pmax.Pmax) {
	client := powermaxClient.GetHTTPClient()
	oldTransport := client.Transport
	client.Transport = &transport{
		RoundTripper:  oldTransport,
		healthHandler: m.healthHandler,
	}
}

func (m *metroClient) healthHandler(failureWeight int) {
	m.mx.Lock()
	defer m.mx.Unlock()
	timeSinceLastFailure := time.Since(m.lastFailure)
	m.lastFailure = time.Now()
	if timeSinceLastFailure > failureTimeThreshold && m.failureCount != 0 {
		log.Infof("Last failure was more than %f minutes ago; reseting the failure count", failureTimeThreshold.Minutes())
		m.failureCount = 1
	} else {
		m.failureCount = m.failureCount + failureWeight
	}
}

func (m *metroClient) getPowerMaxClient() (pmax.Pmax, error) {
	powermax, err := getPowerMax(m.getActiveArray())
	if err != nil {
		return nil, err
	}
	client := powermax.getClient()
	m.setErrorWatcher(client)
	return client, nil
}

func (m *metroClient) getIdentifier() string {
	return fmt.Sprintf("%s-%s", m.primaryArray, m.secondaryArray)
}

type transport struct {
	http.RoundTripper
	healthHandler func(int)
}

func (t *transport) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := t.RoundTripper.RoundTrip(req)
	if err != nil {
		t.healthHandler(2)
	} else if resp.StatusCode == 401 || resp.StatusCode == 403 {
		t.healthHandler(99)
	} else if int(resp.StatusCode/100) == 5 {
		t.healthHandler(1)
	}
	return resp, err
}
