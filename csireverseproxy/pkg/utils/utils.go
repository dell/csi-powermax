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

package utils

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"revproxy/v2/pkg/common"
	"runtime"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// Constants for util package
const (
	ReadRequest         = "Read"
	WriteRequest        = "Write"
	StatusUnAuthorized  = 401
	StatusInternalError = 500
	StatusProxyBusy     = 504
	StatusNotFound      = 404
	Prefix              = "/univmax/restapi"
	PrivatePrefix       = "/univmax/restapi/private"
)

// WriteHTTPError - given a statuscode and error message, writes a HTTP error using the
// ResponseWriter
func WriteHTTPError(w http.ResponseWriter, errorMsg string, statusCode int) {
	w.WriteHeader(statusCode)
	_, _ = w.Write([]byte(fmt.Sprintf("%s\n", errorMsg)))
}

// IsStringInSlice - Returns true if a string is present in a slice
func IsStringInSlice(slice []string, str string) bool {
	for _, el := range slice {
		if el == str {
			return true
		}
	}
	return false
}

// AppendIfMissingStringSlice - appends a string to a slice if it is not present
func AppendIfMissingStringSlice(slice []string, str string) []string {
	for _, el := range slice {
		if el == str {
			return slice
		}
	}
	return append(slice, str)
}

// BasicAuth - returns a base64 encoded username and password for basic auth header
func BasicAuth(credentials common.Credentials) string {
	auth := credentials.UserName + ":" + credentials.Password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
}

// Elapsed - measures the elapsed time duration for a function
func Elapsed(requestID string, op string) func() {
	start := time.Now()
	return func() {
		log.Infof("Request ID: %s - %s time: %v\n", requestID, op, time.Since(start))
	}
}

// GetRequestType - returns the type of HTTP request - Read or Write
func GetRequestType(req *http.Request) common.LockType {
	if req.Method == "GET" {
		return ReadRequest
	}
	return WriteRequest
}

// GetListenAddress - Returns the formatted port number
func GetListenAddress(portNumber string) string {
	return ":" + portNumber
}

// WriteHTTPResponse - Writes a HTTP response using the ResponseWriter
func WriteHTTPResponse(w http.ResponseWriter, val interface{}) {
	jsonBytes, err := json.Marshal(val)
	if err != nil {
		fmt.Println("error:", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	_, err = w.Write(jsonBytes)
	if err != nil {
		log.Error("Couldn't write to ResponseWriter")
		w.WriteHeader(http.StatusInternalServerError)
	}
	return
}

// IsValidResponse - checks if response is valid
func IsValidResponse(resp *http.Response) error {
	switch {
	case resp == nil:
		return fmt.Errorf("No response from API")
	case resp.StatusCode == StatusUnAuthorized:
		return fmt.Errorf("Not Authorised")
	case resp.StatusCode == StatusInternalError:
		return fmt.Errorf("Internal Server Error")
	case !(resp.StatusCode >= 200 && resp.StatusCode <= 299):
		return fmt.Errorf("Invalid Response from API")
	}
	return nil
}

// GetMaxOutStanding - Returns the max out standing request given a lock type and limits
func GetMaxOutStanding(lockType common.LockType, limits common.Limits) int {
	if lockType == ReadRequest {
		if limits.MaxOutStandingRead != 0 {
			return limits.MaxOutStandingRead
		}
		return common.MaxOutStandingReadRequests
	} else if lockType == WriteRequest {
		if limits.MaxOutStandingWrite != 0 {
			return limits.MaxOutStandingWrite
		}
		return common.MaxOutStandingWriteRequests
	}
	return 0
}

// GetMaxActive - Returns the max active requests given a lock type and limits
func GetMaxActive(lockType common.LockType, limits common.Limits) int {
	if lockType == ReadRequest {
		if limits.MaxActiveRead != 0 {
			return limits.MaxActiveRead
		}
		return common.MaxActiveReadRequests
	} else if lockType == WriteRequest {
		if limits.MaxActiveWrite != 0 {
			return limits.MaxActiveWrite
		}
		return common.MaxActiveWriteRequests
	}
	return 0
}

// RootDir - returns root directory of the binary
func RootDir() string {
	_, b, _, _ := runtime.Caller(0)
	d := path.Join(path.Dir(b))
	return filepath.Dir(d)
}

//RemoveTempFiles - Removes temporary files created during testing
func RemoveTempFiles() error {
	rootDir := RootDir()
	certsDir := rootDir + "/../" + common.DefaultCertDirName
	tmpConfigDir := rootDir + "/../" + common.TempConfigDir
	certFiles, err := ioutil.ReadDir(certsDir)
	if err != nil {
		log.Fatalf("Failed to list cert files in `%s`\n", certsDir)
		return err
	}
	configFiles, err := ioutil.ReadDir(tmpConfigDir)
	if err != nil {
		log.Fatalf("Failed to list config files in `%s`\n", tmpConfigDir)
		return err
	}
	files := append(configFiles, certFiles...)
	for _, file := range files {
		fileName := file.Name()
		var err error
		if strings.Contains(fileName, ".pem") {
			err = os.Remove(certsDir + "/" + fileName)
		} else if strings.Contains(fileName, ".yaml") {
			err = os.Remove(tmpConfigDir + "/" + fileName)
		}
		if err != nil {
			log.Fatalf("Failed to remove `%s`. (%s)\n", fileName, err.Error())
		}
	}
	return nil
}
