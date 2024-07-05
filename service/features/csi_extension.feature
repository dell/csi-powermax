Feature: PowerMax CSI interface
  As a consumer of the CSI interface
  I want to test csi extensions apis
  So that they are known to work

  # add @resiliency tag in service_test to run all tests
  @resiliency
  @v2.11.0
  Scenario: Call ValidateVolumeHostConnectivity for implementation
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call ValidateVolumeHostConnectivity
    Then the ValidateVolumeHost message contains "ValidateVolumeHostConnectivity is implemented"

  @resiliency
  @v2.11.0
  Scenario: Call ValidateVolumeHostConnectivity for NodeID with no symID with no service
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call ValidateVolumeHostConnectivity with "node1-127.0.0.2" and symID "none"
    Then no error was received
    And the ValidateVolumeHost message contains "connectivity unknown for array"

  @resiliency
  @v2.11.0
  Scenario Outline: Call ValidateVolumeHostConnectivity with various errors
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call ValidateVolumeHostConnectivity with <nodeID> and symID <symID>
    Then the error contains <errormsg>
    Examples:
      | nodeID    | symID         | induced            | errormsg                     |
      | "node1"   | "default"     | "none"             | "failed to parse node ID"    |
      | "no-node" | "default"     | "none"             | "NodeID is a required field" |
      | "node1"   | "fromVolID"   | "none"             | "failed to parse node ID"    |
      | "node1"   | "fromVolID"   | "InvalidVolumeID"  | "failed to parse node ID"    |


  @resiliency
  Scenario: Call ValidateVolumeHostConnectivity with a connected node
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I induce error "GetFreshMetrics"
    And I start node API server
    When I call ValidateVolumeHostConnectivity with "connected-node" and symID "default"
    Then no error was received
    And the ValidateVolumeHost message contains "connected to node"
    Then I call ValidateVolumeHostConnectivity with "connected-node-faultyVolID" and symID "default"
    Then the error contains "invalid symID"


  @resiliency
  @v2.11.0
  Scenario Outline: call IsIOInProgress with block volume and different errors
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I induce error <induced>
    When I call IsIOInProgress
    Then the error contains <error>
    Examples:
      | induced                   | error              |
      | "none"                    | "no IOInProgress"  |
      | "InvalidSymID"            | "not found"        |
      | "GetArrayPerfKeyError"    | "getting keys"     |
      | "GetFreshMetrics"         | "none"             |

  @resiliency
  @v2.11.0
  Scenario Outline: call IsIOInProgress with file system and different errors
    Given a PowerMax service
    And I call fileSystem CreateVolume "volume1"
    Then a valid CreateVolumeResponse is returned
    And I induce error <induced>
    When I call IsIOInProgress
    Then the error contains <error>
    Examples:
      | induced                   | error             |
      | "none"                    | "no IOInProgress" |

  @resiliency
  @v2.11.0
  Scenario: call IsIOInProgress and get fileSystem metric
    Given a PowerMax service
    And I call fileSystem CreateVolume "volume1"
    Then a valid CreateVolumeResponse is returned
    And I induce error "GetVolumesMetricsError"
    When I call IsIOInProgress
    Then the error contains "no IOInProgress"

  @resiliency
  @v2.11.0
  Scenario: call IsIOInProgress and get Metric error
    Given a PowerMax service
    And I call fileSystem CreateVolume "volume1"
    Then a valid CreateVolumeResponse is returned
    And I induce error "GetFileSysMetricsError"
    And I induce error "GetVolumesMetricsError"
    When I call IsIOInProgress
    Then the error contains "error"      
