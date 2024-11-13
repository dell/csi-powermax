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

package symmetrix

import (
	"testing"

	pmax "github.com/dell/gopowermax/v2"
	types "github.com/dell/gopowermax/v2/types/v100"
)

func TestGetPowerMaxClient(t *testing.T) {
	c, err := pmax.NewClientWithArgs("/", "test", true, true)
	if err != nil {
		t.Fatalf("Faild to create a pmax client: %s", err.Error())
	}

	Initialize([]string{"0001", "0002"}, c)

	_, err = GetPowerMaxClient("0001")
	if err != nil {
		t.Errorf("Faied to create client with only primary managed array specified")
	}
	_, err = GetPowerMaxClient("0003")
	if err == nil {
		t.Errorf("Should have failed with only primary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0001", "0002")
	if err != nil {
		t.Errorf("Faied to create client with both arrays managed")
	}
	_, err = GetPowerMaxClient("0001", "0003")
	if err == nil {
		t.Errorf("Should have failed with secondary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0003", "0002")
	if err == nil {
		t.Errorf("Should have failed with primary unmanaged array specified")
	}
	_, err = GetPowerMaxClient("0003", "0004")
	if err == nil {
		t.Errorf("Should have failed with none of the arrays managed")
	}
}

// ctx context.Context, client pmax.Pmax, symID string
func TestUpdate(t *testing.T) {
	symmetrixCapability := &types.SymmetrixCapability{
		SymmetrixID: "fakeSymmetrix",
		SnapVxCapable: true,
		RdfCapable: true,
	}
	rep := ReplicationCapabilitiesCache{}
        rep.update(symmetrixCapability)
        if rep.cap.SymmetrixID == "fakeSymmetrix" && rep.cap.SnapVxCapable && rep.cap.RdfCapable {
                t.Errorf("update call failed -- capability not set")
        }
}

