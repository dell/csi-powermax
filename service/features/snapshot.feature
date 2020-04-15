Feature: PowerMax CSI Interface
    As a consumer of the CSI interface
    I want to test snapshot interfaces
    So that they are know to work
@v1.2.0
    Scenario: Snapshot license
        Given a PowerMax service
        When I check the snapshot license
        And I reset the license cache
        Then no error was received
@v1.2.0
    Scenario: Snapshot license for unlicensed array
        Given a PowerMax service
        And I induce error "SnapshotNotLicensed"
        When I check the snapshot license
        And I reset the license cache
        Then the error contains "doesn't have Snapshot license"
@v1.2.0
    Scenario: Check snapshot license but receive error
        Given a PowerMax service
        And I induce error "InvalidResponse"
        When I check the snapshot license
        And I reset the license cache
        Then the error contains "induced error"
@v1.2.0
    Scenario: Check snapshot but get unisphere mismatch
        Given a PowerMax service
        And I induce error "UnisphereMismatchError"
        When I check the snapshot license
        And I reset the license cache
        Then the error contains "not being managed by Unisphere"
@v1.2.0
    Scenario: Create Snapshot and idempotency
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call CreateSnapshot "snapshot1" on "volume1"
        And I call CreateSnapshot "snapshot1" on "volume1"
        Then a valid CreateSnapshotResponse is returned
@v1.2.0
    Scenario: Create a snapshot on a volume which is already in a snap session
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        When I call CreateSnapshot "snapshot2" on "volume2"
        Then a valid CreateSnapshotResponse is returned
@v1.2.0
    Scenario: Snapshot a single block volume but receive error
        Given a PowerMax service
        When I call Probe
        And I induce error "CreateSnapshotError"
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        Then the error contains "Failed to create snapshot"
@v1.2.0
    Scenario: Create snapshot with no probe
        Given a PowerMax service
        And an invalid volume
        When I invalidate the Probe cache
        And I call CreateSnapshot With "snap1"
        Then the error contains "Controller Service has not been probed"
@v1.2.0
    Scenario: Create snapshot with no volume
        Given a PowerMax service
        And no volume
        When I call Probe
        And I call CreateSnapshot With "snapshot1"
        Then the error contains "Source volume ID is required"
@v1.2.0
    Scenario: Create snapshot with an invalid volume
        Given a PowerMax service
        And an invalid volume
        When I call Probe
        And I call CreateSnapshot With "snapshot1"
        Then the error contains "Could not parse CSI VolumeId"
@v1.2.0
    Scenario: Create snapshot on a non-existent volume
        Given a PowerMax service
        And a non-existent volume
        When I call Probe
        And I call CreateSnapshot With "snapshot1"
        Then the error contains "Could not find source volume on the array"
@v1.2.0
    Scenario: Remove snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call RemoveSnapshot "snapshot1"
        Then no error was received
@v1.2.0
    Scenario: Remove snapshot but get error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "GetJobError"
        When I call RemoveSnapshot "snapshot1"
        Then the error contains "Error getting Job(s)"
@v1.2.0
    Scenario: Remove snapshot but the job fails
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "JobFailedError"
        When I call RemoveSnapshot "snapshot1"
        Then the error contains "DeleteSnapshot failed"
@v1.2.0
    Scenario: Existence of a snapsession on a volume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call IsVolumeInSnapSession on "volume1"
        Then no error was received
@v1.2.0
    Scenario: Existence of a snapshot on a non-existent volume
        Given a PowerMax service
        And a non-existent volume
        When I call IsVolumeInSnapSession on ""
        Then the error contains "Volume cannot be found"
@v1.2.0
    Scenario: Link/Unlink snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        When I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        Then no error was received
@v1.2.0
    Scenario: Unlink a source volume from all the snapshots and delete all the snapshots
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateSnapshot "snpashot2" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2" 
        When I call UnlinkAndTerminate on "volume2"
        And I call UnlinkAndTerminate on "volume1"
        Then no error was received
@v1.2.0
    Scenario: Unlink a target volume from the snapshot and get error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And I induce error "LinkSnapshotError"
        When I call UnlinkAndTerminate on "volume2"
        Then the error contains "error unlinking the snapshot"
        When I call UnlinkAndTerminate on "volume1"
        Then the error contains "error unlinking the snapshot"

