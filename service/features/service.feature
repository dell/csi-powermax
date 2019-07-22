Feature: PowerMax CSI interface
    As a consumer of the CSI interface
    I want to test service methods
    So that they are known to work

@v1.0.0
    Scenario: Identity GetPluginInfo good call
      Given a PowerMax service
      When I call GetPluginInfo
      Then a valid GetPluginInfoResponse is returned

@v1.0.0
    Scenario: Identity GetPluginCapabilitiles good call
      Given a PowerMax service
      When I call GetPluginCapabilities
      Then a valid GetPluginCapabilitiesResponse is returned

@v1.0.0
    Scenario: Identity Probe good call
      Given a PowerMax service
      When I call Probe
      Then a valid ProbeResponse is returned

@v1.0.0
     Scenario: Identity Probe call no controller connection
      Given a PowerMax service
      And the Controller has no connection
      When I invalidate the Probe cache
      And I call Probe
      Then the error contains "unable to login to Unisphere"

@v1.0.0
     Scenario: Create volume good scenario
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      Then a valid CreateVolumeResponse is returned

@v1.0.0
     Scenario: Idempotent create volume with duplicate volume name
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume2"
      And I call CreateVolume "volume2"
      Then a valid CreateVolumeResponse is returned

@v1.0.0
     Scenario: Idempotent create volume with different sizes
      Given a PowerMax service
      When I call Probe
      And I call CreateVolumeSize "volume3" "50"
      And I call CreateVolumeSize "volume3" "55"
      Then the error contains "different size than requested"

@v1.0.0
     Scenario: Create volume with invalid arrays in the whitelist
      Given a PowerMax service
      And a provided array whitelist of "badone, badtwo"
      When I call Probe
      And I call CreateVolumeSize "volume1" "10"
      Then the error contains "ignored via a whitelist"

@v1.0.0
     Scenario: Create volume without probe
      Given a PowerMax service
      When I invalidate the Probe cache
      And I call CreateVolume "volume1"
      Then the error contains "Controller Service has not been probed"

@v1.0.0
     Scenario Outline: Create volume with induced errors
      Given a PowerMax service
      When I call Probe
      And I induce error <induced>
      And I call CreateVolumeSize "volume3" "50"
      Then the error contains <errormsg>

     Examples:
     | induced                             | errormsg                                           |
     | "none"                              | "none"                                             |
     | "GetStoragePoolListError"           | "Error retrieving StoragePools"                    |
     | "GetVolumeIteratorError"            | "Error looking up volume for idempotence check"    |
     | "GetVolumeError"                    | "Failed to find newly created volume"              |
     | "GetJobError"                       | "Could not create volume"                          |
     | "UpdateStorageGroupError"           | "A job was not returned from UpdateStorageGroup"   |
     | "InvalidSymID"                      | "A SYMID parameter is required"                    |
     | "InvalidStoragePool"                | "Storage Pool invalid not found"                   |
     | "InvalidServiceLevel"               | "An invalid Service Level parameter was specified" |

@v1.0.0
     Scenario Outline: Delete volume worker scenarios
      Given a PowerMax service
      When I call Probe
      And I call CreateVolumeSize "volume7" "50"
      And I induce error <induced>
      And I queue "volume7" for deletion
      Then deletion worker processes "volume7" which results in <errormsg>

     Examples:
     | induced                             | errormsg                                           |
     | "none"                              | "none"                                             |
     | "GetVolumeError"                    | "Error retrieving Volume: induced error"           |
     | "GetJobError"                       | "Error getting Job(s): induced error"              |
     | "JobFailedError"                    | "Job Failed"                                       |
     | "UpdateStorageGroupError"           | "Error updating Storage Group: induced error"      |
     | "DeleteVolumeError"                 | "Error deleting Volume: induced error"             |

