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

	"github.com/dell/gobrick"
	"github.com/dell/gofsutil"
)

var mockGobrickInducedErrors struct {
	ConnectVolumeError    bool
	DisconnectVolumeError bool
}

func mockGobrickReset() {
	mockGobrickInducedErrors.ConnectVolumeError = false
	mockGobrickInducedErrors.DisconnectVolumeError = false
}

type mockFCGobrick struct {
}

func (g *mockFCGobrick) ConnectVolume(ctx context.Context, info gobrick.FCVolumeInfo) (gobrick.Device, error) {
	dev := gobrick.Device{
		WWN:         nodePublishWWN,
		Name:        goodVolumeName,
		MultipathID: "mpatha",
	}
	if len(info.Targets) < 1 {
		return dev, fmt.Errorf("No targets specified")
	}
	if info.Lun < 1 {
		return dev, fmt.Errorf("Invalid LUN")
	}
	if mockGobrickInducedErrors.ConnectVolumeError {
		return dev, fmt.Errorf("induced ConnectVolumeError")
	}
	gofsutil.GOFSMockWWNToDevice[nodePublishWWN] = nodePublishBlockDevicePath
	return dev, nil
}

func (g *mockFCGobrick) DisconnectVolumeByDeviceName(ctx context.Context, name string) error {
	if mockGobrickInducedErrors.DisconnectVolumeError {
		return fmt.Errorf("induced DisconnectVolumeError")
	}
	delete(gofsutil.GOFSMockWWNToDevice, nodePublishWWN)

	return nil
}

func (g *mockFCGobrick) GetInitiatorPorts(ctx context.Context) ([]string, error) {
	result := make([]string, 0)
	return result, nil
}

type mockISCSIGobrick struct {
}

func (g *mockISCSIGobrick) ConnectVolume(ctx context.Context, info gobrick.ISCSIVolumeInfo) (gobrick.Device, error) {
	dev := gobrick.Device{
		WWN:         nodePublishWWN,
		Name:        goodVolumeName,
		MultipathID: "mpatha",
	}
	if len(info.Targets) < 1 {
		return dev, fmt.Errorf("No targets specified")
	}
	if info.Lun < 1 {
		return dev, fmt.Errorf("Invalid LUN")
	}
	if mockGobrickInducedErrors.ConnectVolumeError {
		return dev, fmt.Errorf("induced ConnectVolumeError")
	}
	logger := &customLogger{}
	logger.Debug(ctx, "Adding WWN %s to path %s", nodePublishWWN, nodePublishBlockDevicePath)
	logger.Info(ctx, "Adding WWN %s to path %s", nodePublishWWN, nodePublishBlockDevicePath)
	gofsutil.GOFSMockWWNToDevice[nodePublishWWN] = nodePublishBlockDevicePath
	return dev, nil
}

func (g *mockISCSIGobrick) DisconnectVolumeByDeviceName(ctx context.Context, name string) error {
	if mockGobrickInducedErrors.DisconnectVolumeError {
		return fmt.Errorf("induced DisconnectVolumeError")
	}
	logger := &customLogger{}
	logger.Error(ctx, "Removing WWN %s to path entry", nodePublishWWN)
	logger.Info(ctx, "Removing WWN %s to path entry", nodePublishWWN)
	delete(gofsutil.GOFSMockWWNToDevice, nodePublishWWN)
	return nil
}

func (g *mockISCSIGobrick) GetInitiatorName(ctx context.Context) ([]string, error) {
	result := make([]string, 0)
	return result, nil
}
