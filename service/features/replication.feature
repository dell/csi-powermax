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
      | "GetRemoteVolumeError"       | "Volume not found"                              |
      | "InvalidRemoteVolumeError"   | "Failed to find protected remote storage group" |
      | "GetSRDFPairInfoError"       | "Could not retrieve pair info"                  |
      | "FetchResponseError"         | "failure checking volume"                       |

  @srdf
  @v1.6.0
  Scenario: DiscoverRemoteVolume with no error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    And I call DiscoverRemoteVolume
    Then no error was received

  @srdf
  @v1.6.0
  Scenario Outline: DiscoverRemoteVolume with different error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call DiscoverRemoteVolume
    Then the error contains <errormsg>
    Examples:
      | induced                      | errormsg                                        |
      | "GetVolumeError"             | "Error retrieving Volume"                       |
      | "GetRemoteVolumeError"       | "Volume not found"                              |
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
  Scenario Outline: DeleteStorageProtectionGroup with different error
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call DeleteStorageProtectionGroup on "csi-rep-sg-csi-test-13-ASYNC"
    Then the error contains <errormsg>
    Examples:
      | induced                           | errormsg                          |
      | "GetProtectedStorageGroupError"   | "none"                            |
      | "RDFGroupHasPairError"            | "it is not empty"                 |
      | "DeleteStorageGroupError"         | "Error deleting storage group"    |
      | "FetchResponseError"              | "GetProtectedStorageGroup failed" |
