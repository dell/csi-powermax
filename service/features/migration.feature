Feature: PowerMax CSI Interface
  As a consumer of the CSI interface
  I want to test migration interfaces
  So that they are known to work

  @migration
  @v1.0.0
  Scenario: VolumeMigrate with no errors
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call VolumeMigrate
    Then a valid VolumeMigrateResponse is returned

  @migration
    @v1.0.0
  Scenario Outline: VolumeMigrate with different migration types with no errors
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And I call VolumeMigrateWithDifferentTypes <type>
    And the error contains <errormsg>
    Examples:
      | type | errormsg                 |
      | ""   | "Unimplemented"          |
      | "a"  | "Unknown Migration Type" |

  @migration
  @v1.0.0
  Scenario: VolumeMigrateReplToNonRepl with no errors
    Given a PowerMax service
    And I call RDF enabled CreateVolume "volume1" in namespace "csi-test", mode "ASYNC" and RDFGNo 13
    And a valid CreateVolumeResponse is returned
    And I call VolumeMigrateReplToNonRepl
    Then a valid VolumeMigrateResponse is returned

  @migration
    @v1.0.0
  Scenario Outline: Volume migrate with induced errors
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    Then I induce error <induced>
    And I call VolumeMigrate
    Then the error contains <errormsg>
    Examples:
      | induced                   | errormsg |
      | "CreateStorageGroupError" | "Error creating protected storage group"    |

  @migration
    @v1.0.0
  Scenario Outline: VolumeMigrateWithParams
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And ICallWithParamsVolumeMigrate <repMode> <sg> <appPrefix>
    And the error contains <errormsg>
    Examples:
      | repMode  | sg              | appPrefix      | errormsg                       |
      | "METRO"  | "CSI-Test-SG-1" | "000197900046" | "Unsupported Replication Mode" |
      | "123123" | "CSI-Test-SG-1" | "000197900046" | "Unsupported Replication Mode" |
      | "ASYNC"  | ""              | "000197900046" | "none"                         |
      | "ASYNC"  | ""              | ""             | "none"                         |

  @migration
    @v1.0.0
  Scenario Outline: VolumeMigrateWithVolID
    Given a PowerMax service
    And I call CreateVolume "volume1"
    And a valid CreateVolumeResponse is returned
    And ICallWithVolIDVolumeMigrate <volID>
    And the error contains <errormsg>
    Examples:
      | volID                                    | errormsg                  |
      | "csi-TST-volume1-000197900046-531379167" | "Could not find volume"   |
      | "volume1"                                | "Invalid volume id"       |
      
  @migration
    @v1.0.0
  Scenario Outline: ArrayMigrate
    Given a PowerMax service
    And I call ArrayMigrate with <actionvalue>
    And the error contains <errormsg>
    Examples:
      | actionvalue                            | errormsg                                                        |
      | "csimgr.ActionTypes_MG_MIGRATE"        | "failed to create array migration environment for target array" |
      | "csimgr.ActionTypes_MG_COMMIT"         | "Not Found"                                                     |
      | ""                                     | "Invalid action"                                                |

  @migration
    @v1.0.0
  Scenario Outline: ArrayMigrate
    Given a PowerMax service
    And I call ArrayMigrate with <actionvalue>
    Then I induce error <induced1>
    Then I induce error <induced2>
    And the error contains <errormsg>
    Examples:
      | induced1                                           | induced2                                             | actionvalue                            | errormsg                                                        |
      | "GetOrCreateMigrationEnvironmentNoError"           | "StorageGroupMigrationError"                         | "csimgr.ActionTypes_MG_MIGRATE"        | "failed to create array migration environment for target array" |
      | "GetOrCreateMigrationEnvironmentError"             | "none"                                               | "csimgr.ActionTypes_MG_MIGRATE"        | "failed to create array migration environment for target array" |
      | ""                                                 | ""                                                   | ""                                     | "Invalid action"                                                |
      | "StorageGroupCommitNoError"                        | "AddVolumesToRemoteSGNoError"                        | "csimgr.ActionTypes_MG_COMMIT"         | "Not Found"                                                     |
      | "StorageGroupCommitNoError"                        | "AddVolumesToRemoteSGError"                          | "csimgr.ActionTypes_MG_COMMIT"         | "Not Found"                                                     |
      | "StorageGroupCommitError"                          | "none"                                               | "csimgr.ActionTypes_MG_COMMIT"         | "Not Found"                                                     |