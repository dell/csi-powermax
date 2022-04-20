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
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

const (
	numOfCylindersForDefaultSize = 547
)

var s service
var mockedExitStatus = 0
var mockedStdout string
var debugUnitTest = false

var counters = [60]int{}
var testwg sync.WaitGroup

func incrementCounter(identifier string, num int) {
	lockNumber := RequestLock(identifier, "")
	timeToSleep := rand.Intn(1010-500) + 500
	time.Sleep(time.Duration(timeToSleep) * time.Microsecond)
	if debugUnitTest {
		fmt.Printf("Sleeping for :%d microseconds\n", timeToSleep)
	}
	counters[num]++
	ReleaseLock(identifier, "", lockNumber)
	testwg.Done()
}

// TestReleaseLockWOAcquiring tries to release a lock that
// was never acquired.
func TestReleaseLockWOAcquiring(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	ReleaseLock("nonExistentLock", "", 0)
}

// TestReleasingOtherLock tries to release a lock that it didn't acquire
func TestReleasingOtherLock(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	lockNumber := RequestLock("new_lock", "")
	ReleaseLock("new_lock", "", lockNumber+1)
	ReleaseLock("new_lock", "", lockNumber)
}

var lockCounter int

func incrementLockCounter() {
	lockNumber := RequestLock("identifier", "")
	defer ReleaseLock("identifier", "", lockNumber)
	lockCounter++
}

func TestLockCounter(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	for i := 0; i < 500; i++ {
		// Acquire and release the lock in same goroutine
		incrementLockCounter()
	}
	if lockCounter != 500 {
		t.Error(fmt.Sprintf("Expected lock counter to be 500 but found: %d", lockCounter))
	}
}
func TestLocks(t *testing.T) {
	LockRequestHandler()
	CleanupMapEntries(10 * time.Millisecond)
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		testwg.Add(1)
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	testwg.Wait()
	// Check if all the counters were updated properly
	for _, counter := range counters {
		if counter != 6 {
			t.Error(fmt.Sprintf("expected counter to be %d but found %d", 6, counter))
		}
	}
}

func TestGetVolSize(t *testing.T) {
	tests := []struct {
		cr             *csi.CapacityRange
		numOfCylinders int
	}{
		{
			// not requesting any range should result in a default size
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    0,
			},
			numOfCylinders: numOfCylindersForDefaultSize,
		},
		{
			// requesting a minimum below the MinVolumeSizeBytes
			cr: &csi.CapacityRange{
				RequiredBytes: MinVolumeSizeBytes - 1,
				LimitBytes:    0,
			},
			numOfCylinders: 0,
		},
		{
			// requesting a negative required bytes
			cr: &csi.CapacityRange{
				RequiredBytes: -1,
				LimitBytes:    0,
			},
			numOfCylinders: 0,
		},
		{
			// requesting a negative limit bytes
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    -1,
			},
			numOfCylinders: 0,
		},
		{
			// not requesting a minimum but setting a limit below
			// the minimum size should result in an error
			cr: &csi.CapacityRange{
				RequiredBytes: 0,
				LimitBytes:    MinVolumeSizeBytes - 1,
			},
			numOfCylinders: 0,
		},
		{
			// requesting same sizes for minimum and maximum
			// which can be serviced
			cr: &csi.CapacityRange{
				RequiredBytes: MinVolumeSizeBytes,
				LimitBytes:    MinVolumeSizeBytes,
			},
			numOfCylinders: 26,
		},
		{
			// requesting size of 50 MB which is the advertised
			// minimum volume size
			cr: &csi.CapacityRange{
				RequiredBytes: 50 * 1024 * 1024,
				LimitBytes:    0,
			},
			numOfCylinders: 27,
		},
		{
			// requesting same sizes for minimum and maximum
			// which can't be serviced
			cr: &csi.CapacityRange{
				RequiredBytes: DefaultVolumeSizeBytes,
				LimitBytes:    DefaultVolumeSizeBytes,
			},
			numOfCylinders: 0,
		},
		{
			// requesting volume size of 1 TB
			cr: &csi.CapacityRange{
				RequiredBytes: 1099511627776, // 1* 1024 * 1024 * 1024 * 1024
				LimitBytes:    0,
			},
			numOfCylinders: 559241,
		},
		{
			// requesting volume of MaxVolumeSizeBytes
			cr: &csi.CapacityRange{
				RequiredBytes: MaxVolumeSizeBytes,
				LimitBytes:    0,
			},
			numOfCylinders: 559241,
		},
		{
			// requesting volume size of more than 1 TB
			cr: &csi.CapacityRange{
				RequiredBytes: MaxVolumeSizeBytes + 1,
				LimitBytes:    0,
			},
			numOfCylinders: 0,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run("", func(st *testing.T) {
			st.Parallel()
			s := &service{}
			num, err := s.validateVolSize(context.Background(), tt.cr, "", "", s.adminClient)
			if tt.numOfCylinders == 0 {
				// error is expected
				assert.Error(st, err)
			} else {
				assert.EqualValues(st, tt.numOfCylinders, num)
			}
		})
	}
}

