/*
Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/dell/csi-powermax/csireverseproxy/v2/pkg/common"
)

func TestWriteHTTPError(t *testing.T) {
	w := httptest.NewRecorder()
	WriteHTTPError(w, "Unauthorized", http.StatusUnauthorized)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("got status %d, want %d", w.Code, http.StatusUnauthorized)
	}

	if w.Body.String() != "Unauthorized\n" {
		t.Errorf("got body %s, want %s", w.Body.String(), "Unauthorized\n")
	}
}

func TestIsStringInSlice(t *testing.T) {
	slice := []string{"a", "b", "c"}
	if !IsStringInSlice(slice, "b") {
		t.Error("expected true, got false")
	}
	if IsStringInSlice(slice, "d") {
		t.Error("expected false, got true")
	}
}

func TestBasicAuth(t *testing.T) {
	credentials := common.Credentials{UserName: "user", Password: "pass"}
	expected := "Basic " + base64.StdEncoding.EncodeToString([]byte("user:pass"))
	if auth := BasicAuth(credentials); auth != expected {
		t.Errorf("got %s, want %s", auth, expected)
	}
}

func TestGetRequestType(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if GetRequestType(req) != ReadRequest {
		t.Errorf("expected ReadRequest")
	}

	req = httptest.NewRequest(http.MethodPost, "/", nil)
	if GetRequestType(req) != WriteRequest {
		t.Errorf("expected WriteRequest")
	}
}

func TestIsValidResponse(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusOK}
	if err := IsValidResponse(resp); err != nil {
		t.Errorf("expected nil error, got %v", err)
	}

	resp = &http.Response{StatusCode: http.StatusUnauthorized}
	if err := IsValidResponse(resp); err == nil {
		t.Errorf("expected error, got nil")
	}
}

func TestAppendIfMissingStringSlice(t *testing.T) {
	slice := []string{"a", "b", "c"}
	slice = AppendIfMissingStringSlice(slice, "d")
	if !IsStringInSlice(slice, "d") {
		t.Error("expected 'd' to be appended, but it was not")
	}

	slice = AppendIfMissingStringSlice(slice, "b")
	if len(slice) != 4 {
		t.Errorf("expected slice length 4, got %d", len(slice))
	}
}

func TestRemoveTempFiles(t *testing.T) {
	// Set up the test environment
	// Create temporary directories for certs and config
	rootDir := RootDir()
	path := filepath.Join(rootDir, "/../", common.DefaultCertDirName)
	certsDirPath, err := filepath.Abs(path)
	if err != nil {
		t.Fatal(err)
	}

	err = os.Mkdir(certsDirPath, 0o777)
	if err != nil && os.IsNotExist(err) {
		t.Fatal(err)
	}
	//defer os.RemoveAll(certsDirPath)

	path = filepath.Join(rootDir, "/../", common.TempConfigDir)
	configDirPath, err := filepath.Abs(path)
	err = os.Mkdir(configDirPath, 0o777)
	if err != nil && os.IsNotExist(err) {
		t.Fatal(err)
	}
	//defer os.RemoveAll(configDirPath)

	// Create temporary files in the directories
	certFile, err := os.CreateTemp(certsDirPath, "cert.pem")
	if err != nil {
		t.Fatal(err)
	}
	defer certFile.Close()

	configFile, err := os.CreateTemp(configDirPath, "config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer configFile.Close()

	// Call the function
	err = RemoveTempFiles()

	// Check the result
	if err != nil {
		t.Errorf("RemoveTempFiles() returned an error: %s", err.Error())
	}

	// Check that the files have been removed
	if _, err := os.Stat(certFile.Name()); err == nil {
		t.Errorf("Cert file still exists: %s", certFile.Name())
	}

	if _, err := os.Stat(configFile.Name()); err == nil {
		t.Errorf("Config file still exists: %s", configFile.Name())
	}
}

// MockLogger is a mock implementation of the log.Logger interface
type MockLogger struct {
	logMessage string
}

func (l *MockLogger) Write(p []byte) (n int, err error) {
	l.logMessage = string(p)
	return len(p), nil
}

func TestWriteHTTPResponse(t *testing.T) {
	// Test case: Writing a valid response
	w := httptest.NewRecorder()
	val := map[string]string{"key": "value"}

	WriteHTTPResponse(w, val)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status code %d, but got %d", http.StatusOK, w.Code)
	}

	expectedResponse := `{"key":"value"}`
	if w.Body.String() != expectedResponse {
		t.Errorf("Expected response %s, but got %s", expectedResponse, w.Body.String())
	}

	// Test case: Writing an invalid response
	w = httptest.NewRecorder()
	inval := make(chan int)

	WriteHTTPResponse(w, inval)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status code %d, but got %d", http.StatusInternalServerError, w.Code)
	}

	expectedResponse = "json: unsupported type: chan int"
	if w.Body.String() != expectedResponse {
		t.Errorf("Expected response - %s, but got - %s", expectedResponse, w.Body.String())
	}
}

func TestGetMaxActive(t *testing.T) {
	// Test case for ReadRequest with non-zero MaxActiveRead
	limits := common.Limits{
		MaxActiveRead:  10,
		MaxActiveWrite: 0,
	}
	maxActive := GetMaxActive(ReadRequest, limits)
	if maxActive != limits.MaxActiveRead {
		t.Errorf("Expected %d, but got %d", limits.MaxActiveRead, maxActive)
	}

	// Test case for ReadRequest with zero MaxActiveRead
	limits = common.Limits{
		MaxActiveRead:  0,
		MaxActiveWrite: 0,
	}
	maxActive = GetMaxActive(ReadRequest, limits)
	if maxActive != common.MaxActiveReadRequests {
		t.Errorf("Expected %d, but got %d", common.MaxActiveReadRequests, maxActive)
	}

	// Test case for WriteRequest with non-zero MaxActiveWrite
	limits = common.Limits{
		MaxActiveRead:  0,
		MaxActiveWrite: 20,
	}
	maxActive = GetMaxActive(WriteRequest, limits)
	if maxActive != limits.MaxActiveWrite {
		t.Errorf("Expected %d, but got %d", limits.MaxActiveWrite, maxActive)
	}

	// Test case for WriteRequest with zero MaxActiveWrite
	limits = common.Limits{
		MaxActiveRead:  0,
		MaxActiveWrite: 0,
	}
	maxActive = GetMaxActive(WriteRequest, limits)
	if maxActive != common.MaxActiveWriteRequests {
		t.Errorf("Expected %d, but got %d", common.MaxActiveWriteRequests, maxActive)
	}

	// Test case for invalid lockType
	maxActive = GetMaxActive(common.LockType("Invalid"), limits)
	if maxActive != 0 {
		t.Errorf("Expected %d, but got %d", 0, maxActive)
	}
}

func TestGetMaxOutStanding(t *testing.T) {
	// Test case for ReadRequest with non-zero MaxOutStandingRead
	limits := common.Limits{
		MaxOutStandingRead:  10,
		MaxOutStandingWrite: 0,
	}
	maxOutStanding := GetMaxOutStanding(ReadRequest, limits)
	if maxOutStanding != limits.MaxOutStandingRead {
		t.Errorf("Expected %d, but got %d", limits.MaxOutStandingRead, maxOutStanding)
	}

	// Test case for ReadRequest with zero MaxOutStandingRead
	limits = common.Limits{
		MaxOutStandingRead:  0,
		MaxOutStandingWrite: 0,
	}
	maxOutStanding = GetMaxOutStanding(ReadRequest, limits)
	if maxOutStanding != common.MaxOutStandingReadRequests {
		t.Errorf("Expected %d, but got %d", common.MaxOutStandingReadRequests, maxOutStanding)
	}

	// Test case for WriteRequest with non-zero MaxOutStandingWrite
	limits = common.Limits{
		MaxOutStandingRead:  0,
		MaxOutStandingWrite: 20,
	}
	maxOutStanding = GetMaxOutStanding(WriteRequest, limits)
	if maxOutStanding != limits.MaxOutStandingWrite {
		t.Errorf("Expected %d, but got %d", limits.MaxOutStandingWrite, maxOutStanding)
	}

	// Test case for WriteRequest with zero MaxOutStandingWrite
	limits = common.Limits{
		MaxOutStandingRead:  0,
		MaxOutStandingWrite: 0,
	}
	maxOutStanding = GetMaxOutStanding(WriteRequest, limits)
	if maxOutStanding != common.MaxOutStandingWriteRequests {
		t.Errorf("Expected %d, but got %d", common.MaxOutStandingWriteRequests, maxOutStanding)
	}

	// Test case for invalid lockType
	maxOutStanding = GetMaxOutStanding(common.LockType("Invalid"), limits)
	if maxOutStanding != 0 {
		t.Errorf("Expected %d, but got %d", 0, maxOutStanding)
	}
}

func TestGetListenAddress(t *testing.T) {
	// Test case for valid port number
	portNumber := "8080"
	expectedListenAddress := ":" + portNumber
	listenAddress := GetListenAddress(portNumber)
	if listenAddress != expectedListenAddress {
		t.Errorf("Expected %s, but got %s", expectedListenAddress, listenAddress)
	}

	// Test case for empty port number
	portNumber = ""
	expectedListenAddress = ":"
	listenAddress = GetListenAddress(portNumber)
	if listenAddress != expectedListenAddress {
		t.Errorf("Expected %s, but got %s", expectedListenAddress, listenAddress)
	}
}
