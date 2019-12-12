package service

import (
	"fmt"
	"os/exec"
	"sync"

	log "github.com/sirupsen/logrus"
)

// Map - structure which provides a thread safe map of the type map[interface{}]interface{}
type Map struct {
	syncMap sync.Map
}

// LoadOrStore - Reads or stores a key/value pair from/into the map
func (m *Map) LoadOrStore(key string, value chan interface{}) chan interface{} {
	val, _ := m.syncMap.LoadOrStore(key, value)
	return val.(chan interface{})
}

// Load - Reads a value from the map based on the key
func (m *Map) Load(key string) chan interface{} {
	val, ok := m.syncMap.Load(key)
	if ok {
		return val.(chan interface{})
	}
	return nil
}

// Delete - Deletes the key/value pair from map based on the key
func (m *Map) Delete(key string) {
	m.syncMap.Delete(key)
}

// addLock - Adds the key/value pair if missing from the map
func addLock(identifier string) {
	ch := make(chan interface{}, 1)
	var i interface{}
	i = 1
	ch <- i
	locks.LoadOrStore(identifier, ch)
}

// AcquireLock - acquire a lock given an identifier string
func AcquireLock(identifier string, requestID string) {
	log.Debug(fmt.Sprintf("Attempting to acquire lock: %s for: %s", identifier, requestID))
	addLock(identifier)
	lockChannel := locks.Load(identifier)
	if lockChannel != nil {
		_, ok := <-lockChannel
		if !ok {
			AcquireLock(identifier, requestID)
		} else {
			log.Debug(fmt.Sprintf("Acquired lock lock: %s for: %s", identifier, requestID))
		}
	} else {
		AcquireLock(identifier, requestID)
	}
}

// ReleaseLock - release a lock given an identifier string
func ReleaseLock(identifier string, requestID string) {
	lockChannel := locks.Load(identifier)
	locks.Delete(identifier)
	log.Debug(fmt.Sprintf("Released lock: %s for: %s", identifier, requestID))
	close(lockChannel)
}

var execCommand = exec.Command

// locks is a sync map which holds named locks
var locks Map