func TestVolumeIdentifier(t *testing.T) {
	volumePrefix := s.getClusterPrefix()
	devID := "12345"
	volumeName := "Vol-Name"
	symID := "123456789012"
	csiDeviceID := s.createCSIVolumeID(volumePrefix, volumeName, symID, devID)
	volumeNameT, symIDT, devIDT, _, _, err := s.parseCsiID(csiDeviceID)
	if err != nil {
		t.Error()
		t.Error(err.Error())
	}
	volumeName = fmt.Sprintf("csi-%s-%s", volumePrefix, volumeName)
	if volumeNameT != volumeName ||
		symIDT != symID || devIDT != devID {
		t.Error("createCSIVolumeID and parseCsiID doesn't match")
	}
	// Test for empty device id
	_, _, _, _, _, err = s.parseCsiID("")
	if err == nil {
		t.Error("Expected an error while parsing empty ID but recieved success")
	}
	// Test for malformed device id
	malformedCSIDeviceID := "Vol1-Test"
	volumeNameT, symIDT, devIDT, _, _, err = s.parseCsiID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
	malformedCSIDeviceID = "-vol1-Test"
	_, _, _, _, _, err = s.parseCsiID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
}

func TestMetroCSIDeviceID(t *testing.T) {
	volumePrefix := s.getClusterPrefix()
	devID := "12345"
	volumeName := "Vol-Name"
	symID := "123456789012"
	remoteDevID := "98765"
	remoteSymID := "000000000012"
	volumeName = fmt.Sprintf("csi-%s-%s", volumePrefix, volumeName)
	csiDeviceID := fmt.Sprintf("%s-%s:%s-%s:%s", volumeName, symID, remoteSymID, devID, remoteDevID)
	volumeNameT, symIDT, devIDT, remSymIDT, remoteDevIDT, err := s.parseCsiID(csiDeviceID)
	if err != nil {
		t.Error()
		t.Error(err.Error())
	}
	if volumeNameT != volumeName ||
		symIDT != symID || devIDT != devID || remoteDevIDT != remoteDevID || remSymIDT != remoteSymID {
		t.Error("createCSIVolumeID and parseCsiID doesn't match")
	}
}

func TestStringSliceComparison(t *testing.T) {

	valA := []string{"a", "b", "c"}
	valB := []string{"c", "b", "a"}
	valC := []string{"a", "b"}
	valD := []string{"a", "b", "d"}

	if !stringSlicesEqual(valA, valB) {
		t.Error("Could not validate that reversed slices are equal")
	}
	if stringSlicesEqual(valA, valC) {
		t.Error("Could not validate that slices of different sizes are different")
	}
	if stringSlicesEqual(valA, valD) {
		t.Error("Could not validate that slices of different content are different")
	}
}

func TestStringSliceRegexMatcher(t *testing.T) {
	slice1 := []string{"aaa", "bbb", "abbba"}
	matches := stringSliceRegexMatcher(slice1, ".*bbb.*")
	if len(matches) != 2 {
		t.Errorf("Expected 2 matches got %d: %s", len(matches), matches)
	}
	// Test using bad regex
	matches = stringSliceRegexMatcher(slice1, "[a*")
	if len(matches) != 0 {
		t.Errorf("Expected 2 matches got %d: %s", len(matches), matches)
	}
}

func TestExecCommandHelper(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}

	fmt.Printf("Mocked stdout: %s", os.Getenv("STDOUT"))
	fmt.Fprintf(os.Stdout, os.Getenv("STDOUT"))
	i, _ := strconv.Atoi(os.Getenv("EXIT_STATUS"))
	os.Exit(i)
}

func TestAppendIfMissing(t *testing.T) {
	testStrings := []string{"Test1", "Test2", "Test3"}
	testStrings = appendIfMissing(testStrings, "Test1")
	count := 0
	for _, str := range testStrings {
		if str == "Test1" {
			count++
		}
	}
	if count != 1 {
		t.Error(fmt.Sprintf("Expected no more than one occurence of string Test1 in slice but found %d", count))
	}
	count = 0
	testStrings = appendIfMissing(testStrings, "Test4")
	for _, str := range testStrings {
		if str == "Test4" {
			count++
		}
	}
	if count != 1 {
		t.Error(fmt.Sprintf("Expected no more than one occurence of string Test4 in slice but found %d", count))
	}
}

