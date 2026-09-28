Feature: Port Group CSI Prefix Filtering
  As a CSI driver
  I want to filter port groups by CSI prefix
  So that I only use driver-managed port groups and avoid conflicts with manually created port groups

  Background:
    Given a PowerMax array with ID "000197900111"
    And the cluster prefix is "test-cluster"
    And a Fibre Channel host "csi-node-host1" logged into ports "FA-1D:4,FA-2D:4"

  Scenario: V4 array selects CSI-managed port group and ignores manual port groups
    Given the array is running PowerMax OS 10 with API version "103"
    And the following port groups exist on the array:
      | Port Group ID                          | Type       | Ports              |
      | manual-pg-production                   | non-CSI    | FA-1D:4,FA-2D:4    |
      | csi-test-cluster-FA-1D-4-FA-2D-4-PG   | CSI-managed| FA-1D:4,FA-2D:4    |
      | admin-pg-backup                        | non-CSI    | FA-1D:4,FA-2D:4    |
    When the driver selects a port group for the host
    Then the selected port group should be "csi-test-cluster-FA-1D-4-FA-2D-4-PG"
    And the driver should log "Found valid port group csi-test-cluster-FA-1D-4-FA-2D-4-PG"
    And manual port groups should be ignored

  Scenario: Legacy array selects CSI-managed port group and ignores manual port groups
    Given the array is running Hypermax OS with API version "102"
    And the following port groups exist on the array:
      | Port Group ID                          | Type       | Ports              |
      | manual-pg-production                   | non-CSI    | FA-1D:4,FA-2D:4    |
      | csi-test-cluster-FA-1D-4-FA-2D-4-PG   | CSI-managed| FA-1D:4,FA-2D:4    |
    When the driver selects a port group for the host
    Then the selected port group should be "csi-test-cluster-FA-1D-4-FA-2D-4-PG"
    And the driver should log "Found valid port group csi-test-cluster-FA-1D-4-FA-2D-4-PG"

  Scenario: V4 array creates new CSI port group when no matching CSI port group exists
    Given the array is running PowerMax OS 10 with API version "103"
    And the following port groups exist on the array:
      | Port Group ID                    | Type       | Ports              |
      | manual-pg-1                      | non-CSI    | FA-1D:4,FA-2D:4    |
      | csi-test-cluster-FA-3D-5-PG     | CSI-managed| FA-3D:5            |
    When the driver selects a port group for the host
    Then a new port group should be created
    And the new port group name should contain "csi-test-cluster"
    And the new port group should have ports "FA-1D:4,FA-2D:4"
    And the driver should log "No port group found on the array. Attempting to create one"

  Scenario: V4 array with multiple CSI port groups selects the first match
    Given the array is running PowerMax OS 10 with API version "103"
    And the following port groups exist on the array:
      | Port Group ID                            | Type       | Ports              |
      | csi-test-cluster-FA-1D-4-FA-2D-4-PG-1   | CSI-managed| FA-1D:4,FA-2D:4    |
      | csi-test-cluster-FA-1D-4-FA-2D-4-PG-2   | CSI-managed| FA-1D:4,FA-2D:4    |
    When the driver selects a port group for the host
    Then the selected port group should be "csi-test-cluster-FA-1D-4-FA-2D-4-PG-1"
    And the second CSI port group should not be selected

  Scenario: Different cluster prefixes provide isolation
    Given the cluster prefix is "cluster-a"
    And the array is running PowerMax OS 10 with API version "103"
    And the following port groups exist on the array:
      | Port Group ID                | Type       | Ports       |
      | csi-cluster-a-FA-1D-4-PG    | CSI-managed| FA-1D:4     |
      | csi-cluster-b-FA-1D-4-PG    | CSI-managed| FA-1D:4     |
      | manual-pg                    | non-CSI    | FA-1D:4     |
    When the driver selects a port group for the host
    Then the selected port group should be "csi-cluster-a-FA-1D-4-PG"
    And port groups with different cluster prefixes should be ignored

  Scenario: Existing masking view reuses its current port group
    Given the array is running PowerMax OS 10 with API version "103"
    And a masking view "csi-mv-host1" exists with port group "csi-test-cluster-old-PG"
    And the following port groups exist on the array:
      | Port Group ID                | Type       | Ports              |
      | csi-test-cluster-old-PG     | CSI-managed| FA-1D:4,FA-2D:4    |
      | csi-test-cluster-new-PG     | CSI-managed| FA-1D:4,FA-2D:4    |
    When the driver publishes a volume to the existing masking view
    Then the existing port group "csi-test-cluster-old-PG" should be reused
    And the driver should log "Using existing PortGroup csi-test-cluster-old-PG from MaskingView"
    And no port group selection should occur

  Scenario: V4 array with only non-CSI port groups creates new CSI port group
    Given the array is running PowerMax OS 10 with API version "103"
    And the following port groups exist on the array:
      | Port Group ID      | Type    | Ports              |
      | manual-pg-1       | non-CSI | FA-1D:4,FA-2D:4    |
      | manual-pg-2       | non-CSI | FA-1D:4,FA-2D:4    |
      | manual-pg-3       | non-CSI | FA-1D:4,FA-2D:4    |
    When the driver selects a port group for the host
    Then a new port group should be created
    And the new port group name should contain "csi-test-cluster"
    And all manual port groups should be ignored

  Scenario: Port group filtering works with base64 encoded port IDs on V4
    Given the array is running PowerMax OS 10 with API version "103"
    And port IDs are base64 encoded in the format "DirectorID|PortID"
    And the following port groups exist on the array:
      | Port Group ID                          | Type       | Encoded Ports                    |
      | manual-pg                              | non-CSI    | RkEtMUQ6NA==,RkEtMkQ6NA==       |
      | csi-test-cluster-FA-1D-4-FA-2D-4-PG   | CSI-managed| RkEtMUQ6NA==,RkEtMkQ6NA==       |
    When the driver decodes the port IDs
    Then the decoded ports should be "FA-1D:4,FA-2D:4"
    And the CSI-managed port group should be selected
    And the manual port group should be ignored
