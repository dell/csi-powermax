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
	"fmt"
	"github.com/coreos/go-systemd/dbus"
	"time"
)

var mockgosystemdInducedErrors struct {
	ListUnitsError                bool
	ListUnitISCSIDNotPresentError bool
	ISCSIDInactiveError           bool
	StartUnitError                bool
	StartUnitMaskedError          bool
	JobFailure                    bool
}

func mockgosystemdReset() {
	mockgosystemdInducedErrors.ListUnitsError = false
	mockgosystemdInducedErrors.ListUnitISCSIDNotPresentError = false
	mockgosystemdInducedErrors.ISCSIDInactiveError = false
	mockgosystemdInducedErrors.StartUnitError = false
	mockgosystemdInducedErrors.StartUnitMaskedError = false
	mockgosystemdInducedErrors.JobFailure = false
}

type mockDbusConnection struct {
}

func (c *mockDbusConnection) Close() {
	// Do nothing
}

func (c *mockDbusConnection) ListUnits() ([]dbus.UnitStatus, error) {
	units := make([]dbus.UnitStatus, 0)
	if mockgosystemdInducedErrors.ListUnitsError {
		return units, fmt.Errorf("mock - failed to list the units")
	}
	if mockgosystemdInducedErrors.ListUnitISCSIDNotPresentError {
		return units, nil
	}
	iscsidStatus := dbus.UnitStatus{Name: "iscsid.service", ActiveState: "active"}
	if mockgosystemdInducedErrors.ISCSIDInactiveError {
		iscsidStatus.ActiveState = "inactive"
		units = append(units, iscsidStatus)
		return units, nil
	}
	units = append(units, iscsidStatus)
	return units, nil
}

func (c *mockDbusConnection) StartUnit(name string, mode string, ch chan<- string) (int, error) {
	if mockgosystemdInducedErrors.StartUnitError {
		fmt.Println("Induced start unit error")
		return 0, fmt.Errorf("mock - failed to start the unit")
	}
	if mockgosystemdInducedErrors.StartUnitMaskedError {
		return 0, fmt.Errorf("mock - unit is masked - failed to start the unit")
	}
	go responseChannel(ch)
	return 0, nil
}

func responseChannel(ch chan<- string) {
	time.Sleep(100 * time.Millisecond)
	if mockgosystemdInducedErrors.JobFailure {
		ch <- "mock - job to start the unit failed"
		return
	}
	ch <- "done"
}
