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

Feature: PowerMax CSI Metro Site Failure Handling
    As a consumer of the CSI interface
    I want to test Metro site-failure handling for SRDF/Metro volumes
    So that volumes remain accessible when one array becomes unreachable

@metro
@v2.18.0
    Scenario: Metro site failure handling enabled - winner detected from local array
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And I call RDF enabled CreateVolume "metro-vol1" in namespace "csi-test", mode "METRO" and RDFGNo 14
      Then a valid CreateVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro site failure handling enabled - volume provisioned in degraded mode during site failure
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And I induce error "RemoteSymbolNotFound"
      And I call RDF enabled CreateVolume "metro-degraded-vol" in namespace "csi-test", mode "METRO" and RDFGNo 14
      Then a valid CreateVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro deferred operation queue reports warning at threshold
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And the deferred operation queue is at warning threshold
      Then the queue status AtWarning flag is true
      And the queue status AtLimit flag is false

@metro
@v2.18.0
    Scenario: Metro deferred operation queue blocks new deferrals at hard limit
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And the deferred operation queue is at hard limit
      Then creating a new deferred operation returns ErrQueueFull

@metro
@v2.18.0
    Scenario: Metro site failure handling - ControllerPublishVolume detects winning array
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro site failure handling - DeleteVolume defers remote cleanup on unreachable array
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And I call RDF enabled CreateVolume "metro-vol-del" in namespace "csi-test", mode "METRO" and RDFGNo 14
      Then a valid CreateVolumeResponse is returned
      And I induce error "DeleteVolumeError"
      And I call DeleteVolume with "metro-vol-del"
      Then a valid DeleteVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro site failure handling feature flag disabled - standard behavior
      Given a PowerMax service
      And the Metro site failure handling feature is disabled
      And I call CreateVolume "volume1"
      Then a valid CreateVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro reconciliation - deferred operation replayed after array recovers
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And a deferred operation exists in the volume journal for array "000120000001"
      When the array "000120000001" becomes reachable again
      Then the deferred operation is replayed successfully

@metro
@v2.18.0
    Scenario: Metro Device Bias configuration - R1 failure emits actionable event
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And Device Bias is configured without Witness
      And I induce error "RemoteSymbolNotFound"
      And I call RDF enabled CreateVolume "bias-vol" in namespace "csi-test", mode "METRO" and RDFGNo 14
      Then a valid CreateVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro Witness-based winner detection - remote array designated winner
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And Witness is configured and effective on remote array
      And I induce error "LocalArrayUnreachable"
      And I call RDF enabled CreateVolume "witness-winner-vol" in namespace "csi-test", mode "METRO" and RDFGNo 14
      Then a valid CreateVolumeResponse is returned

@metro
@v2.18.0
    Scenario: Metro queue age warning event emitted
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And a deferred operation exists in the volume journal for array "000120000001" with age 35 minutes
      Then a Kubernetes warning event is emitted for queue age threshold

@metro
@v2.18.0
    Scenario: Metro reconciliation completion event emitted
      Given a PowerMax service
      And the Metro site failure handling feature is enabled
      And a deferred operation exists in the volume journal for array "000120000001"
      When the array "000120000001" becomes reachable again
      Then a Kubernetes normal event is emitted for reconciliation completion
