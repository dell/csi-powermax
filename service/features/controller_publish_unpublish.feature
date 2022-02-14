Feature: PowerMax CSI interface
    As a consumer of the CSI interface
    I want to test controller publish / unpublish interfaces
    So that they are known to work

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.1.0
     Scenario: Publish volume with single writer for FC when PG is present
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I set transport protocol to "FC"
      And I have a Node "node1" with Host
      And I have a FC PortGroup "PG1"
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.1.0
     Scenario: Publish volume with single writer for FC when PG is not present
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I set transport protocol to "FC"
      And I have a Node "node1" with Host
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.1.0
     Scenario: Publish volume with single writer for FC when PG is not present and initiator is mapped to lot of dir/ports
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I set transport protocol to "FC"
      And I have a Node "node1" with Host with Initiator mapped to multiple ports
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with an invalid volume
      Given a PowerMax service
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with <access> to "node1"
      Then the error contains "Volume not found"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario Outline: Publish volume with masking view and induced errors
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I induce error <induced>
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains <errormsg>

     Examples:
      | induced                           | errormsg                                              |
      | "UpdateStorageGroupError"         | "Failed to add volume to storage group"               |
      | "GetMaskingViewConnectionsError"  | "Could not get MV Connections"                        |
      | "GetPortError"                    | "Failed to fetch port details for any director ports" |

#@wip
@controllerPublish
@v1.0.0
     Scenario Outline: Publish volume with no masking view and induced errors
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I set transport protocol to "FC"
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
     Scenario Outline: Publish volume with no masking view and induced errors
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I set transport protocol to "ISCSI"
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
      | "GetMaskingViewError"     | "none"                                           |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single-writer to a Node with conflicting MV
      Given a PowerMax service
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
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with FastManagedStorageGroup
      And I add the Volume to "node1"
      When I call PublishVolume with <access> to "node1"
      Then the error contains "Conflicting SG present"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with conflicting SG but no MV
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with FastManagedStorageGroup
      When I call PublishVolume with <access> to "node1"
      Then the error contains "Storage group exists with same name but with conflicting params"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume to a Node which is already published to multiple nodes
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with initiators "initlist1" with MaskingView
      And I add the Volume to "node1"
      And I have a Node "node2" with initiators "initlist2" with MaskingView
      And I add the Volume to "node1"
      And I have a Node "node3" with initiators "initlist3" with MaskingView
      When I call PublishVolume with "single-writer" to "node3"
      Then the error contains "Volume already part of multiple Masking views"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer to multiple Nodes
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with initiators "initlist1" with MaskingView
      And I have a Node "node2" with initiators "initlist1" with MaskingView
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with <access> to "node2"
      Then the error contains "volume already present in a different masking view"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without masking view
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without host
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I call PublishVolume with <access> to "node1"
      Then the error contains "Failed to fetch host"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer without masking view
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with StorageGroup
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Publish volume with single writer with PortGroupError
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with Host
      And I have a Node "node1" with StorageGroup
      And I induce error "PortGroupError"
      When I request a PortGroup
      And I call PublishVolume with <access> to "node1"
      Then the error contains "No port groups have been supplied"

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Idempotent publish volume with single writer
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with <access> to "node1"
      Then a valid PublishVolumeResponse is returned

     Examples:
      | access                      |
      | "single-writer"             |
      | "single-node-single-writer" |
      | "single-node-multi-writer"  |

@controllerPublish
@v1.0.0
     Scenario: Idempotent publish volume with multiple writer
      Given a PowerMax service
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
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with initiators "initlist1" with MaskingView
      And I have a Node "node2" with initiators "initlist2" with MaskingView
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "multiple-writer" to "node2"
      Then a valid PublishVolumeResponse is returned

@controllerPublish
@v1.0.0
     Scenario: Publish volume with multiple writer to same node
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned
      And I call PublishVolume with "multiple-writer" to "node1"
      Then a valid PublishVolumeResponse is returned

