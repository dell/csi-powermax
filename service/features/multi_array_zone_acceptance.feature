# Copyright © 2026 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

@v2.17.0
Feature: Multi-array zone acceptance and capacity-based selection
    As an operator of the PowerMax CSI driver
    I want to configure multiple PowerMax arrays within the same availability zone
    So that capacity is pooled across arrays and provisioning survives a single array outage

  # FR-1: Multi-Array Zone Configuration & Acceptance
  @fr1 @ac001
  Scenario: Two arrays with identical zone label are accepted
    Given a Kubernetes Secret contains two PowerMax array entries with zone label "us-east-1a"
    When the driver parses the storage arrays secret
    Then both arrays are registered for zone "us-east-1a"
    And no error is raised

  @fr1
  Scenario: One invalid array entry does not block the remaining valid entry
    Given a Kubernetes Secret contains a valid array entry and a malformed array entry missing storagearrayid
    When the driver parses the storage arrays secret
    Then the malformed entry is skipped with an error identifying entry index 0
    And the valid array remains registered

  @fr1
  Scenario: Malformed secret entry that is not a map is skipped without panicking
    Given a Kubernetes Secret contains a non-map entry in the storagearrays list
    When the driver parses the storage arrays secret
    Then the driver does not panic
    And the malformed entry is skipped with an error identifying entry index 0

  # FR-2: Capacity-Based Array Selection & Health Checking
  @fr2 @ac001
  Scenario: PVC provisioned to the lowest-utilization array in a multi-array zone
    Given a zone "us-east-1a" contains array "000000000001" at 60 percent utilization
    And a zone "us-east-1a" contains array "000000000002" at 40 percent utilization
    When the driver selects an array for zone "us-east-1a" from topology requirements
    Then the selected array is "000000000002"

  @fr2 @ac003
  Scenario: Array marked unavailable is skipped in favor of the remaining array
    Given a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And array "000000000001" is marked unavailable
    And a zone "us-east-1a" contains array "000000000002" at 90 percent utilization
    When the driver selects an array for zone "us-east-1a" from topology requirements
    Then the selected array is "000000000002"

  @fr2 @ac003
  Scenario: Array recovers immediately on the next successful capacity poll
    Given a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And array "000000000001" is marked unavailable
    When array "000000000001" reports a successful capacity poll at 15 percent utilization
    Then array "000000000001" is available

  @fr2 @ac006
  Scenario: Distinct error when all arrays in a zone are unavailable
    Given a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And array "000000000001" is marked unavailable
    And a zone "us-east-1a" contains array "000000000002" at 20 percent utilization
    And array "000000000002" is marked unavailable
    When the driver selects an array for zone "us-east-1a" from topology requirements
    Then the driver returns an error containing "all arrays in zone us-east-1a are unavailable"
    And the error mentions 2 arrays attempted

  @fr2
  Scenario: Connectivity error is classified as eligible for failover
    Given a provisioning error "dial tcp 10.0.0.1:8443: connect: connection refused" is observed for array "000000000001"
    When the driver classifies the provisioning error
    Then the error is classified as a connectivity error

  @fr2
  Scenario: Non-connectivity error is not classified for failover
    Given a provisioning error "invalid ServiceLevel parameter" is observed for array "000000000001"
    When the driver classifies the provisioning error
    Then the error is not classified as a connectivity error

  @fr2 @ac007
  Scenario: StorageClass SYMID targets a specific array regardless of capacity
    Given a zone "us-east-1a" contains array "000000000001" at 90 percent utilization
    And a zone "us-east-1a" contains array "000000000002" at 10 percent utilization
    When a CreateVolume request specifies SYMID "000000000001" for zone "us-east-1a"
    Then the volume is provisioned on array "000000000001"

  @fr2 @ac007
  Scenario: StorageClass SYMID targeting an unavailable array returns an error without fallback
    Given a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And array "000000000001" is marked unavailable
    When a CreateVolume request specifies SYMID "000000000001" for zone "us-east-1a"
    Then the driver returns an error stating array "000000000001" is unavailable
    And no volume is provisioned on array "000000000002"

  # FR-3: Observable Per-Array Metrics
  @fr3
  Scenario: Selection latency is recorded after a selection in a multi-array zone
    Given multi-array metrics are enabled
    And a zone "us-east-1a" contains array "000000000001" at 60 percent utilization
    And a zone "us-east-1a" contains array "000000000002" at 40 percent utilization
    When the driver selects an array for zone "us-east-1a" from topology requirements
    Then metric "dell_csi_array_selection_latency_seconds" for zone "us-east-1a" has a positive count

  @fr3
  Scenario: Capacity and availability gauges reflect the cache after a poll refresh
    Given multi-array metrics are enabled
    And a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And array "000000000001" is marked unavailable
    And a zone "us-east-1a" contains array "000000000002" at 20 percent utilization
    When the driver refreshes capacity metrics for zone "us-east-1a"
    Then metric "dell_csi_array_available" for zone "us-east-1a" array "000000000001" equals 0
    And metric "dell_csi_array_available" for zone "us-east-1a" array "000000000002" equals 1
    And metric "dell_csi_array_capacity_utilization" for zone "us-east-1a" array "000000000002" equals 20

  @fr3
  Scenario: Adoption gauge counts zones configured with more than one array
    Given multi-array metrics are enabled
    And a zone "us-east-1a" contains array "000000000001" at 10 percent utilization
    And a zone "us-east-1a" contains array "000000000002" at 20 percent utilization
    And a zone "us-west-1a" contains array "000000000003" at 30 percent utilization
    When the driver recomputes the multi-array zone adoption gauge
    Then metric "dell_csi_multiarray_zones_total" equals 1

  # FR-4: Cross-Array Volume Access
  @fr4 @ac004
  Scenario: A volume is served from the array in its handle regardless of node preference
    Given a volume provisioned on array "000000000001" with device "00123"
    When a publish request for that volume arrives at a node preferring array "000000000002"
    Then the array resolved for publish is "000000000001"

  @fr4 @ac004
  Scenario: A malformed volume handle is rejected rather than resolved to an arbitrary array
    Given a malformed volume handle
    When the array is resolved from the volume handle
    Then resolving the array from the handle fails