@v1.0.0
     Scenario: Delete worker multiple volumes
      Given a PowerMax service
      When I call Probe
      And I call CreateVolumeSize "volume8" "50"
      And I call CreateVolumeSize "volume9" "60"
      And I call CreateVolumeSize "volume10" "70"
      And I queue "volume8" for deletion
      And I queue "volume9" for deletion
      And I queue "volume10" for deletion
      Then deletion worker processes "volume8" which results in "none"
      And deletion worker processes "volume9" which results in "none"
      And deletion worker processes "volume10" which results in "none"

@v1.0.0
     Scenario Outline: Idempotent create volume with induced errors
      Given a PowerMax service
      When I call Probe
      And I call CreateVolumeSize "volume3" "50"
      And I induce error <induced>
      And I induce error <second_induced>
      And I call CreateVolumeSize "volume3" "50"
      Then the error contains <errormsg>

     Examples:
     | induced                     | second_induced                   | errormsg                                           |
     | "none"                      | "none"                           | "none"                                             |
     | "GetVolumeIteratorError"    | "none"                           | "Error looking up volume for idempotence check"    |
     | "GetVolumeError"            | "none"                           | "Error fetching volume for idempotence check"      |
     | "GetStorageGroupError"      | "none"                           | "none"                                             |
     | "GetStorageGroupError"      | "CreateStorageGroupError"        | "Error creating storage group"                     |

@v1.0.0
     Scenario: Create volume with application prefix
      Given a PowerMax service
      When I call Probe
      And I specify a ApplicationPrefix
      And I call CreateVolume "volume1" 
      Then a valid CreateVolumeResponse is returned


@v1.0.0
     Scenario: Create volume with storage group
      Given a PowerMax service
      When I call Probe
      And I specify a StorageGroup
      And I call CreateVolume "volume1" 
      Then a valid CreateVolumeResponse is returned

@notyet
     Scenario: Idempotent create volume with different storage pool
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume4" 
      And I change the StoragePool "other_storage_pool"
      And I call CreateVolume "volume4" 
      Then the error contains "different storage pool"

@notyet
     Scenario: Idempotent create volume with bad storage pool
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume4" 
      And I change the StoragePool "no_storage_pool"
      And I call CreateVolume "volume4" 
      Then the error contains "Couldn't find storage pool"

@v1.0.0
     Scenario: Create volume with Accessibility Requirements
      Given a PowerMax service
      When I call Probe
      And I specify AccessibilityRequirements
      And I call CreateVolume "accessibility"
      Then the error contains "Volume AccessibilityRequirements is not supported"

@v1.0.0
     Scenario: Create volume with VolumeContentSource
      Given a PowerMax service
      When I call Probe
      And I specify VolumeContentSource
      And I call CreateVolume "volumecontentsource"
      Then the error contains "Volume VolumeContentSource is not supported"

@v1.0.0
     Scenario: Create volume with AccessMode_MULTINODE_WRITER
      Given a PowerMax service
      When I call Probe
      And I specify MULTINODEWRITER
      And I call CreateVolume "multi-writer"
      Then a valid CreateVolumeResponse is returned
       
@v1.0.0
     Scenario: Attempt create volume with no name
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume ""
      Then the error contains "Name cannot be empty"

@v1.0.0
     Scenario: Attempt create volume with long name
     Given a PowerMax service
     When I call Probe
     And I call CreateVolume "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
     Then a valid CreateVolumeResponse is returned

@v1.0.0
     Scenario: Idempotent create volume with duplicate long volume name
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
      And I call CreateVolume "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyz"
      Then a valid CreateVolumeResponse is returned

@v1.0.0
     Scenario: Create volume with bad capacity
      Given a PowerMax service
      When I call Probe
      And I specify a BadCapacity
      And I call CreateVolume "bad capacity"
      Then the error contains "bad capacity"

@v1.0.0
     Scenario: Call NodeGetInfo and validate NodeId
      Given a PowerMax service
      When I call NodeGetInfo
      Then a valid NodeGetInfoResponse is returned

