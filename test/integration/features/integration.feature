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
      | sizeMB      | expectedGB | errormsg                                      |
      | "0"         | "1.00"     | "none"                                        |
      | "8192"      | "8.00"     | "none"                                        |
# 2 TB
      | "2097152"   | "2048.00"  | "none"                                        |
      | "100000000" | "0.00"     | "The device size specified exceeds the maximum allowed" |

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

  @v1.1.0
  @xwip
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
  @xwip
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
    And when I call NodeStageVolume "Node1"
    And there are no errors
    And when I call NodeStageVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call NodeUnpublishVolume "Node1"
    And there are no errors
    And when I call NodeUnstageVolume "Node1"
    And there are no errors
    And when I call NodeUnstageVolume "Node1"
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
    And I call LinkVolumeToSnapshot
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
    And when I call NodeStageVolume "nodeID"
    And when I call NodePublishVolume "nodeID"
    And verify published volume with voltype <voltype> access <access> fstype <fstype>
    And when I call NodePublishVolume "nodeID"
    And when I call NodeUnstageVolume "nodeID"
    And when I call NodeUnpublishVolume "nodeID"
    And when I call UnpublishVolume "nodeID"
    And when I call DeleteVolume
    Then the error message should contain <errormsg>

    Examples:
      | voltype | access                      | fstype | errormsg                   |
      | "mount" | "single-writer"             | "xfs"  | "none"                     |
      | "mount" | "single-writer"             | "ext4" | "none"                     |
      | "mount" | "multi-writer"              | "ext4" | "multi-writer not allowed" |
      | "block" | "single-writer"             | "none" | "none"                     |
      | "block" | "multi-writer"              | "none" | "none"                     |
      | "block" | "single-writer"             | "none" | "none"                     |
      | "mount" | "single-node-single-writer" | "xfs"  | "none"                     |
      | "mount" | "single-node-single-writer" | "ext4" | "none"                     |
      | "block" | "single-node-single-writer" | "none" | "none"                     |


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
    @xwip
  Scenario Outline: Scalability test to create volumes, publish, node publish, node unpublish, unpublish, delete volumes in parallel
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

  @v1.1.0
    @xwip
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
  Scenario Outline: Idempotent create volumes, publish, node stage, node publish, node unpublish, node unstage, unpublish, delete volumes in parallel
    Given a Powermax service
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    When I create <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node stage <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node stage <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node publish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unpublish <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unstage <numberOfVolumes> volumes in parallel
    And there are no errors
    And I node unstage <numberOfVolumes> volumes in parallel
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

    Examples:
      | fstype | access          | voltype |
      | "xfs"  | "single-writer" | "mount" |
      | "ext4" | "single-writer" | "mount" |


  @v1.2.0
  Scenario: Create volume, create snapshot, create volume from snapshot, create volume from volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I call LinkVolumeToSnapshot
    Then there are no errors
    And I call LinkVolumeToVolume
    And there are no errors
    Then I call DeleteSnapshot
    And there are no errors
    And when I call DeleteVolume
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Create 'n' snapshots from a volume in parallel
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I create 10 snapshots in parallel
    And there are no errors
    Then I call DeleteAllSnapshots
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Create 'n' volumes from snapshot in parallel
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I create 10 volumes from snapshot in parallel
    And there are no errors
    Then I call DeleteSnapshot
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Create 'n' volumes from volume in parallel
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I create 4 volumes from volume in parallel
    And there are no errors
    Then when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Delete snapshot
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I call DeleteSnapshot
    And there are no errors
    Then when I call DeleteVolume
    And there are no errors


  @v1.2.0
  Scenario: Delete 'n' snapshots in parallel
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I create 10 snapshots in parallel
    And there are no errors
    Then I call DeleteSnapshot in parallel
    And there are no errors
    Then when I call DeleteVolume
    And there are no errors

  @v1.2.0
  Scenario: Delete Snapshot then the Volume having a snapshot
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I create 2 snapshots in parallel
    And there are no errors
    Then I call DeleteAllSnapshots
    And there are no errors
    Then when I call DeleteVolume
    And there are no errors

  @v1.2.0
  Scenario: Deleting and creating a snapshot concurrently on source and target volumes
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    And I call LinkVolumeToSnapshot
    And there are no errors
    Then I call DeleteSnapshot and CreateSnapshot in parallel
    And there are no errors
    And I call DeleteSnapshot
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Creating a Snapshot on newly created Volume from Snasphot
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    Then I call LinkVolumeToSnapshot
    And there are no errors
    And I call CreateSnapshot on new volume
    And there are no errors
    Then I call DeleteAllSnapshots
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Deleting Target Volume then the Source Volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    Then I call LinkVolumeToSnapshot
    And there are no errors
    Then I call LinkVolumeToSnapshot
    And there are no errors
    And I call DeleteLocalVolume
    And there are no errors
    Then I call DeleteSnapshot
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Creating a Volume from newly created Volume from Volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    Then I call LinkVolumeToVolume
    And there are no errors
    Then I call CreateVolume from new volume
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @v1.2.0
  Scenario: Checks if a soft deleted volume is hard deleted
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I create 2 snapshots in parallel
    And there are no errors
    And when I call DeleteVolume
    And there are no errors
    And I delete a snapshot
    And there are no errors
    Then I check if volume exist
    And there are no errors
    And I delete a snapshot
    And there are no errors
    Then I check if volume is deleted
    And there are no errors

  @wip
  @v1.4.0
  Scenario: Expand Mount Volume
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
    And when I call ExpandVolume to 100 cylinders
    And there are no errors
    And when I call NodeExpandVolume
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

  @v1.4.0
  Scenario: Expand Mount Volume with Capacity Bytes not set
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
    And when I call ExpandVolume to 0 cylinders
    Then the error message should contain "Invalid argument"
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And all volumes are deleted successfully

  @wip
  @v1.4.0
  Scenario: Expand Raw Block Volume
    Given a Powermax service
    And a mount volume request "integration6"
    And a basic block volume request "integration7" "100"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call NodeStageVolume "Node1"
    And there are no errors
    And when I call NodePublishVolume "Node1"
    And there are no errors
    And when I call ExpandVolume to 100 cylinders
    And there are no errors
    And when I call NodeExpandVolume
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

  @v1.4.0
  Scenario Outline: Create and Delete 'n' Snapshots from 'n' Volumes in parallel and retry for failing snapshot request
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I create <n> volumes in parallel
    And there are no errors
    And I create a snapshot per volume in parallel
    And there are no errors
    Then I call DeleteSnapshot in parallel
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

    Examples:
      | n  |
      | 10 |
      | 20 |
      | 35 |

  @v1.6.0
  Scenario: Create and delete replication volume
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I receive a valid volume
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v1.6.0
  Scenario: Idempotent create and delete replication volume
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I call CreateVolume
    And I receive a valid volume
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v1.6.0
  Scenario: Create, publish, unpublish and delete replication volume
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And there are no errors
    And when I call PublishVolume "Node1"
    And there are no errors
    And when I call UnpublishVolume "Node1"
    And there are no errors
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v1.6.0
  Scenario: Create and delete replication mount volume
    Given a Powermax service
    And a mount volume request "integration5"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And there are no errors
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v1.6.0
  Scenario: SRDF Actions on a protected SG
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I receive a valid volume
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call ExecuteAction with action "Failover"
    And there are no errors
    And I call ExecuteAction with action "Failback"
    And there are no errors
    And I call ExecuteAction with action "Suspend"
    And there are no errors
    And I call ExecuteAction with action "Resume"
    And there are no errors
    And I call ExecuteAction with action "Resume"
    And there are no errors
    And I call ExecuteAction with action "Suspend"
    And there are no errors
    And I call ExecuteAction with action "Establish"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v1.6.0
  Scenario: Get Status of a protected SG
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I receive a valid volume
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call ExecuteAction with action "Failover"
    And there are no errors
    And I call GetStorageProtectionGroupStatus to get "FAILEDOVER"
    And there are no errors
    And I call ExecuteAction with action "Failback"
    And there are no errors
    And I call GetStorageProtectionGroupStatus to get "SYNCHRONIZED"
    And there are no errors
    And I call ExecuteAction with action "Suspend"
    And there are no errors
    And I call GetStorageProtectionGroupStatus to get "SUSPENDED"
    And there are no errors
    And I call ExecuteAction with action "Resume"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v2.2.0
  Scenario: Volume Health Monitoring method
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
    And when I call ControllerGetVolume
    And there are no errors
    And when I call NodeGetVolumeStats
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

  @v2.4.0
  Scenario: Create and delete replication volume with auto SRDF group
    Given a Powermax service
    And a basic block volume request "integration1" "100"
    And adds auto SRDFG replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I receive a valid volume
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And when I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @v2.7.0
  Scenario: Create and delete replication volume with HostIOLimits
    Given a Powermax service
    And I have SetHostIOLimits on the storage group
    And a basic block volume request "integration1" "100"
    And adds replication capability with mode "ASYNC" namespace "INT"
    When I call CreateVolume
    And I receive a valid volume
    And I call CreateStorageProtectionGroup with mode "ASYNC"
    And there are no errors
    And I call CreateRemoteVolume with mode "ASYNC"
    And there are no errors
    And when I call DeleteVolume
    Then there are no errors
    And when I call DeleteLocalVolume
    Then there are no errors
    And all volumes are deleted successfully
    And I call Delete LocalStorageProtectionGroup
    And there are no errors
    And I call Delete RemoteStorageProtectionGroup
    And there are no errors

  @2.12.0
  Scenario: Creating a Volume from newly created Volume from Volume
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    Then I call LinkVolumeToVolume
    And there are no errors
    Then I call LinkVolumeToVolumeAgain
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors

  @2.12.0
  Scenario: Creating a Volume from newly created Spanshot
    Given a Powermax service
    And a basic block volume request "integration1" "50"
    When I call CreateVolume
    And there are no errors
    And I call CreateSnapshot
    And there are no errors
    Then I call LinkVolumeToSnapshot
    And there are no errors
    Then I call LinkVolumeToSnapshotAgain
    And there are no errors
    And when I call DeleteAllVolumes
    And there are no errors  
