Feature: Powermax OS CSI interface
  As a consumer of the CSI interface
  I want to run a system test
  So that I know the service functions correctly.

@v1.0.0
  Scenario: Create and delete basic thick large volume
    Given a Powermax service
    And a basic block volume request "integration1" "65536"
    And I use thick provisioning
    When I call CreateVolume
    And I receive a valid volume
    #   When I call ListVolume
    #	Then a valid ListVolumeResponse is returned
    And when I call DeleteVolume
    Then there are no errors
    And there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Create and delete basic volume using Gold Service Level
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    And an alternate ServiceLevel "Gold"
    When I call CreateVolume
    And I receive a valid volume
    #   When I call ListVolume
    #	Then a valid ListVolumeResponse is returned
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.1.0
  Scenario Outline: Create and delete basic volume using various volume sizes
    Given a Powermax service
    And a basic block volume request "integration11" <sizeMB>
    When I call CreateVolume
    And the volume size is <expectedGB> 
    #   When I call ListVolume
    #	Then a valid ListVolumeResponse is returned
    And when I call DeleteVolume
    Then the error message should contain <errormsg>
    And all volumes are deleted successfully

    Examples:
    | sizeMB       | expectedGB    | errormsg                                        |
    | "0"          | "1.00"        | "none"                                          |
    | "8192"       | "8.00"        | "none"                                          |
# 2 TB
    | "2097152"    | "2048.00"     | "none"                                          |
    | "100000000"  | "0.00"        | "greater than the maximum available capacity"  |

@v1.0.0
# This test checks an important DL scenario, that to delete a volume 
# it must match not only the symmetrix ID and device ID, but also the volume name.
  Scenario: Create volume, try to delete it with incorrect CSI VolumeID, then delete it.
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    And an alternate ServiceLevel "Gold"
    When I call CreateVolume
    #   When I call ListVolume
    #	Then a valid ListVolumeResponse is returned
    And I munge the CSI VolumeId
    And when I call DeleteVolume
    Then the volume is not deleted
    And I unmunge the CSI VolumeId
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Idempotent create and delete basic volume
    Given a Powermax service
    And a basic block volume request "integration2" "50"
    When I call CreateVolume
    And I call CreateVolume
    And I receive a valid volume
    And when I call DeleteVolume
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Create and delete mount volume
    Given a Powermax service
    And a mount volume request "integration5"
    When I call CreateVolume
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Create, publish, unpublish and delete basic volume
    Given a Powermax service
    And a basic block volume request "integration5" "100"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Create publish, node publish, node unpublish, unpublish, delete basic volume
    Given a Powermax service
    And a mount volume request "integration5"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.1.0
  Scenario: Create publish, node stage, node publish, node unpublish, node unstage, unpublish, delete basic volume
    Given a Powermax service
    And a mount volume request "integration6"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call NodeStageVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call NodeUnstageVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

@v1.0.0
  Scenario: Idempotent create publish, node publish, node unpublish, unpublish, delete basic volume
    Given a Powermax service
    And an idempotent test
    And a mount volume request "integration5"
    When I call CreateVolume
    And there are no errors
    And I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully
	
@v1.0.0
  Scenario: Create and delete basic volume to maximum capacity
    Given a Powermax service
    And max retries 1
    And a basic block volume request "integration4" "1048576"
    When I call CreateVolume
    And I receive a valid volume
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully
	
@v1.0.0
  Scenario: Create and delete basic volume beyond maximum capacity
    Given a Powermax service
    And max retries 1
    And a basic block volume request "integration4" "1073741824"
    When I call CreateVolume
    Then the error message should contain "OutOfRange desc = bad capacity"
	
@v1.0.0
  Scenario: Create and delete basic volume of minimum capacity
    Given a Powermax service
    And max retries 1
    And a basic block volume request "integration4" "50"
    When I call CreateVolume
    And I receive a valid volume
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully
	
@v1.0.0
    Scenario: Create and delete basic volume below minimum capacity
    Given a Powermax service
    And max retries 1
    And a basic block volume request "integration4" "48"
    When I call CreateVolume
    Then the error message should contain "OutOfRange desc = bad capacity"
	