func TestTruncateString(t *testing.T) {
	stringToBeTruncated := "abcdefghijklmnopqrstuvwxyz"
	// Set maxLength to an even number
	truncatedString := truncateString(stringToBeTruncated, 10)
	if truncatedString != "abcdevwxyz" {
		t.Error("Truncated string doesn't match the expected string")
	}
	// Set maxLength to an odd number
	truncatedString = truncateString(stringToBeTruncated, 11)
	if truncatedString != "abcdeuvwxyz" {
		t.Error("Truncated string doesn't match the expected string")
	}
}

func TestFibreChannelSplitInitiatorID(t *testing.T) {
	director, port, initiator, err := splitFibreChannelInitiatorID("FA-2A:6:0x1000000000000000")
	if director != "FA-2A" {
		t.Errorf("Expected director FA-2A got %s", director)
	}
	if port != "FA-2A:6" {
		t.Errorf("Expected port FA-2A:6 got %s", port)
	}
	if initiator != "0x1000000000000000" {
		t.Errorf("Expected initiator 0x1000000000000000 got %s", initiator)
	}
	_, _, _, err = splitFibreChannelInitiatorID("meaningless string")
	if err == nil {
		t.Errorf("Expected error but got none")
	}
}

func TestPending(t *testing.T) {
	tests := []struct {
		npending     int
		maxpending   int
		differentIDs bool
		errormsg     string
	}{
		{npending: 2,
			maxpending:   1,
			differentIDs: true,
			errormsg:     "overload",
		},
		{npending: 4,
			maxpending:   5,
			differentIDs: true,
			errormsg:     "none",
		},
		{npending: 2,
			maxpending:   5,
			differentIDs: false,
			errormsg:     "pending",
		},
		{npending: 0,
			maxpending:   1,
			differentIDs: false,
			errormsg:     "none",
		},
	}
	for _, test := range tests {
		pendState := &pendingState{
			maxPending: test.maxpending,
		}
		for i := 0; i < test.npending; i++ {
			id := strconv.Itoa(i)
			if test.differentIDs == false {
				id = "same"
			}
			var vid volumeIDType
			vid = volumeIDType(id)
			err := vid.checkAndUpdatePendingState(pendState)
			if debugUnitTest {
				fmt.Printf("test %v err %v\n", test, err)
			}
			if i+1 == test.npending {
				if test.errormsg == "none" {
					if err != nil {
						t.Error("Expected no error but got: " + err.Error())
					}
				} else {
					if err != nil && !strings.Contains(err.Error(), test.errormsg) {
						t.Error("Didn't get expected error: " + test.errormsg)
					}
				}
			}
		}
		for i := 0; i <= test.maxpending; i++ {
			id := strconv.Itoa(i)
			if test.differentIDs == false {
				id = "same"
			}
			var vid volumeIDType
			vid = volumeIDType(id)
			vid.clearPending(pendState)
		}
	}
}

func TestGobrickInitialization(t *testing.T) {
	iscsiConnectorPrev := s.iscsiConnector
	s.iscsiConnector = nil
	s.initISCSIConnector("/")
	if s.iscsiConnector == nil {
		t.Error("Expected s.iscsiConnector to be initialized")
	}
	s.iscsiConnector = iscsiConnectorPrev

	fcConnectorPrev := s.fcConnector
	s.fcConnector = nil
	s.initFCConnector("/")
	if s.fcConnector == nil {
		t.Error("Expected s.fcConnector to be initialized")
	}
	s.fcConnector = fcConnectorPrev
}

func TestSetGetLogFields(t *testing.T) {
	fields := log.Fields{
		"RequestID": "123",
		"DeviceID":  "12345",
	}
	ctx := setLogFields(nil, fields)
	fields = getLogFields(ctx)
	if fields["RequestID"] == nil {
		t.Error("Expected fields.CSIRequestID to be initialized")
	}
	fields = getLogFields(nil)
	if fields == nil {
		t.Error("Expected fields to be initialized")
	}
	fields = getLogFields(context.Background())
	if fields == nil {
		t.Error("Expected fields to be initialized")
	}
}

func TestEsnureISCSIDaemonIsStarted(t *testing.T) {
	s.dBusConn = &mockDbusConnection{}
	// Return a ListUnit mock response without ISCSId unit
	mockgosystemdInducedErrors.ListUnitISCSIDNotPresentError = true
	errMsg := fmt.Sprintf("failed to find iscsid.service. Going to panic")
	assert.PanicsWithError(t, errMsg, func() { s.ensureISCSIDaemonStarted() })
	mockgosystemdReset()
	s.dBusConn = &mockDbusConnection{}
	// Set the Daemon to inactive in mock response
	mockgosystemdInducedErrors.ISCSIDInactiveError = true
	mockgosystemdInducedErrors.StartUnitMaskedError = true
	errMsg = fmt.Sprintf("mock - unit is masked - failed to start the unit")
	assert.PanicsWithError(t, errMsg, func() { s.ensureISCSIDaemonStarted() })
}