@v1.2.0
    Scenario: GetSnapSessions call fails to GetVolumeSnapInfo
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I induce error "GetVolSnapsError"
        And I call GetSnapSessions on "volume1"
        Then the error contains "induced error"
@v1.2.0
    Scenario: GetSnapSessions call fails to GetPrivVolumeByID
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2" 
        And no error was received
        When I induce error "GetPrivVolumeByIDError"
        And I call GetSnapSessions on "volume2"
        Then the error contains "induced error"
@v1.2.0
    Scenario: Get all the source and target snap sessions on a volume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        When I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2" 
        And no error was received
        And I call GetSnapSessions on "volume2"
        And I call GetSnapSessions on "volume1"
        Then no error was received

@v1.2.0
    Scenario: Remove temporary snapshots
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "CSI_TEMP_SNAP00001" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call ExecSnapAction to "Link" snapshot "CSI_TEMP_SNAP00001" to "volume2"
        And no error was received
        When I call RemoveTempSnapshot on "volume2"
        Then no error was received
@v1.2.0
    Scenario: Create a snapshot of a volume and check for idempotency (helper function)
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call CreateSnapshotFromVolume "snapshot1"
        And I call CreateSnapshotFromVolume "snapshot1"
        Then no error was received
@v1.2.0
    Scenario: Create a snapshot of linked target in undefined state (helper function)
        Given a PowerMax service
        And I induce error "TargetNotDefinedError"
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        When I call CreateSnapshotFromVolume "snapshot2"
        Then the error contains "not in Defined state"
@v1.2.0
    Scenario: Create a snapshot of linked target that doesn't unlink (helper function)
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        And I induce error "LinkSnapshotError"
        When I call CreateSnapshotFromVolume "snapshot2"
        Then the error contains "error unlinking the snapshot"

@v1.2.0
    Scenario: Create a volume from a snapshot (helper function)
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        When I call create volume "volume2" from "snapshot1"
        Then no error was received
@v1.2.0
    Scenario: Create a volume from a snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call Create Volume from Snapshot
        Then a valid CreateVolumeResponse is returned
@v1.2.0
    Scenario: Create a volume from invalid snapshot
        Given a PowerMax service
        And an invalid snapshot
        When I call Probe
        And I call Create Volume from Snapshot
        Then the error contains "Snapshot identifier not in supported format"
@v1.2.0
    Scenario: Create a volume from a snapshot but receive error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "LinkSnapshotError"
        When I call Create Volume from Snapshot
        Then the error contains "Failed to create volume from snapshot"
@v1.2.0
    Scenario: Create a volume from another volume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call Create Volume from Volume
        Then a valid CreateVolumeResponse is returned
@v1.2.0
    Scenario: Create a volume without specifying a source
        Given a PowerMax service
        And no volume source
        When I call Probe
        And I call Create Volume from Volume
        Then the error contains "VolumeContentSource is missing volume and snapshot source"
@v1.2.0
    Scenario: Create a volume with non-existent volume as a source
        Given a PowerMax service
        And a non-existent volume
        When I call Probe
        And I call Create Volume from Volume
        Then the error contains "Volume content source volume couldn't be found"
@v1.2.0
    Scenario: Create a volume from invalid volume
        Given a PowerMax service
        And an invalid volume
        When I call Probe
        And I call Create Volume from Volume
        Then the error contains "Source volume identifier not in supported format"
@v1.2.0
    Scenario: Create a volume from a volume but receive error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I induce error "LinkSnapshotError"
        When I call Create Volume from Volume
        Then the error contains "Failed to create volume from volume"
@v1.2.0
    Scenario: Terminating a snaphot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call TerminateSnapshot
        Then no error was received
        And I call TerminateSnapshot
        Then no error was received
@v1.2.0
    Scenario: Unlink and terminate snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call UnlinkAndTerminate snapshot
        Then no error was received
@v1.2.0
    Scenario: Unlink and terminate snapshot but get error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "JobFailedError"
        When I call UnlinkAndTerminate snapshot
        Then the error contains "Failed to terminate snapshot"
@v1.2.0
    Scenario: Unlink and terminate snapshot but unlinking fails
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        And I induce error "LinkSnapshotError"
        When I call UnlinkAndTerminate snapshot
        Then the error contains "error unlinking the snapshot"
@v1.2.0
    Scenario: Unlink and terminate snapshot but targets are not in defined state
        Given a PowerMax service
        And I induce error "TargetNotDefinedError"
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        When I call UnlinkAndTerminate snapshot
        Then the error contains "Not all the targets are in Defined state"
