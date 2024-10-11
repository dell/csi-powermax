Feature: PowerMax CSI interface
    As a consumer of the CSI interface
    I want to test delete service methods
    So that they are known to work

@delete
@v1.0.0
    Scenario: Delete volume with valid CapacityRange capabilities BlockVolume, SINGLE_NODE_WRITER and null VolumeContentSource.
	Given a PowerMax service
	And a valid volume
	When I call Probe
	And I call DeleteVolume with "single-writer"
	Then a valid DeleteVolumeResponse is returned

@delete
@v1.1.0
    Scenario: Delete volume with valid CapacityRange capabilities BlockVolume, SINGLE_NODE_WRITER and null VolumeContentSource and a Post ELM SR array
	Given a PowerMax service
	And a PostELMSR Array
	And a valid volume
	When I call Probe
	And I call DeleteVolume with "single-writer"
	Then a valid DeleteVolumeResponse is returned

@delete
@v1.0.0
    Scenario: Delete volume with valid CapacityRange capabilities BlockVolume,  MULTI_NODE_READER_ONLY null VolumeContentSource.
	Given a PowerMax service
	And a valid volume
	When I call Probe
	And I call DeleteVolume with "multiple-reader"
	Then a valid DeleteVolumeResponse is returned

@delete
@v1.0.0
    Scenario: Delete volume with valid CapacityRange capabilities BlockVolume, MULTI_NODE_WRITE null VolumeContentSource.
	Given a PowerMax service
	And a valid volume
	When I call Probe
	And I call DeleteVolume with "multiple-writer"
	Then a valid DeleteVolumeResponse is returned

@delete
@v1.0.0
    Scenario: Test idempotent deletion volume valid CapacityRange capabilities BlockVolume, SINGLE_NODE_WRITER and null VolumeContentSource (2nd attempt to delete same volume should be nop.)
	Given a PowerMax service
	And a valid volume
	When I call Probe
	And I call DeleteVolume with "single-writer"
	And I call DeleteVolume with "single-writer"
	Then a valid DeleteVolumeResponse is returned

@delete
@v1.0.0
	Scenario: Test deletion without Probe
	 Given a PowerMax service
	 And a valid volume
	 When I invalidate the Probe cache
	 And I call DeleteVolume with "single-writer"
	 Then the error contains "Controller Service has not been probed"


@delete
@v1.0.0
	Scenario: Test deletion when volume is in masking view
	 Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
	  And I call DeleteVolume with "single-writer"
	  Then the error contains "Volume is in use"

@delete
@v1.0.0
    Scenario Outline: Delete volume with various induced errors
	Given a PowerMax service
	And a valid volume
	And I induce error <induced>
	When I call Probe
	And I call DeleteVolume with "single-writer"
	Then the error contains <errormsg>

	Examples:
	| induced                      | errormsg                                                         |
	| "NoVolumeID"                 | "none"                                                           |
	| "InvalidVolumeID"            | "none"                                                           |
	| "UpdateVolumeError"          | "Failed to rename volume"                                        |
	| "GetStorageGroupError"       | "Unable to find storage group"                                   |
	| "GetVolumeError"             | "Could not retrieve volume"                                      |

@delete
@v1.0.0
    Scenario Outline: Restart deletion worker with volumes on the Queue
    Given a PowerMax service
	And <num> existing volumes to be deleted
	And I induce error <induced>
	When I restart the deletionWorker
	Then the error contains <errormsg>
	And <numrecvd> volumes are being processed for deletion

	Examples:
	| num  | numrecvd |induced                                | errormsg                                     |
	| 5    | 5        |"none"                                 | "none"                                       |

@delete
@v1.1.0
    Scenario Outline: Re-run populateDeletionQueuesThread
    Given a PowerMax service
	And <num> existing volumes to be deleted
	And I induce error <induced>
	When I repopulate the deletion queues
	Then the error contains <errormsg>

	Examples:
	| num  |induced                                | errormsg                                     |
	| 5    |"GetVolumeIteratorError"               | "none"                                       |
	| 5    |"GetVolumeError"                       | "none"                                       |

@delete
@v1.5.0
	Scenario: Deletion Worker deletes a Volume with Temp Snapshot
	Given a PowerMax service
	Given a PowerMax service
	And I call CreateVolume "volume1"
	And a valid CreateVolumeResponse is returned
	And I call CreateSnapshot "snapshot1" on "volume1"
	And a valid CreateSnapshotResponse is returned
	And I call CreateVolume "volume2"
	And a valid CreateVolumeResponse is returned
	And I call ExecSnapAction to "Link" snapshot "snapshot1" to "volume2"
	When I queue "volume1" for deletion
	And I queue "volume2" for deletion
	Then deletion worker processes "volume1" which results in "none"
