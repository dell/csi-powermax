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

package servermock

import (
	"net/http"
	"revproxy/v2/pkg/utils"

	"github.com/gorilla/mux"
)

const (
	authenticationEndpoint = "/univmax/authenticate"
	timeoutEndpoint        = "/univmax/timeout"
	defaultEndpoint        = "/univmax"
)

var mockRouter http.Handler

// GetHandler returns the http handler
func GetHandler() http.Handler {
	handler := http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if mockRouter != nil {
				mockRouter.ServeHTTP(w, r)
			} else {
				getRouter().ServeHTTP(w, r)
			}
		})
	return handler
}

func getRouter() http.Handler {
	router := mux.NewRouter()
	router.HandleFunc(authenticationEndpoint, handleAuth)
	router.HandleFunc(defaultEndpoint, handleDefault)
	router.HandleFunc(timeoutEndpoint, handleTimeout)
	router.HandleFunc(utils.Prefix+"/version", handleVersion)
	router.HandleFunc(utils.Prefix+"/{version}/system/symmetrix", handleSymm)
	router.HandleFunc(utils.Prefix+"/common/Iterator/{iterId}/page", handleDefault)
	router.HandleFunc(utils.Prefix+"/{version}/replication/capabilities/symmetrix", handleSymmCapabilities)
	router.HandleFunc(utils.Prefix+"/{version}/sloprovisioning/symmetrix/{symid}", handleDefault)
	router.Path(utils.Prefix + "/{version}/sloprovisioning/symmetrix/{symid}/volume").HandlerFunc(handleVolume)
	mockRouter = router
	return router
}

func handleVolume(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{ "id": "00000000-1111-2abc-def3-44gh55ij66kl_0" }`))
}

func handleSymmCapabilities(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("{\"symmetrixCapability\":[{\"symmetrixId\":\"000000000000\",\"snapVxCapable\":true,\"rdfCapable\":true,\"virtualWitnessCapable\":false}]}"))
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{ "version": "V9.1.0.2" }`))
}

func handleSymm(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	data := `{"symmetrixId": [ "000197802104", "000197900046", "000197900047" ]}`
	_, _ = w.Write([]byte(data))
}

func handleTimeout(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusServiceUnavailable)
	_, _ = w.Write([]byte(r.Host))
}

func handleDefault(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(r.Host))
}

func handleAuth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(r.Host))
}