@v1.2.0
    Scenario: Unlink and terminate a snapshot that has already expired
        Given a PowerMax service
        And I induce error "SnapshotExpired"
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call UnlinkAndTerminate snapshot
        Then no error was received

@v1.2.0
    Scenario: Unlink and terminate a snapshot on a volume which is neither source no target
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call UnlinkAndTerminate on "volume1"
        Then the error contains "Volume is neither a source nor target"

@v1.2.0
    Scenario: Unlink and terminate the only snapshot but fail to GetVolume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "GetVolumeError"
        When I call UnlinkAndTerminate snapshot
        Then no error was received
@v1.2.0
    Scenario:  Unlink and terminate the only snapshot but fail to rename the tagged to delete volume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        Then I call DeleteVolume with "single-writer"
        And a valid DeleteVolumeResponse is returned
        And I induce error "UpdateVolumeError"
        When I call UnlinkAndTerminate snapshot
        Then no error was received
@v1.2.0
    Scenario: Delete snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call DeleteSnapshot
        Then a valid DeleteSnapshotResponse is returned
@v1.2.0
    Scenario: Idempotent delete snapshot
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call DeleteSnapshot
        And I call DeleteSnapshot
        Then no error was received
@v1.2.0
    Scenario: Delete a snapshot with no name
        Given a PowerMax service
        When I call Probe
        And I call DeleteSnapshot
        Then the error contains "Snapshot ID to be deleted is required"
@v1.2.0
    Scenario: Delete a snapshot with invalid name
        Given a PowerMax service
        And an invalid snapshot
        When I call Probe
        And I call DeleteSnapshot
        Then the error contains "Snapshot name is not in supported format"
@v1.2.0
    Scenario: Delete a snapshot with no probe
        Given a PowerMax service
        When I invalidate the Probe cache
        And I call DeleteSnapshot
        Then the error contains "Controller Service has not been probed"
@v1.2.0
    Scenario: Delete a snapshot but receive error
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And I induce error "JobFailedError"
        When I call DeleteSnapshot
        Then the error contains "Renaming snapshot failed"

@v1.2.0
    Scenario: Mark snapshots for deletion
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And I call CreateVolume "volume2"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
        And no error was received
        When I call MarkSnapshotForDeletion
        Then no error was received
@v1.2.0
    Scenario: Validate snapshot for deletion
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I call IsSnapshotSource
        Then IsSnapshotSource returns "true"
        And no error was received
@v1.2.0
    Scenario: Validate snapshot for deletion with no errors
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        When I call IsSnapshotSource
        Then IsSnapshotSource returns "false"
        Then no error was received
@v1.2.0
    Scenario: Snapshot cleanup thread but fetching symmetrix ids fails
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "GetSymmetrixError"
        And I call MarkSnapshotForDeletion
        When I check if the snapshot has been deleted
        Then the error contains "Snapshot not deleted by the worker"
@v1.2.0
    Scenario: Snapshot cleanup thread but fetching volumes with snapshot fails
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "GetSymVolumeError"
        And I call MarkSnapshotForDeletion
        When I check if the snapshot has been deleted
        Then the error contains "Snapshot not deleted by the worker"
@v1.2.0
    Scenario: Snapshot cleanup thread but the snapshots are returned with invalid names
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I induce error "InvalidSnapshotName"
        And I call MarkSnapshotForDeletion
        When I check if the snapshot has been deleted
        Then the error contains "Snapshot not deleted by the worker"
@v1.2.0
    Scenario: Soft Deleting a volume
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And a valid CreateSnapshotResponse is returned
        And I call CreateSnapshot "snapshot2" on "volume1"
        And a valid CreateSnapshotResponse is returned
        Then I call DeleteVolume with "single-writer"
        And a valid DeleteVolumeResponse is returned
        Then I call DeleteSnapshot with "snapshot1"
        And no error was received
        Then I call DeleteSnapshot with "snapshot2"
        And no error was received
@v1.2.0
    Scenario: Queueing the snapshots for deletion 
        Given a PowerMax service
        And I call Probe
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I call CreateSnapshot "snapshot1" on "volume1"
        And I call CreateSnapshot "snapshot2" on "volume1"
        And I call CreateSnapshot "snapshot3" on "volume1"
        And a valid CreateSnapshotResponse is returned
        When I queue snapshots for termination
        Then the deletion worker processes the snapshots successfully 