@v1.0.0
     Scenario: Call NodeGetInfo without setting Node Name
      Given a PowerMax service
      And I induce error "UnspecifiedNodeName"
      When I call NodeGetInfo
      Then the error contains "Unable to get Node Name"

@v1.0.0
     Scenario: Call GetCapacity with valid Storage Pool Name
      Given a PowerMax service
      When I call Probe
      And I call GetCapacity with storage pool "SRP_1"
      Then a valid GetCapacityResponse is returned

@v1.0.0
     Scenario: Call GetCapacity without probe
      Given a PowerMax service
      When I invalidate the Probe cache
      And I call GetCapacity with storage pool "SRP_1"
      Then the error contains "Controller Service has not been probed"

@v1.0.0	
     Scenario: Call GetCapacity with invalid Storage Pool name
      Given a PowerMax service
      When I call Probe
      And I call GetCapacity with storage pool "xxx"
      Then the error contains "Storage Pool xxx not found"

@v1.0.0
     Scenario: Call GetCapacity with invalid arrays in the whitelist
      Given a PowerMax service
      And a provided array whitelist of "badone, badtwo"
      When I call Probe
      And I call GetCapacity with storage pool "SRP_1"
      Then the error contains "ignored via a whitelist"

@v1.0.0
     Scenario: Call GetCapacity with induced error retrieving capacity
      Given a PowerMax service
      When I call Probe
      # First call populates the cache
      And I call GetCapacity with storage pool "SRP_1"
      And I induce error "GetStoragePoolError"
      And I call GetCapacity with storage pool "SRP_1"
      Then the error contains "Could not retrieve StoragePool"

@v1.0.0
     Scenario: Call GetCapacity without specifying Symmetrix ID
      Given a PowerMax service
      When I call Probe
      And I call GetCapacity without Symmetrix ID
      Then the error contains "A SYMID parameter is required"

@v1.0.0
     Scenario: Call GetCapacity without specifying Parameters
      Given a PowerMax service
      When I call Probe
      And I call GetCapacity without Parameters
      Then the error contains "Required StoragePool and SymID in parameters"

@v1.0.0
     Scenario: Call GetCapacity with invalid capabilities
      Given a PowerMax service
      When I call Probe
      And I call GetCapacity with Invalid capabilities
      Then the error contains "access mode cannot be UNKNOWN"

@v1.0.0
     Scenario: Call ControllerGetCapabilities
      Given a PowerMax service
      When I call ControllerGetCapabilities
      Then a valid ControllerGetCapabilitiesResponse is returned

@v1.0.0
     Scenario Outline: Calls to validate volume capabilities
      Given a PowerMax service
      When I call Probe
      And I call CreateVolume "volume1"
      And a valid CreateVolumeResponse is returned
      And I call ValidateVolumeCapabilities with voltype <voltype> access <access> fstype <fstype> pool <pool> level <level>
      Then the error contains <errormsg>

      Examples:
      | voltype    | access                     | fstype    | pool    | level       | errormsg                                                          |
      | "block"    | "single-writer"            | "none"    | ""      | ""          | "none"                                                            |
      | "block"    | "multi-reader"             | "none"    | ""      | ""          | "none"                                                            |
      | "mount"    | "multi-writer"             | "ext4"    | ""      | ""          | "multi-node with writer(s) only supported for block access type"  |
      | "mount"    | "multi-node-single-writer" | "ext4"    | ""      | ""          | "multi-node with writer(s) only supported for block access type"  |
      | "mount"    | "unknown"                  | "ext4"    | ""      | ""          | "access mode cannot be UNKNOWN"                                   |
      | "none "    | "unknown"                  | "ext4"    | ""      | ""          | "unknown access type is not Block or Mount"                       |
      | "mount"    | "single-writer"            | "ext4"    | "SRP_1" | "Optimized" | "none"                                                            |
      | "mount"    | "single-writer"            | "ext4"    | "bad"   | "bad"       | "Unable to validate context (SRP=false, SLO=false)"               |
      | "mount"    | "single-writer"            | "ext4"    | "bad"   | "Optimized" | "Unable to validate context (SRP=false, SLO=true)"                |
      | "mount"    | "single-writer"            | "ext4"    | "SRP_1" | "bad"       | "Unable to validate context (SRP=true, SLO=false)"                |
      | "mount"    | "single-writer"            | "ext4"    | ""      | "Optimized" | "none"                                                            |
      | "mount"    | "single-writer"            | "ext4"    | "SRP_1" | ""          | "none"                                                            |

