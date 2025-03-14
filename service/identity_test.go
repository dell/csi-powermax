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

package service

import (
	"context"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	commonext "github.com/dell/dell-csi-extensions/common"
	"github.com/stretchr/testify/assert"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestProbe(t *testing.T) {
	testCases := []struct {
		name      string
		myService *service
		err       error
	}{
		{
			name: "Failed: Node Probe",
			myService: &service{
				mode: "node",
				opts: Opts{
					NodeName: "",
				},
			},
			err: status.Errorf(codes.FailedPrecondition, "Error getting NodeName from the environment"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.myService.Probe(context.Background(), &csi.ProbeRequest{})
			assert.Equal(t, tc.err, err)
		})
	}
}

func TestProbeController(t *testing.T) {
	testCases := []struct {
		name      string
		myService *service
		err       error
	}{
		{
			name: "Failed: Controller Probe - missing endpoint",
			myService: &service{
				mode: "controller",
				opts: Opts{},
			},
			err: status.Error(codes.FailedPrecondition, "missing Unisphere endpoint"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.myService.ProbeController(context.Background(), &commonext.ProbeControllerRequest{})
			assert.Equal(t, tc.err, err)
		})
	}
}
