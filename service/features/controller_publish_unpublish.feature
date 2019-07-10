Feature: PowerMax CSI interface
    As a consumer of the CSI interface
    I want to test controller publish / unpublish interfaces
    So that they are known to work

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with an invalid volume
      Given a PowerMax service
      When I call Probe
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "malformed"

@controllerPublish
@v1.0.0
     Scenario Outline: Publish volume with masking view and induced errors
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I induce error <induced>
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains <errormsg>

     Examples:
      | induced                           | errormsg                                         |
      | "UpdateStorageGroupError"         | "Failed to add volume to storage group"          |
      | "GetMaskingViewConnectionsError"  | "none"                                           |

@controllerPublish
@v1.0.0
     Scenario Outline: Publish volume with no masking view and induced errors
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I induce error <induced>
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains <errormsg>

     Examples:
      | induced                   | errormsg                                         |
      | "CreateStorageGroupError" | "Failed to create storage group"                 |
      | "CreateMaskingViewError"  | "Failed to create masking view"                  |
      | "UpdateStorageGroupError" | "Failed to add volume to storage group"          |
      | "GetStorageGroupError"    | "Failed to fetch SG details"                     |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single-writer to a Node with conflicting MV
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with a FastManagedMaskingView
      And I add the Volume to "node1"
      When I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Volume in conflicting masking view"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with conflicting SG
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with FastManagedStorageGroup
      And I add the Volume to "node1"
      When I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Conflicting SG present"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with conflicting SG but no MV
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with FastManagedStorageGroup
      When I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Storage group exists with same name but with conflicting params"

@controllerPublish
@v1.0.0
     Scenario: Publish volume to a Node which is already published to multiple nodes
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I add the Volume to "node1"
      And I have a Node "node2" with MaskingView
      And I add the Volume to "node1"
      And I have a Node "node3" with MaskingView
      When I call PublishVolume with "single-writer" to "node3"
      Then the error contains "Volume already part of multiple Masking views"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer to multiple Nodes
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I have a Node "node2" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "single-writer" to "node2"
      Then the error contains "volume already present in a different masking view"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without masking view
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without host
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Failed to fetch host details from the array"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without masking view
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with StorageGroup
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with PortGroupError
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with StorageGroup
      And I induce error "PortGroupError"
      When I request a PortGroup
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Failed to get port group"

@controllerPublish
@v1.0.0
     Scenario: Idempotent publish volume with single writer
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "single-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Idempotent publish volume with multiple writer
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with multiple writer to different nodes
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I have a Node "node2" with MaskingView
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "multiple-writer" to "node2"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with multiple writer to same node
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume to a Node with conflicting MV
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with a FastManagedMaskingView
      And I add the Volume to "node1"
      When I call PublishVolume with "multiple-writer" to "node1"
      Then the error contains "conflicting SG"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with an invalid volumeID
      Given a PowerMax service
      When I call Probe
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "not formed correctly"

@controllerPublish
@v1.0.0
     Scenario: Publish volume no volumeID specified
      Given a PowerMax service
      And no volume
      When I call Probe
      And I call PublishVolume with "single-writer" to "Node"
      Then the error contains "volume ID is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no nodeID specified
      Given a PowerMax service
      And a valid volume
      And no node
      When I call Probe
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "node ID is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no volume capability
      Given a PowerMax service
      And a valid volume
      And no volume capability
      When I call Probe
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "volume capability is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no access mode
      Given a PowerMax service
      And a valid volume
      And no access mode
      When I call Probe
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "access mode is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no previous probe
      Given a PowerMax service
      And a valid volume
      When I invalidate the Probe cache
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Controller Service has not been probed"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with AccessMode UNKNOWN
      Given a PowerMax service
      And a valid volume
      When I call Probe
      And I call PublishVolume with "unknown" to "node1"
      Then the error contains "access mode cannot be UNKNOWN"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I call UnpublishVolume from "node1"
      And no error was received
      Then a valid UnpublishVolumeResponse is returned 

@controllerPublish
@v1.0.0
     Scenario: Idempotent Unpublish volume
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I call UnpublishVolume from "node1"
      And no error was received
      And I call UnpublishVolume from "node1"
      And no error was received
      Then a valid UnpublishVolumeResponse is returned


@controllerPublish
@v1.0.0
     Scenario: UnpublishVolume when volume is not present in MaskingView
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I induce error "UpdateStorageGroupError"
      And I call UnpublishVolume from "node1"
      And I call UnpublishVolume from "node1"
      And no error was received
      Then a valid UnpublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario Outline: Unpublish volume with induced errors
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I induce error <induced>
      And I call UnpublishVolume from "node1"
      Then the error contains <errormsg>

     Examples:
      | induced                   | errormsg                                         |
      | "DeleteMaskingViewError"  | "Error deleting Masking view: induced error"     |
      | "UpdateStorageGroupError" | "Error updating Storage Group: induced error"    |
      | "GetStorageGroupError"    | "Failed to fetch SG details"                     |
      | "DeleteStorageGroupError" | "none"                                           |

@controllerPublish
@v1.0.0
     Scenario: UnPublish volume with no previous probe
      Given a PowerMax service
      And a valid volume
      And I have a Node "node1" with MaskingView
      When I invalidate the Probe cache
      And I call UnpublishVolume from "node1"
      Then the error contains "Controller Service has not been probed"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume with no volume id
      Given a PowerMax service
      And a valid volume
      When I call Probe
      And no volume
      And I call UnpublishVolume from "node1"
      Then the error contains "Volume ID is required"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume with invalid volume id
      Given a PowerMax service
      And an invalid volume
      And I call UnpublishVolume from "node1"
      Then the error contains "not formed correctly"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume with no node id
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I call UnpublishVolume from ""
      Then the error contains "Node ID is required"