@v1.0.0
     Scenario Outline: Call validate volume capabilities with non-existent volume
      Given a PowerMax service
      When I call Probe
      And I induce error <induced>
      And I call CreateVolume "volume1"
      And I call ValidateVolumeCapabilities with voltype <voltype> access <access> fstype <fstype> pool <pool> level <level>
      Then the error contains <errormsg>

      Examples:
      | voltype       | access               | fstype      | induced                    | errormsg                   | pool           | level               |
      | "block"       | "single-writer"      | "none"      | "InvalidVolumeID"          | "is not formed correctly"  | "SRP_1"        | "Optimized"         |
      | "block"       | "single-writer"      | "none"      | "DifferentVolumeID"        | "Volume cannot be found"   | "SRP_1"        | "Optimized"         |

@v1.0.0
     Scenario Outline: Call with no probe volume to validate volume capabilities
      Given a PowerMax service
      When I invalidate the Probe cache
      And I call ValidateVolumeCapabilities with voltype <voltype> access <access> fstype <fstype> pool "SRP_1" level "Optimized"
      Then the error contains <errormsg>

      Examples:
      | voltype       | access               | fstype      | errormsg                                                          |
      | "block"       | "single-writer"      | "none"      | "Service has not been probed"                                     |

@v1.0.0
     Scenario: Call validate volume capabilities with invalid volume identifier
      Given a PowerMax service
      When I call Probe
      And I have a volume with invalid volume identifier
      And I call ValidateVolumeCapabilities with voltype "block" access "single-writer" fstype "ext4" pool "SRP_1" level "Optimized"
      Then the error contains "Failed to validate combination of Volume Name and Volume ID"

@v1.0.0
     Scenario: Test BeforeServe
      Given a PowerMax service
      When I call BeforeServe
      # Get different error message on Windows vs. Linux
      Then the error contains "cannot find the path specified@@none"

@v1.0.0
     Scenario: Test BeforeServe
      Given a PowerMax service
      When I call BeforeServe without ClusterPrefix
      Then the error contains "No Cluster Prefix was specified"

@v1.0.0
     Scenario: Test BeforeServe
      Given a PowerMax service
      When I call BeforeServe with an invalid ClusterPrefix
      Then the error contains "exceeds maximum length"

@v1.0.0
     Scenario: Call NodeStageVolume, should get unimplemented
      Given a PowerMax service
      When I call NodeStageVolume
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call NodeUnstageVolume, should get unimplemented
      Given a PowerMax service
      When I call NodeUnstageVolume
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call NodeGetVolumeStats, should get unimplemented
      Given a PowerMax service
      When I call NodeGetVolumeStats
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call CreateSnapshot, should get unimplemented
      Given a PowerMax service
      When I call CreateSnapshot
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call DeleteSnapshot, should get unimplemented
      Given a PowerMax service
      When I call DeleteSnapshot
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call ListVolumes, should get unimplemented
      Given a PowerMax service
      When I call ListVolumes
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call ListSnapshots, should get unimplemented
      Given a PowerMax service
      When I call ListSnapshots
      Then the error contains "Unimplemented"

@v1.0.0
     Scenario: Call NodeGetCapabilities should return a valid response
      Given a PowerMax service
      When I call NodeGetCapabilities
      Then a valid NodeGetCapabilitiesResponse is returned

     Scenario: Validate PortGroup selection
      Given a PowerMax service
      When I request a PortGroup
      Then a valid PortGroup is returned
      And no error was received