#@wip
@controllerPublish
@v1.0.0
     Scenario: Publish volume to a Node with conflicting MV
      Given a PowerMax service
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
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "Volume not found"

@controllerPublish
@v1.0.0
     Scenario: Publish volume no volumeID specified
      Given a PowerMax service
      And no volume
      And I call PublishVolume with "single-writer" to "Node"
      Then the error contains "volume ID is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no nodeID specified
      Given a PowerMax service
      And a valid volume
      And no node
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "node ID is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no volume capability
      Given a PowerMax service
      And a valid volume
      And no volume capability
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "volume capability is required"

@controllerPublish
@v1.0.0
     Scenario: Publish volume with no access mode
      Given a PowerMax service
      And a valid volume
      And no access mode
      And I call PublishVolume with "single-writer" to "node1"
      Then the error contains "access mode is required"

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
      And I call PublishVolume with "unknown" to "node1"
      Then the error contains "access mode cannot be UNKNOWN"

#@wip
@controllerPublish
@v1.0.0
     Scenario: Unpublish volume
      Given a PowerMax service
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
      # The new code retries individually if there was an error, and the masking view has been
      # deleted, so on the retry there is no error but the volume is left in the SG.
      # I'm not sure what to do about this. It would be true in the real world if k8s retried also.
      | "UpdateStorageGroupError" | "none"                                           |
      #| "UpdateStorageGroupError" | "Error updating Storage Group: induced error"    |
      | "GetStorageGroupError"    | "Failed to fetch SG details"                     |
      | "DeleteStorageGroupError" | "none"                                           |

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
      And no volume
      And I call UnpublishVolume from "node1"
      Then the error contains "Volume ID is required"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume with invalid volume id
      Given a PowerMax service
      And an invalid volume
      And I call UnpublishVolume from "node1"
      Then the error contains "Invalid volume id"

@controllerPublish
@v1.0.0
     Scenario: Unpublish volume with no node id
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      And I call UnpublishVolume from ""
      Then the error contains "Node ID is required"

@controllerPublish
@v1.1.0
    Scenario Outline: Call GetPortIdentifier with various scenarios
      Given a PowerMax service
      And I have a PortCache entry for port <incache>
      And I have a port "FA-1D:5" identifier "5000000000000002" type "FibreChannel"
      And I have a port "FA-1D:100" identifier "" type ""
      And I induce error <induced>
      When I call GetPortIdenfier for <desired>
      Then the result is <result>
      And the error contains <errormsg>

      Examples:
      | incache          | desired               | induced               | result               | errormsg                                              |
      | "FA-1D:4"        | "FA-1D:4"             | "none"                | "5000000000000001"   | "none"                                                |
      | "FA-1D:4"        | "FA-1D:100"           | "none"                | ""                   | "port not found"                                      |
      | "FA-1D:4"        | "FA-1D:5"             | "none"                | "0x5000000000000002" | "none"                                                |
      | ""               | "FA-1D:5"             | "none"                | "0x5000000000000002" | "none"                                                |

@controllerPublish
@v1.3.0
#@wip
   Scenario Outline: Call requestAddVolumesToSGMV and runAddVolumesToSGMV with various scenarios
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node <node> with MaskingView
      When I call requestAddVolumeToSGMV <node> mv <mv>
      And I induce error <induced>
      And I call runAddVolumesToSGMV
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv        | induced                 | errormsg                                      |
      | "node1"  |  "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "GetVolumeError"        | "Error retrieving Volume"                     |
      | "node1"  |  "default" | "GetStorageGroupError"  | "Failed to fetch SG details"                  |
      | "none"   |  "default" | "none"                  | "none"                                        |

