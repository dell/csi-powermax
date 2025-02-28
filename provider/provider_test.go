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

package provider

import (
	"os"
	"testing"

	"github.com/dell/csi-powermax/v2/service"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	tests := []struct {
		name              string
		envGrpcMaxThreads string
	}{
		{
			name:              "Default max concurrent streams",
			envGrpcMaxThreads: "",
		},
		{
			name:              "Custom max concurrent streams",
			envGrpcMaxThreads: "8",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set the environment variable for the test
			if tt.envGrpcMaxThreads != "" {
				os.Setenv(service.EnvGrpcMaxThreads, tt.envGrpcMaxThreads)
				defer os.Unsetenv(service.EnvGrpcMaxThreads)
			}

			// Call the New function and validate it is not nil
			p := New()
			assert.NotNil(t, p)
		})
	}
}