@v1.0.0
    Scenario Outline: Validate createOrUpdateHost
      Given a PowerMax service
      And I induce error <induced1>
      And I induce error <induced2>
      When I invoke createOrUpdateHost <hostname>
      Then the error contains <errormsg>
      And <count> initiators are found

      Examples:
      | hostname           | induced1             | induced2              | errormsg                         | count |
      | "testhost"         |"NoArray"             | "none"                | "No array specified"             | 0     |
      | "testhost"         |"NoNodeName"          | "none"                | "No nodeName specified"          | 0     |
      | "testhost"         |"NoIQNs"              | "none"                | "No initiators specified"        | 0     |
      | "testhost"         |"GetHostError"        | "CreateHostError"     | "Unable to create Host"          | 0     |
      | "testhost"         |"none"                | "none"                | "none"                           | 1     |
      | "CSI-Test-Node-1"  |"UpdateHostError"     | "none"                | "Unable to update Host"          | 0     |
      | "CSI-Test-Node-1"  |"UpdateHostError"     | "ResetAfterFirstError"| "none"                           | 1     |
      | "CSI-Test-Node-1"  |"GetHostError"        | "none"                | "none"                           | 1     |

@v1.0.0
    Scenario Outline: Validate nodeHostSetup
      Given a PowerMax service
      And I have a Node "Node1" with MaskingView
      And I induce error <induced>
      When I invoke nodeHostSetup with a <mode> service
      Then the error contains <errormsg>

      Examples:
      | induced              | errormsg                         | mode              | 
      | "none"               | "none"                           | "node"            |
      | "GetSymmetrixError"  | "timed out"                      | "node"            |

@v1.0.0
    Scenario: Validate nodeHostSetup with temporary failure
      Given a PowerMax service
      And I induce error "GetSymmetrixError"
      And the error clears after 10 seconds
      When I invoke nodeHostSetup with a "node" service
      Then the error contains "none"

@v1.0.0
    Scenario Outline: Validate ensureLoggedIntoEveryArray
      Given a PowerMax service
      And there are no arrays logged in
      And I induce error <induced1>
      When I invoke ensureLoggedIntoEveryArray
      Then the error contains <errormsg>
      And <count> arrays are logged in

      Examples:
      | induced1               | errormsg                                         | count |
      | "GetSymmetrixError"    | "Unable to retrieve Array List"                  | 0     |
      | "GetDirectorError"     | "Error retrieving Director"                      | 0     |
      | "GOISCSIDiscoveryError"| "Unable to perform iSCSI discovery and login"    | 0     |
      | "none"                 | "none"                                           | 2     |

@v1.0.0
    Scenario Outline: Validate Array Whitelists
      Given a PowerMax service
      And a provided array whitelist of <whitelist>
      When I invoke getArrayWhitelist
      Then the error contains <errormsg>
      And <count> arrays are found

      Examples:
      | whitelist              | errormsg                         | count              | 
      | ""                     | "none"                           | 0                  |
      | "001, 002"             | "none"                           | 2                  |
      | "001,002,   003"       | "none"                           | 3                  |
      | "001,, ,  ,002"        | "none"                           | 2                  |

@v1.0.0
    Scenario Outline: Test GetVolumeByID function
      Given a PowerMax service
      And I induce error <induced>
      And a valid volume
      When I call GetVolumeByID
      Then the error contains <errormsg>
      And a valid GetVolumeByID result is returned if no error

      Examples:
      | induced                    | errormsg                           |
      | "none"                     | "none"                             |
      | "NoVolumeID"               | "malformed"                        |
      | "InvalidVolumeID"          | "cannot be found"                  |
      | "DifferentVolumeID"        | "Failed to validate combination"   |
      | "GetVolumeError"           | "failure checking volume"          |

@v1.0.0
    Scenario Outline: Test validateStoragePoolID function
      Given a PowerMax service
      And I call validateStoragePoolID <numberOfTimes> in parallel
      Then no error was received

       Examples:
       | numberOfTimes               |
       | 2000                        |