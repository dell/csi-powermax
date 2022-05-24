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
      | volID                                    | errormsg            |
      | "csi-TST-volume1-000197900046-531379167" | "Volume not found"  |
      | "volume1"                                | "Invalid volume id" |