@controllerPublish
@v1.3.0
@wip
   Scenario Outline: Call requestAddVolumesToSGMV and runAddVolumesToSGMV when creating masking view
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And I request a PortGroup
      And a valid CreateVolumeResponse is returned
      When I call requestAddVolumeToSGMV <node> mv <mv>
      And I induce error <induced>
      And I call runAddVolumesToSGMV
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv        | induced                 | errormsg                                      |
      | "node1"  |  "default" | "GetHostError"          | "Failed to fetch host details"                |

@controllerPublish
@v1.3.0
#@wip
   Scenario Outline: Test handleAddVolumeToSGMV with various scenarios
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node <node> with MaskingView
      When I call requestAddVolumeToSGMV <node> mv <mv>
      And I call runAddVolumesToSGMV
      And I call requestAddVolumeToSGMV <node> mv <mv>
      And I induce error <induced>
      And I call handleAddVolumeToSGMVError
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv        | induced                 | errormsg                                      |
      | "node1"  |  "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "GetVolumeError"        | "Error retrieving Volume"                     |
      | "node1"  |  "default" | "GetMaskingViewError"   | "none"                                        |

@controllerPublish
@v1.3.0
#@wip
   Scenario Outline: Test requestAddVolumesToSGMV with multiple requests
      Given a PowerMax service
      And I call CreateVolume "volume1"
      And I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node <node> with MaskingView
      When I call requestAddVolumeToSGMV <node> mv <mv1>
      And I call requestAddVolumeToSGMV <node> mv <mv2>
      And I call runAddVolumesToSGMV
      And I induce error <induced>
      And I call handleAddVolumeToSGMVError
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv1       | mv2       | induced                 | errormsg                                      |
      | "node1"  |  "badmv"   | "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "badmv"   | "none"                  | "none"                                        |
      | "node1"  |  "default" | "default" | "none"                  | "none"                                        |

@controllerPublish
@v1.3.0
@wip
     Scenario Outline: Test requestRemoveVolumesFromSGMV and runRemoveVolumesFromSGMV
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      When I call requestRemoveVolumeFromSGMV <node> mv <mv>
      And I induce error <induced>
      And I call runRemoveVolumesFromSGMV
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv        | induced                 | errormsg                                      |
      | "node1"  |  "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "GetStorageGroupError"  | "Failed to fetch SG details"                  |

@controllerPublish
@v1.3.0
@wip
   Scenario Outline: Test handleRemoveVolumeFromSGMV with various scenarios
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      When I call requestRemoveVolumeFromSGMV <node> mv <mv>
      And I call runRemoveVolumesFromSGMV
      And I call requestRemoveVolumeFromSGMV <node> mv <mv>
      And I induce error <induced>
      And I call handleRemoveVolumeFromSGMVError
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv        | induced                 | errormsg                                      |
      | "node1"  |  "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "GetVolumeError"        | "Error retrieving Volume"                     |
      | "node1"  |  "default" | "GetMaskingViewError"   | "none"                                        |

@controllerPublish
@v1.3.0
@wip
   Scenario Outline: Test requestRemoveVolumesFromSGMV with multiple requests
      Given a PowerMax service
      And I call CreateVolume "volume1"
      When I request a PortGroup
      And a valid CreateVolumeResponse is returned
      And I have a Node "node1" with MaskingView
      And I call PublishVolume with "single-writer" to "node1"
      And no error was received
      When I call requestRemoveVolumeFromSGMV <node> mv <mv1>
      And I call requestRemoveVolumeFromSGMV <node> mv <mv2>
      And I call runRemoveVolumesFromSGMV
      And I induce error <induced>
      And I call handleRemoveVolumeFromSGMVError
      Then the error contains <errormsg> 
     
      Examples:
      | node     |  mv1       | mv2       | induced                 | errormsg                                      |
      | "node1"  |  "badmv"   | "default" | "none"                  | "none"                                        |
      | "node1"  |  "default" | "badmv"   | "none"                  | "none"                                        |
      | "node1"  |  "default" | "default" | "none"                  | "none"                                        |