@v1.0.0
  Scenario: Call GetCapacity 
    Given a Powermax service
    And a get capacity request "SRP_1"
    And I call GetCapacity
    Then a valid GetCapacityResponse is returned

  Scenario: Create volume, create snapshot, create volume from snapshot, delete original volume, delete new volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And I call CreateSnapshot
    And there are no errors
    And I call ListVolume
    And a valid ListVolumeResponse is returned
    And I call ListSnapshot
    And a valid ListSnapshotResponse is returned
    And I call CreateVolumeFromSnapshot
    Then there are no errors
    And I call ListVolume
    And a valid ListVolumeResponse is returned
    And I call DeleteSnapshot
    And there are no errors
    And when I call DeleteVolume
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors
    And I call ListVolume

  Scenario: Create volume, create snapshot, create many volumes from snap, delete original volume, delete new volumes
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I call CreateManyVolumesFromSnapshot
    Then the error message should contain "too many volumes in the VTree"
    And I call DeleteSnapshot
    And when I call DeleteVolume
    And when I call DeleteAllVolumes
    

  Scenario: Create volume, idempotent create snapshot, delete volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And I call CreateSnapshot
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I call DeleteSnapshot
    And there are no errors
    And I call DeleteSnapshot
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors

  Scenario: Create multiple volumes, create snapshot of consistency group, delete volumes
    Given a Powermax service
    And a basic block volume request "integration1" "8"
    When I call CreateVolume
    And a basic block volume request "integration2" "8"
    And I call CreateVolume
    And a basic block volume request "integration3" "8"
    And I call CreateVolume
    And I call CreateSnapshotConsistencyGroup
    And there are no errors
    Then I call DeleteSnapshot
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  Scenario Outline: Create publish, node-publish, node-unpublish, unpublish, and delete basic volume
    Given a Powermax service
    And a capability with voltype <voltype> access <access> fstype <fstype>
    And a volume request "integration5" "8"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "nodeID"
    And when I call NodePublishVolume "nodeID"
    And verify published volume with voltype <voltype> access <access> fstype <fstype>
    And when I call NodePublishVolume "nodeID"
    And when I call NodeUnpublishVolume "nodeID"
    And when I call UnpublishVolume "nodeID"
    And when I call DeleteVolume
    Then the error message should contain <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |
    | "mount"      | "single-writer"                | "ext4"     | "none"                                       |
    | "mount"      | "multi-writer"                 | "ext4"     | "multi-writer not allowed"                   |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "block"      | "multi-writer"                 | "none"     | "none"                                       |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    

  Scenario: Create publish, unpublish, and delete basic volume
    Given a Powermax service
    And a basic block volume request "integration5" "8"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "nodeID"
    And there are no errors
    And when I call UnpublishVolume "nodeID"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors

  Scenario: Multi-host create publish, unpublish, and delete basic volume
    Given a Powermax service
    And a basic block volume request "integration6" "8"
    And access type is "multi-writer"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "nodeID"
    And there are no errors
    And when I call PublishVolume "ALT_GUID"
    And there are no errors
    And when I call UnpublishVolume "nodeID"
    And there are no errors
    And when I call UnpublishVolume "ALT_GUID"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors

  Scenario: Create and delete basic 100000G volume
    Given a Powermax service
    And max retries 1
    And a basic block volume request "integration4" "100000"
    When I call CreateVolume
    And when I call DeleteVolume
    Then the error message should contain "The capacity of the devices in this Storage Pool does not allow for this amount of thin capacity allocation"

  Scenario: Create and delete basic 96G volume
    Given a Powermax service
    And max retries 10
    And a basic block volume request "integration3" "96"
    When I call CreateVolume
    And when I call DeleteVolume
    Then there are no errors

@v1.0.0
  Scenario Outline: Scalability test to create volumes, publish, node publish, node unpublish, unpublish, delete volumes in parallel
    Given a Powermax service
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And when I delete <numberOfVolumes> volumes in parallel
    Then there are no errors
    And all volumes are deleted successfully

    Examples:
    | numberOfVolumes |
    | 1               |
    | 2               |
    | 5               |
    | 8               |
   # | 10              |
   # | 20              |
   # | 50              |
   # | 100             |
   # | 200             |

@v1.1.0
  Scenario Outline: Scalability test to create volumes, publish, node stage, node publish, node unpublish, node unstage,  unpublish, delete volumes in parallel
    Given a Powermax service
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node stage <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unstage <numberOfVolumes> volumes in parallel
    And there are no errors
    And I unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And when I delete <numberOfVolumes> volumes in parallel
    Then there are no errors
    And all volumes are deleted successfully

    Examples:
    | numberOfVolumes |
    | 1               |
    | 2               |
    | 5               |
    | 8               |
   # | 10              |
   # | 20              |
   # | 50              |
   # | 100             |
   # | 200             |

@v1.0.0
  Scenario Outline: Idempotent create volumes, publish, node publish, node unpublish, unpublish, delete volumes in parallel
    Given a Powermax service
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And when I delete <numberOfVolumes> volumes in parallel
    And there are no errors
    And when I delete <numberOfVolumes> volumes in parallel
    Then there are no errors

    Examples:
    | numberOfVolumes |
    | 1               |
    #| 10               |
    #| 20               |

@v1.0.0
  Scenario Outline: Create publish, node-publish, node-unpublish, unpublish, and delete basic volume
    Given a Powermax service
    And a volume request with file system "integration5" fstype <fstype> access <access> voltype <voltype>
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
   
    Examples:
    | fstype | access          | voltype |
    | "xfs"  | "single-writer" | "mount" |
    | "ext4" | "single-writer" | "mount" |
