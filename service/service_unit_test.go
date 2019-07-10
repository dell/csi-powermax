package service

import (
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/stretchr/testify/assert"
)

const (
	numOfCylindersForDefaultSize = 547
)

var s service
var mockedExitStatus = 0
var mockedStdout string

func TestDeletionQueue(t *testing.T) {
	if delWorker == nil {
		delWorker = new(deletionWorker)
	}
	cnt := 100
	for i := 0; i < cnt; i++ {
		var req deletionWorkerRequest
		req.volumeID = fmt.Sprintf("ID %d", i)
		req.volumeName = fmt.Sprintf("Name %d", i)
		req.volumeSizeInCylinders = int64(rand.Int31() / 100)
		delWorker.requestDeletion(&req)
		req2 := req
		req2.volumeName = req.volumeName + "X"
		delWorker.requestDeletion(&req2)
	}
	prev := int64(0)
	for i := 0; i < cnt; i++ {
		req := delWorker.removeItem()
		//fmt.Printf("%d %d %s %s\n", req.volumeSizeInCylinders, req.priority, req.volumeID, req.volumeName)
		if req.priority < prev {
			t.Error(fmt.Sprintf("expected %d to be smaller than %d", req.priority, prev))
		}
		prev = req.priority
	}
}

var counters = [60]int{}

func incrementCounter(identifier string, num int) {
	AcquireLock(identifier)
	counters[num]++
	ReleaseLock(identifier)
}

func TestLocks(t *testing.T) {
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	for i := 0; i < 60; i++ {
		sgname := "sg" + strconv.Itoa(i)
		go incrementCounter(sgname, i)
	}
	fmt.Println("Sleeping for 15 seconds")
	time.Sleep(15 * time.Second)
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
			num, err := validateVolSize(tt.cr)
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
	volumeNameT, symIDT, devIDT, err := s.parseCSIVolumeID(csiDeviceID)
	if err != nil {
		t.Error()
		t.Error(err.Error())
	}
	volumeName = fmt.Sprintf("csi-%s-%s", volumePrefix, volumeName)
	if volumeNameT != volumeName ||
		symIDT != symID || devIDT != devID {
		t.Error("createCSIVolumeID and parseCSIVolumeID doesn't match")
	}
	// Test for empty device id
	_, _, _, err = s.parseCSIVolumeID("")
	if err == nil {
		t.Error("Expected an error while parsing empty ID but recieved success")
	}
	// Test for malformed device id
	malformedCSIDeviceID := "Vol1-Test"
	volumeNameT, symIDT, devIDT, err = s.parseCSIVolumeID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
	malformedCSIDeviceID = "-vol1-Test"
	_, _, _, err = s.parseCSIVolumeID(malformedCSIDeviceID)
	if err == nil {
		t.Error("Expected an error while parsing malformed ID but recieved success")
	}
}

func TestStringSliceComparison(t *testing.T) {

	valA := []string{"a", "b", "c"}
	valB := []string{"c", "b", "a"}
	valC := []string{"a", "b"}
	valD := []string{"a", "b", "d"}

	if !s.stringSlicesEqual(valA, valB) {
		t.Error("Could not validate that reversed slices are equal")
	}
	if s.stringSlicesEqual(valA, valC) {
		t.Error("Could not validate that slices of different sizes are different")
	}
	if s.stringSlicesEqual(valA, valD) {
		t.Error("Could not validate that slices of different content are different")
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
