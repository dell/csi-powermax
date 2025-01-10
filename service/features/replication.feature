Feature: PowerMax CSI Interface
  As a consumer of the CSI interface
  I want to test replication interfaces
  So that they are known to work


  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume with no errors
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v1.6.0
  Scenario: Idempotent create SRDF volume with no errors
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v1.6.0
  Scenario: Idempotent create SRDF volume with error in GetRDFDevicePairInfo
    Given a PowerMax service
    And I induce error "GetSRDFPairInfoError"
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then the error contains "Failed to fetch rdf pair information"

  @srdf
  @v1.6.0
  Scenario: Create 2 SRDF volume with no errors
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then a valid CreateVolumeResponse is returned
    And I call RDF enabled CreateVolume "volume2" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume with failed verification of protection group ID
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then a valid CreateVolumeResponse is returned
    And I call RDF enabled CreateVolume "volume2" in namespace "csi-test-other", mode "ASYNC" and RDFGNo 13
    Then the error contains "already a part of ReplicationGroup"

  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume with unsupported replication mode
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "unsupported" and RDFGNo 13
    Then the error contains "Unsupported Replication Mode"

  @srdf
  @v1.6.0
  Scenario Outline: Create an SRDF volume with different errors
    Given a PowerMax service
    And I induce error <induced>
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then the error contains <errormsg>
    Examples:
      | induced                          | errormsg                                   |
      | "CreateStorageGroupError"        | "Error creating protected storage group"   |
      | "VolumeNotAddedError"            | "Could not add volume in protected SG"     |
      | "GetRDFGroupError"               | "the specified RA group does not exist"    |
      | "RDFGroupHasPairError"           | "already has volume pairing"               |
      | "CreateSGReplicaError"           | "Failed to create SG replica"              |

  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume failed as a SG with volumes present on Remote array
    Given a PowerMax service
    And I induce error "GetSGWithVolOnRemote"
    When I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then the error contains "has devices, can not protect SG"

  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume with error as Delete SG on remote SG failed
    Given a PowerMax service
    And I induce error "GetSGOnRemote"
    And I induce error "DeleteStorageGroupError"
    When I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then the error contains "Error (Delete Remote Storage Group"

  @srdf
  @v1.6.0
  Scenario: Create an SRDF volume after deleting remote SG and no errors
    Given a PowerMax service
    And I induce error "GetSGOnRemote"
    When I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    Then no error was received


  @srdf
  @v1.6.0
  Scenario Outline:  Create an SRDF volume failed with error in VerifyProtectedGroupDirection
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    When I call RDF enabled CreateVolume "volume2" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And the error contains <errormsg>
    Examples:
      | induced                   | errormsg                       |
      | "GetSRDFInfoError"        | "Error retrieving SRDF Info"   |
      | "VolumeRdfTypesError"     | "does not contains R1 volumes" |

  @srdf
  @v1.6.0
  Scenario: GetRDFInfoFromSGID with no error
    Given a PowerMax service
    And I call  GetRDFInfoFromSGID with "csi-rep-sg-csi-test-13-ASYNC"
    Then no error was received

  @srdf
  @v1.6.0
  Scenario: GetRDFInfoFromSGID with error
    Given a PowerMax service
    And I call  GetRDFInfoFromSGID with "csi-test-namespace-1-mode"
    Then the error contains "not formed correctly"

  @srdf
  @v1.6.0
  Scenario: Protect an already protected storage group with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    And I call ProtectStorageGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then no error was received

  @srdf
  @v1.6.0
  Scenario: Protect an already protected storage group with error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    When I induce error "GetProtectedStorageGroupError"
    And I call ProtectStorageGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then the error contains "storage group cannot be found"

  @srdf
  @v1.6.0
  Scenario: DiscoverStorageProtectionGroup with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    And I call DiscoverStorageProtectionGroup
    Then no error was received

  @srdf
  @v1.6.0
  Scenario Outline: DiscoverStorageProtectionGroup with different error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call DiscoverStorageProtectionGroup
    Then the error contains <errormsg>
    Examples:
      | induced                      | errormsg                                        |
      | "GetVolumeError"             | "Error retrieving Volume"                       |
      | "InvalidLocalVolumeError"    | "Failed to find protected local storage group"  |
      | "GetRemoteVolumeError"       | "Could not find volume"                         |
      | "InvalidRemoteVolumeError"   | "Failed to find protected remote storage group" |
      | "GetSRDFPairInfoError"       | "Could not retrieve pair info"                  |
      | "FetchResponseError"         | "failure checking volume"                       |

  @srdf
  @v1.6.0
  Scenario: CreateRemoteVolume with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    And I call CreateRemoteVolume
    Then no error was received

  @srdf
  @v1.6.0
  Scenario Outline: CreateRemoteVolume with different error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call CreateRemoteVolume
    Then the error contains <errormsg>
    Examples:
      | induced                      | errormsg                                        |
      | "GetVolumeError"             | "Error retrieving Volume"                       |
      | "GetRemoteVolumeError"       | "Could not find volume"                         |
      | "InvalidRemoteVolumeError"   | "Failed to find protected remote storage group" |
      | "GetSRDFPairInfoError"       | "Could not retrieve pair info"                  |
      | "FetchResponseError"         | "failure checking volume"                       |
      | "UpdateVolumeError"          | "Failed to rename volume"                       |

  @srdf
  @v1.6.0
  Scenario: DeleteStorageProtectionGroup with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error "RemoveVolumesFromSG"
    And I call DeleteStorageProtectionGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then no error was received

  @srdf
  @v1.6.0
  Scenario: DeleteStorageProtectionGroup with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error "GetProtectedStorageGroupError"
    And I call DeleteStorageProtectionGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then no error was received

  @srdf
  @v1.6.0
  Scenario Outline: DeleteStorageProtectionGroup with different error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call DeleteStorageProtectionGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then the error contains <errormsg>
    Examples:
      | induced                           | errormsg                          |
      | "RDFGroupHasPairError"            | "it is not empty"                 |
      | "DeleteStorageGroupError"         | "Error deleting storage group"    |
      | "FetchResponseError"              | "GetProtectedStorageGroup failed" |

  @srdf
  @v2.6.0
  Scenario: Create SRDF volume with Auto-SRDF group
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "test", mode "ASYNC" and RDFGNo 0
    And a valid CreateVolumeResponse is returned

  @srdf
  @v2.6.0
  Scenario Outline: Create SRDF volume with Auto-SRDF group with different errors
    Given a PowerMax service
    And I induce error <induced>
    And I call RDF enabled CreateVolume "volume1" in namespace "test", mode "ASYNC" and RDFGNo 0
    Then the error contains <errormsg>
    Examples:
      | induced                       | errormsg                                   |
      | "GetFreeRDFGError"            | "Could not retrieve free RDF group"        |
      | "GetLocalOnlineRDFDirsError"  | "Could not retrieve RDF director"          |
      | "GetLocalOnlineRDFPortsError" | "Could not retrieve local online RDF port" |
      | "GetRemoteRDFPortOnSANError"  | "Could not retrieve remote RDF port"       |
      | "GetLocalRDFPortDetailsError" | "Could not retrieve local RDF port"        |
      | "CreateRDFGroupError"         | "error creating RDF group"                 |
      | "GetRDFGroupError"            | "the specified RA group does not exist"    |

  @srdf
  @v2.6.0
  Scenario Outline: Create a SRDF volume from a snapshot
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call CreateSnapshot "snapshot1" on "volume1"
    And a valid CreateSnapshotResponse is returned
    When I call RDF enabled CreateVolume "volume2" in namespace "test", mode <mode> and RDFGNo 13 from snapshot
    Then a valid CreateVolumeResponse is returned
    Examples:
      | mode    |
      | "ASYNC" |
      | "SYNC"  |

  @srdf
  @v2.6.0
  Scenario Outline: Create a SRDF volume from another volume
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I induce error <induced>
    When I call RDF enabled CreateVolume "volume2" in namespace "test", mode "ASYNC" and RDFGNo 13 from volume
    Then the error contains <errormsg>
    Examples:
      | induced               | errormsg                                   |
      | "none"                | "none"                                     |
      | "LinkSnapshotError"   | "Failed to create SRDF volume from volume" |
      | "MaxSnapSessionError" | "Failed to create SRDF volume from volume" |

  @srdf
  @v2.9.0
  Scenario: Create an SRDF volume with no errors : METRO
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v2.9.0
  Scenario: Idempotent create SRDF volume with no errors : METRO
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v2.9.0
  Scenario: Idempotent create SRDF volume with error in GetRDFDevicePairInfo : METRO
    Given a PowerMax service
    And I induce error "GetSRDFPairInfoError"
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    Then the error contains "Failed to fetch rdf pair information"

  @srdf
  @v2.9.0
  Scenario: Create 2 SRDF volume with no errors : METRO
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "METRO" and RDFGNo 14
    Then a valid CreateVolumeResponse is returned
    And I call RDF enabled CreateVolume "volume2" in namespace "csi-test", mode "METRO" and RDFGNo 14
    Then a valid CreateVolumeResponse is returned

  @srdf
  @v2.9.0
  Scenario Outline: Create a SRDF volume from a snapshot
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call CreateSnapshot "snapshot1" on "volume1"
    And a valid CreateSnapshotResponse is returned
    When I call RDF enabled CreateVolume "volume2" in namespace "test", mode <mode> and RDFGNo 14 from snapshot
    Then a valid CreateVolumeResponse is returned
    Examples:
      | mode    |
      | "ASYNC" |
      | "SYNC"  |
      | "METRO" |

  @srdf
  @v2.9.0
  Scenario Outline: Create a SRDF volume from another volume
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I induce error <induced>
    When I call RDF enabled CreateVolume "volume2" in namespace "test", mode "METRO" and RDFGNo 14 from volume
    Then the error contains <errormsg>
    Examples:
      | induced               | errormsg                                   |
      | "none"                | "none"                                     |
      | "LinkSnapshotError"   | "Failed to create SRDF volume from volume" |
      | "MaxSnapSessionError" | "Failed to create SRDF volume from volume" |

  @srdf
  @v2.9.0
  Scenario Outline: Call GetStorageProtectionGroupStatus with various error
    Given a PowerMax service
    And I induce error <induced>
    When I call GetStorageProtectionGroupStatus with "ASYNC"
    Then the error contains <errormsg>
    Examples:
      | induced                   | errormsg                        |
      | "none"                    | "none"                          |
      | "InvalidSymID"            | "not found"                     |
      | "GetRDFInfoFromSGIDError" | "not formed correctly"          |
      | "GetSRDFInfoError"        | "Failed to Get RDF Info"        |

  @srdf
  @v2.9.0
  Scenario: Call GetStorageProtectionGroupStatus with invalid mode
    Given a PowerMax service
    When I call GetStorageProtectionGroupStatus with "METRO"
    Then the error contains "invalid Replication mode"

  @srdf
  @v2.9.0
  Scenario: Call GetStorageProtectionGroupStatus with mixed state
    Given a PowerMax service
    And I mix the RDF states
    When I call GetStorageProtectionGroupStatus with "ASYNC"
    Then no error was received

  @srdf
  @v2.9.0
  Scenario Outline: Call GetStorageProtectionGroupStatus with different RDF status
    Given a PowerMax service
    And I set current state to <currentState>
    When I call GetStorageProtectionGroupStatus with "ASYNC"
    Then no error was received
    Examples:
      | currentState |
      | "Unknown"    |
      | "SyncInProg" |

  @srdf
  @v2.9.0
  Scenario Outline: Call ExecuteAction with mixed RDF personalities
    Given a PowerMax service
    And I mix the RDF personalities
    When I call ExecuteAction with <action>
    Then the error contains <errormsg>
    Examples:
      | errormsg                     | action                          |
      | "mixed SRDF personalities"   | "FAILBACK_LOCAL"                |
      | "mixed SRDF personalities"   | "FAILOVER_REMOTE"               |
      | "mixed SRDF personalities"   | "REPROTECT_REMOTE"              |
      | "mixed SRDF personalities"   | "SWAP_REMOTE"                   |

  @srdf
  @v2.9.0
  Scenario Outline: Call ExecuteAction with mixed RDF states
    Given a PowerMax service
    And I mix the RDF states
    When I call ExecuteAction with <action>
    Then the error contains <errormsg>
    Examples:
      | errormsg              | action                          |
      | "mixed SRDF states"   | "FAILBACK_LOCAL"                |
      | "mixed SRDF states"   | "FAILOVER_REMOTE"               |
      | "mixed SRDF states"   | "REPROTECT_REMOTE"              |
      | "mixed SRDF states"   | "SWAP_REMOTE"                   |

  @srdf
  @v2.9.0
  Scenario Outline: Call ExecuteAction with various error
      Given a PowerMax service
      And I induce error <induced>
      And I set current state to <currentState>
      When I call ExecuteAction with <action>
      Then the error contains <errormsg>
      Examples:
        | induced              | errormsg                                       | currentState  | action                          |
        | "none"               | "none"                                         | "Suspended"   | "SUSPEND"                       |
        | "GetSRDFInfoError"   | "induced error"                                | "Suspended"   | "SUSPEND"                       |
        | "none"               | "none"                                         | "Consistent"  | "SUSPEND"                       |
        | "ExecuteActionError" | "induced error"                                | "Consistent"  | "SUSPEND"                       |
        | "none"               | "none"                                         | "Suspended"   | "RESUME"                        |
        | "GetSRDFInfoError"   | "induced error"                                | "Suspended"   | "RESUME"                        |
        | "none"               | "none"                                         | "Consistent"  | "RESUME"                        |
        | "ExecuteActionError" | "induced error"                                | "Suspended"   | "RESUME"                        |
        | "none"               | "none"                                         | "Consistent"  | "FAILBACK_LOCAL"                |
        | "GetSRDFInfoError"   | "induced error"                                | "Failed Over" | "FAILBACK_LOCAL"                |
        | "none"               | "none"                                         | "Failed Over" | "FAILBACK_LOCAL"                |
        | "none"               | "none"                                         | "Unknown"     | "FAILBACK_LOCAL"                |
        | "ExecuteActionError" | "Incorrect state to perform failback"          | "Unknown"     | "FAILBACK_LOCAL"                |
        | "none"               | "Can't perform planned failback"               | "Consistent"  | "FAILBACK_REMOTE"               |
        | "ExecuteActionError" | "induced error"                                | "Failed Over" | "FAILBACK_LOCAL"                |
        | "ExecuteActionError" | "induced error"                                | "Consistent"  | "FAILOVER_WITHOUT_SWAP_REMOTE"  |
        | "ExecuteActionError" | "induced error"                                | "Unknown"     | "FAILOVER_WITHOUT_SWAP_REMOTE"  |
        | "none"               | "none"                                         | "Consistent"  | "FAILOVER_LOCAL"                |
        | "none"               | "driver unable to determine next SRDF action"  | "Unknown"     | "FAILOVER_LOCAL"                |
        | "none"               | "none"                                         | "Suspended"   | "FAILOVER_LOCAL"                |
        | "none"               | "none"                                         | "Consistent"  | "FAILOVER_REMOTE"               |
        | "ExecuteActionError" | "induced error"                                | "Consistent"  | "FAILOVER_REMOTE"               |
        | "none"               | "can't perform planned failover"               | "Suspended"   | "FAILOVER_REMOTE"               |
        | "none"               | "can't perform planned failover"               | "Unkownn"     | "FAILOVER_REMOTE"               |
        | "none"               | "none"                                         | "Failed Over" | "FAILOVER_REMOTE"               |
        | "GetSRDFInfoError"   | "induced error"                                | "Failed Over" | "FAILOVER_REMOTE"               |
        | "none"               | "none"                                         | "Failed Over" | "FAILOVER_REMOTE"               |
        | "none"               | "it is already R1"                             | "Consistent"  | "FAILOVER_WITHOUT_SWAP_LOCAL"   |
        | "none"               | "none"                                         | "Consistent"  | "FAILOVER_WITHOUT_SWAP_REMOTE"  |
        | "none"               | "none"                                         | "Failed Over" | "FAILOVER_WITHOUT_SWAP_REMOTE"  |
        | "none"               | "none"                                         | "Unknown"     | "FAILOVER_WITHOUT_SWAP_REMOTE"  |
        | "ExecuteActionError" | "induced error"                                | "Consistent"  | "FAILOVER_REMOTE"               |
        | "GetSRDFInfoError"   | "induced error"                                | "Suspended"   | "REPROTECT_LOCAL"               |
        | "none"               | "none"                                         | "Suspended"   | "REPROTECT_LOCAL"               |
        | "none"               | "Can't reprotect volumes at site which is R2"  | "Suspended"   | "REPROTECT_REMOTE"              |
        | "none"               | "none"                                         | "Consistent"  | "REPROTECT_LOCAL"               |
        | "none"               | "none"                                         | "Unknown"     | "REPROTECT_LOCAL"               |
        | "ExecuteActionError" | "induced error"                                | "Suspended"   | "REPROTECT_LOCAL"               |
        | "none"               | "none"                                         | "Suspended"   | "ESTABLISH"                     |
        | "GetSRDFInfoError"   | "induced error"                                | "Suspended"   | "ESTABLISH"                     |
        | "none"               | "none"                                         | "Consistent"  | "ESTABLISH"                     |
        | "ExecuteActionError" | "induced error"                                | "Suspended"   | "ESTABLISH"                     |
        | "none"               | "none"                                         | "Suspended"   | "SWAP_LOCAL"                    |
        | "none"               | "driver unable to determine next SRDF action"  | "Unknown"     | "SWAP_LOCAL"                    |
        | "GetSRDFInfoError"   | "induced error"                                | "Suspended"   | "SWAP_LOCAL"                    |
        | "none"               | "none"                                         | "Suspended"   | "SWAP_REMOTE"                   |
        | "none"               | "Incorrect SRDF state to perform a Swap"       | "Consistent"  | "SWAP_LOCAL"                    |
        | "ExecuteActionError" | "induced error"                                | "Suspended"   | "SWAP_REMOTE"                   |
        | "none"               | "does not match with supported actions"        | "Consistent"  | "Dance"                         |
        | "InvalidSymID"       | "not found"                                    | "Consistent"  | "SUSPEND"                       |
        | "none"               | "none"                                         | "Consistent"  | "UNPLANNED_FAILOVER_LOCAL"      |
        | "ExecuteActionError" | "induced error"                                | "Consistent"  | "UNPLANNED_FAILOVER_REMOTE"     |
        | "none"               | "none"                                         | "Consistent"  | "UNPLANNED_FAILOVER_REMOTE"     |
