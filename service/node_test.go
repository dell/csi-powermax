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
	"testing"
)

func TestReachableEndPoint(t *testing.T) {
	type args struct {
		endpoint string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{"Unreachable IP", args{endpoint: "10.255.1.2:100"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := s.reachableEndPoint(tt.args.endpoint); got != tt.want {
				t.Errorf("reachableEndPoint() = %v, want %v", got, tt.want)
			}
		})
	}
}
