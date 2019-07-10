Feature: PMAX Client library
    As a developer of the PMAX CSI driver that uses the PMAX REST client library
    I want to test the client library methods
    So they are known to work and have a high test coverage.

    Scenario Outline: Authenticate test cases
      When I induce error <induced> 
      And I call authenticate with endpoint <endpoint> credentials <credentials>
      Then the error message contains <errormsg>

      Examples:
      | endpoint    | credentials    | induced         | errormsg                    |
      | "mockurl"   | "good"         | "none"          | "none"                      |
      | "mockurl"   | "bad"          | "none"          | "Unauthorized"              |
      | "badurl"    | "good"         | "none"          | "connect"                   | 
      | "nilurl"    | "good"         | "none"          | "Endpoint must be supplied" |
      | "mockurl"   | "good"         | "httpStatus500" | "Internal Error"            |
      | "mockurl"   | "good"         | "InvalidJSON"   | "invalid character"         |

    Scenario Outline: TestCases for GetSymmetrixIDList
      Given a valid connection
      And I induce error <induced>
      When I call GetSymmetrixIDList
      Then the error message contains <errormsg>
      And I get a valid Symmetrix ID List if no error

      Examples:
      | induced               | errormsg                      |
      | "none"                | "none"                        |
      | "GetSymmetrixError"   | "induced error"               |

    Scenario Outline: Get Symmetrix System
      Given a valid connection
      And I induce error <induced>
      When I call GetSymmetrixByID <id>
      Then the error message contains <errormsg>
      And I get a valid Symmetrix Object if no error

      Examples:
      | id              | induced               | errormsg                    |
      | "000197900046"  | "none"                | "none"                      |
      | "000000000000"  | "none"                | "not found"                 |
      | "000197900046"  | "GetSymmetrixError"   | "induced error"             |
      | "000197900046"  | "httpStatus500"       | "Internal Error"            |
      | "000197900046"  | "InvalidJSON"         | "invalid character"         |

    Scenario Outline: Test cases for GetVolumeIDList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have <nvols> volumes
      And I induce error <induced>
      When I call GetVolumeIDList <volume_identifier> 
      Then the error message contains <errormsg>
      And I get a valid VolumeIDList with <vols> if no error
     
      Examples:                # volumes are numbered 1...n  Vol00001, Vol00002, ...
      | nvols      | vols  | volume_identifier | induced                    | errormsg                      | whitelist |
      | 7          | 7     | ""                | "none"                     | "none"                        | ""        |
      | 10         | 10    | ""                | "none"                     | "none"                        | ""        |
      | 11         | 11    | ""                | "none"                     | "none"                        | ""        |
      | 23         | 23    | ""                | "none"                     | "none"                        | ""        |
      | 23         | 23    | ""                | "GetVolumeIteratorError"   | "induced error"               | ""        |
      | 23         | 23    | ""                | "httpStatus500"            | "Internal Error"              | ""        |
      | 23         | 23    | ""                | "InvalidJSON"              | "invalid character"           | ""        |
      | 23         | 1     | "Vol00005"        | "none"                     | "none"                        | ""        |
      | 23         | 1     | "Vol00015"        | "none"                     | "none"                        | ""        |
      | 23         | 1     | "Vol00015"        | "none"                     | "none"                        | ""        |
      | 23         | 0     | "ABCDEFGH"        | "none"                     | "none"                        | ""        |
      | 23         | 9     | "<like>Vol0000"   | "none"                     | "none"                        | ""        |
      | 23         | 10    | "<like>Vol0001"   | "none"                     | "none"                        | ""        |
      | 23         | 4     | "<like>Vol0002"   | "none"                     | "none"                        | ""        |
      | 5          | 5     | ""                | "none"                     | "ignored via a whitelist"     | "ignore"  |

    Scenario Outline: Test cases for GetVolumeByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have 5 volumes
      And I induce error <induced>
      When I call GetVolumeByID <id>
      Then the error message contains <errormsg>
      And I get a valid Volume Object <id> if no error

      Examples:
      | id              | induced               | errormsg                      | whitelist |
      | "00001"         | "none"                | "none"                        | ""        |
      | "00003"         | "none"                | "none"                        | ""        |
      | "00010"         | "none"                | "cannot be found"             | ""        |
      | "00001"         | "GetVolumeError"      | "induced error"               | ""        |
      | "00001"         | "httpStatus500"       | "Internal Error"              | ""        |
      | "00001"         | "InvalidJSON"         | "invalid character"           | ""        |
      | "00001"         | "none"                | "ignored via a whitelist"     | "ignored" |

    Scenario Outline: Test cases for GetStorageGroupIDList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call GetStorageGroupIDList
      Then the error message contains <errormsg>
      And I get a valid StorageGroupIDList if no errors

      Examples:
      | induced               | errormsg                      | whitelist |
      | "none"                | "none"                        | ""        |
      | "GetStorageGroupError"| "induced error"               | ""        |
      | "httpStatus500"       | "Internal Error"              | ""        |
      | "InvalidJSON"         | "invalid character"           | ""        |
      | "none"                | "ignored via a whitelist"     | "ignored" |
      | "InvalidResponse"     | "EOF"                         | ""        |

    Scenario Outline: Test cases for GetStorageGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call GetStorageGroup <name>
      Then the error message contains <errormsg>
      And I get a valid StorageGroup if no errors

      Examples:
      | name               | induced               | errormsg                      | whitelist |
      | "CSI-Test-SG-1"    | "none"                | "none"                        | ""        |
      | "CSI-Test-SG-1"    | "GetStorageGroupError"| "induced error"               | ""        |
      | "CSI-Test-SG-1"    | "httpStatus500"       | "Internal Error"              | ""        |
      | "CSI-Test-SG-1"    | "InvalidJSON"         | "invalid character"           | ""        |
      | "CSI-Test-SG-1"    | "none"                | "ignored via a whitelist"     | "ignored" |
      | "CSI-Test-SG-1"    | "InvalidResponse"     | "EOF"                         | ""        |

    Scenario Outline: Test cases for GetStoragePool
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call GetStoragePool <name>
      Then the error message contains <errormsg>
      And I get a valid GetStoragePool if no errors

      Examples:
      | name     | induced               | errormsg                      | whitelist |
      | "SRP_1"  | "none"                | "none"                        | ""        |
      | "SRP_1"  | "GetStoragePoolError" | "induced error"               | ""        |
      | "SRP_1"  | "httpStatus500"       | "Internal Error"              | ""        |
      | "SRP_1"  | "InvalidJSON"         | "invalid character"           | ""        |
      | "SRP_1"  | "none"                | "ignored via a whitelist"     | "ignored" |

    Scenario Outline: Test cases for GetJobIDList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      And I have <njobs> jobs
      When I call GetJobIDList with <status>
      Then the error message contains <errormsg>
      And I get a valid JobsIDList with <njobs> if no errors

      Examples:
      | njobs         | induced                     | status       | errormsg                        | whitelist |
      | 1             | "none"                      | ""           | "none"                          | ""        |
      | 0             | "none"                      | ""           | "none"                          | ""        |
      | 20            | "none"                      | ""           | "none"                          | ""        |
      | 20            | "none"                      | "SCHEDULED"  | "none"                          | ""        |
      | 1             | "GetJobError"               | ""           | "induced error"                 | ""        |
      | 20            | "httpStatus500"             | ""           | "Internal Error"                | ""        |
      | 20            | "InvalidJSON"               | ""           | "invalid character"             | ""        |
      | 1             | "none"                      | ""           | "ignored via a whitelist"       | "ignored" |

    Scenario Outline: Test cases for GetJobByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      And I create a job with initial state <initial> and final state <final>
      When I call GetJobByID
      And the error message contains <errormsg>
      And I get a valid Job with state <initial> if no error
      Then I call GetJobByID
      And I get a valid Job with state <final> if no error

      Examples:
      | initial        | final            | induced                       | errormsg                  | whitelist |
      | "RUNNING"      | "SUCCEEDED"      | "none"                        | "none"                    | ""        |
      | "RUNNING"      | "FAILED"         | "none"                        | "none"                    | ""        |
      | "RUNNING"      | "RUNNING"        | "none"                        | "none"                    | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "GetJobError"                 | "induced error"           | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "httpStatus500"               | "Internal Error"          | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "InvalidJSON"                 | "invalid character"       | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "GetJobCannotFindRoleForUser" | "none"                    | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "none"                        | "ignored via a whitelist" | "ignored" |

    Scenario Outline: Test cases WaitOnJobCompletion
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      And I create a job with initial state <initial> and final state <final>
      When I call WaitOnJobCompletion
      Then the error message contains <errormsg>
      And I get a valid Job with state <final> if no error

      Examples:
      | initial        | final            | induced          | errormsg                  | whitelist |
      | "RUNNING"      | "SUCCEEDED"      | "none"           | "none"                    | ""        |
      | "RUNNING"      | "FAILED"         | "none"           | "none"                    | ""        |
      | "RUNNING"      | "RUNNING"        | "none"           | "timed out after"         | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "GetJobError"    | "induced error"           | ""        |
      | "RUNNING"      | "SUCCEEDED"      | "none"           | "ignored via a whitelist" | "ignored" |

    Scenario Outline: Test cases for CreateVolumeInStorageGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call CreateVolumeInStorageGroup with name <volname> and size <size>
      Then the error message contains <errormsg>
      And I get a valid Volume with name <volname> if no error

      Examples:
      | volname                                                                        | size     | induced                   | errormsg                                               | whitelist |
      | "IntgA"                                                                        | 1        | "none"                    | "none"                                                 | ""        |
      | "IntgB"                                                                        | 5        | "none"                    | "none"                                                 | ""        |
      | "IntgC"                                                                        | 1        | "UpdateStorageGroupError" | "A job was not returned from UpdateStorageGroup"       | ""        |
      | "IntgD"                                                                        | 1        | "httpStatus500"           | "A job was not returned from UpdateStorageGroup"       | ""        |
      | "IntgE"                                                                        | 1        | "GetJobError"             | "induced error"                                        | ""        |
      | "IntgF"                                                                        | 1        | "JobFailedError"          | "The UpdateStorageGroup job failed"                    | ""        |
      | "IntgG"                                                                        | 1        | "GetVolumeError"          | "Failed to find newly created volume with name: IntgG" | ""        |
      | "IntgH"                                                                        | 1        | "VolumeNotCreatedError"   | "Failed to find newly created volume with name: IntgH" | ""        |
      | "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxy"| 1        | "none"                    | "Length of volumeName exceeds max limit"               | ""        |
      | "abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk"              | 1        | "none"                    | "none"                                                 | ""        |
      | "IntgA"                                                                        | 1        | "none"                    | "ignored via a whitelist"                              | "ignored" |

    Scenario Outline: Test cases for Remove Volume From Storage Group
      Given a valid connection
      And I call CreateVolumeInStorageGroup with name "IntM" and size 1
      And I induce error <induced>
      And I have a whitelist of <whitelist>
      When I call RemoveVolumeFromStorageGroup
      Then the error message contains <errormsg>
      And the volume is no longer a member of the Storage Group if no error

      Examples:
      | induced                   | errormsg                                         | whitelist |
      | "none"                    | "none"                                           | ""        |
      | "UpdateStorageGroupError" | "induced error"                                  | ""        |
      | "none"                    | "ignored via a whitelist"                        | "ignored" |

    Scenario Outline: Test cases for Rename Volume
      Given a valid connection
      And I call CreateVolumeInStorageGroup with name "IntN" and size 1
      And I induce error <induced>
      And I have a whitelist of <whitelist>
      When I call RenameVolume with <newname>
      Then the error message contains <errormsg>
      And I get a valid Volume with name <newname> if no error

      Examples:
      | newname              | induced                   | errormsg                                         | whitelist |
      | "Renamed"            | "none"                    | "none"                                           | ""        |               
      | "Renamed"            | "UpdateVolumeError"       | "induced error"                                  | ""        |
      | "Renamed"            | "none"                    | "ignored via a whitelist"                        | "ignored" |         

      Scenario Outline: Test cases for Initiate Deallocation of Tracks
      Given a valid connection
      And I call CreateVolumeInStorageGroup with name "IntO" and size 1
      And I induce error <induced>
      And I have a whitelist of <whitelist>
      When I call InitiateDeallocationOfTracksFromVolume
      Then the error message contains <errormsg>
      And I get a valid Job with state "RUNNING" if no error

      Examples:
      | induced                   | errormsg                                         | whitelist |
      | "none"                    | "none"                                           | ""        |               
      | "UpdateVolumeError"       | "induced error"                                  | ""        |
      | "none"                    | "ignored via a whitelist"                        | "ignored" |

    Scenario Outline: Test cases for Delete Volume
      Given a valid connection
      And I call CreateVolumeInStorageGroup with name "IntP" and size 1
      And I induce error <induced>
      And I have a whitelist of <whitelist>
      When I call DeleteVolume
      Then the error message contains <errormsg>

      Examples:
      | induced                   | errormsg                                         | whitelist |
      | "none"                    | "none"                                           | ""        |               
      | "DeleteVolumeError"       | "induced error"                                  | ""        |
      | "none"                    | "ignored via a whitelist"                        | "ignored" |

    Scenario Outline: Test cases for CreateStorageGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call CreateStorageGroup with name <sgname> and srp <srp> and sl <sl>
      Then the error message contains <errormsg>
      And I get a valid StorageGroup with name <sgname> if no error

      Examples:
      | sgname               | srp      | sl           | induced                    | errormsg                                              | whitelist |
      | "CSI-Test-New-SG1"   | "SRP_1"  | "Diamond"    | "none"                     | "none"                                                | ""        |
      | "CSI-Test-New-SG1"   | "None"   | "Diamond"    | "none"                     | "none"                                                | ""        |
      | "CSI-Test-New-SG2"   | "SRP_1"  | "Optimized"  | "none"                     | "none"                                                | ""        |
      | "CSI-Test-New-SG2"   | "SRP_1"  | "Optimized"  | "StorageGroupAlreadyExists"| "The requested storage group resource already exists" | ""        |
      | "CSI-Test-New-SG3"   | "SRP_1"  | "Diamond"    | "CreateStorageGroupError"  | "induced error"                                       | ""        |
      | "CSI-Test-New-SG4"   | "SRP_1"  | "Diamond"    | "httpStatus500"            | "Internal Error"                                      | ""        |
      | "CSI-Test-New-SG1"   | "SRP_1"  | "Diamond"    | "none"                     | "ignored via a whitelist"                             | "ignored" |
      | "CSI-Test-New-SG1"   | "SRP_1"  | "Diamond"    | "InvalidResponse"          | "EOF"                                                 | ""        |

    Scenario Outline: Test DeleteStorageGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call DeleteStorageGroup <name>
      Then the error message contains <errormsg>

      Examples:
      | induced                        | name                  | errormsg                           | whitelist |
      | "none"                         | "CSI-Test-SG-2"       | "none"                             | ""        |
      | "DeleteStorageGroupError"      | "CSI-Test-SG-3"       | "induced error"                    | ""        |
      | "none"                         | "CSI-Test-SG-3"       |"ignored via a whitelist"           | "ignored" |

    Scenario Outline: Test GetStoragePoolList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call GetStoragePoolList
      Then the error message contains <errormsg>
      And I get a valid StoragePoolList if no error

      Examples:
      | induced                        | errormsg                                              | whitelist |
      | "none"                         | "none"                                                | ""        |
      | "GetStoragePoolListError"      | "induced error"                                       | ""        |
      | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test GetMaskingViewList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a MaskingView <mvname>
      And I induce error <induced>
      When I call GetMaskingViewList
      Then the error message contains <errormsg>
      And I get a valid MaskingViewList if no error

      Examples:
      | induced                        | errormsg                              | mvname            | whitelist |
      | "none"                         | "none"                                | "CSI-Test-MV"     | ""        |
      | "GetMaskingViewError"          | "induced error"                       | "CSI-Test-MV"     | ""        |
      | "none"                         | "ignored via a whitelist"             | "CSI-Test-MV"     | "ignored" |

    Scenario Outline: Test GetMaskingViewByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a MaskingView <mvname>
      And I induce error <induced>
      When I call GetMaskingViewByID <mvname>
      Then the error message contains <errormsg>
      And I get a valid MaskingView if no error

      Examples:
      | mvname                | induced                        | errormsg                                              | whitelist |
      | "Test-MV"             | "none"                         | "none"                                                | ""        |
      | "Test-MV"             | "GetMaskingViewError"          | "induced error"                                       | ""        |
      | "Test-MV"             | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test DeleteMaskingView
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a MaskingView <mvname>
      And I induce error <induced>
      When I call DeleteMaskingView
      Then the error message contains <errormsg>

      Examples:
      | induced                        | errormsg                     | mvname                 | whitelist |
      | "none"                         | "none"                       | "CSI-Test-MV"          | ""        |
      | "DeleteMaskingViewError"       | "induced error"              | "CSI-Test-MV"          | ""        |
      | "none"                         | "ignored via a whitelist"    | "CSI-Test-MV"          | "ignored" |

    Scenario Outline: Test GetPortGroupList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a PortGroup
      And I induce error <induced>
      When I call GetPortGroupList
      Then the error message contains <errormsg>
      And I get a valid PortGroupList if no error

      Examples:
      | induced                        | errormsg                                              | whitelist |
      | "none"                         | "none"                                                | ""        |
      | "GetPortGroupError"            | "induced error"                                       | ""        |
      | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test GetPortGroupByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a PortGroup
      And I induce error <induced>
      When I call GetPortGroupByID
      Then the error message contains <errormsg>
      And I get a valid PortGroup if no error

      Examples:
      | induced                        | errormsg                                              | whitelist |
      | "none"                         | "none"                                                | ""        |
      | "GetPortGroupError"            | "induced error"                                       | ""        |
      | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test GetHostList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Host <hostname>
      And I induce error <induced>
      When I call GetHostList
      Then the error message contains <errormsg>
      And I get a valid HostList if no error

      Examples:
      | hostname     | induced                        | errormsg                                              | whitelist |
      | "Test-Host"  | "none"                         | "none"                                                | ""        |
      | "Test-Host"  | "GetHostError"                 | "induced error"                                       | ""        |
      | "Test-Host"  | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test GetHostByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Host <hostname>
      And I induce error <induced>
      When I call GetHostByID <hostname>
      Then the error message contains <errormsg>
      And I get a valid Host if no error

      Examples:
      | hostname     | induced                        | errormsg                                              | whitelist |
      | "Test-Host"  | "none"                         | "none"                                                | ""        |
      | "Test-Host"  | "GetHostError"                 | "induced error"                                       | ""        |
      | "Test-Host"  | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test CreateHost
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call CreateHost <hostname>
      Then the error message contains <errormsg>
      And I get a valid Host if no error

      Examples:
      | hostname       | induced                        | errormsg                                              | whitelist |
      | "Test-Host"    | "none"                         | "none"                                                | ""        |
      | "Test-Host"    | "CreateHostError"              | "induced error"                                       | ""        |
      | "Test-Host"    | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test UpdateHost
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call CreateHost <hostname>
      And I get a valid Host if no error
      When I call UpdateHost
      Then the error message contains <errormsg>
      And I get a valid Host if no error

      Examples:
      | hostname       | induced                        | errormsg                                              | whitelist |
      | "Test-Host"    | "none"                         | "none"                                                | ""        |
      | "Test-Host"    | "UpdateHostError"              | "induced error"                                       | ""        |
      | "Test-Host"    | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test DeleteHost
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call CreateHost <hostname>
      And I get a valid Host if no error
      When I call DeleteHost <hostname>
      Then the error message contains <errormsg>

      Examples:
      | hostname            | induced                        | errormsg                                              | whitelist |
      | "Test-Host"         | "none"                         | "none"                                                | ""        |
      | "Test-Host"         | "DeleteHostError"              | "induced error"                                       | ""        |
      | "Test-Host"         | "none"                         | "ignored via a whitelist"                             | "ignored" |

     Scenario Outline: Test GetInitiatorList
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Initiator
      And I induce error <induced>
      When I call GetInitiatorList
      Then the error message contains <errormsg>
      And I get a valid InitiatorList if no error

      Examples:
      | induced                        | errormsg                                              | whitelist |
      | "none"                         | "none"                                                | ""        |
      | "GetInitiatorError"            | "induced error"                                       | ""        |
      | "none"                         | "ignored via a whitelist"                             | "ignored" |

     Scenario Outline: Test GetInitiatorList with filters
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Initiator
      When I call GetInitiatorList with filters
      Then the error message contains <errormsg>

      Examples:
      |errormsg          | whitelist |
      | "none"           | ""        |

    Scenario Outline: Test GetInitiatorByID
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Initiator
      And I induce error <induced>
      When I call GetInitiatorByID
      Then the error message contains <errormsg>
      And I get a valid Initiator if no error

      Examples:
      | induced                        | errormsg                                              | whitelist |
      | "none"                         | "none"                                                | ""        |
      | "GetInitiatorError"            | "induced error"                                       | ""        |
      | "none"                         | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test cases for CreateMaskingViewWithHost
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a Host <hostname>
      And I have a PortGroup
      And I have a StorageGroup <sgname>
      And I induce error <induced>
      When I call CreateMaskingViewWithHost <mvname>
      Then the error message contains <errormsg>
      And I get a valid MaskingView if no error

      Examples:
      | hostname     | sgname      | mvname         | induced                      | errormsg                                              | whitelist |
      | "TestHost"   | "TestSG"    | "TestMV"       | "none"                       | "none"                                                | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "CreateMaskingViewError"     | "Failed to create masking view"                       | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "MaskingViewAlreadyExists"   | "The requested masking view resource already exists"  | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "PortGroupNotFoundError"     | "Port Group on Symmetrix cannot be found"             | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "InitiatorGroupNotFoundError"| "Initiator Group on Symmetrix cannot be found"        | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "StorageGroupNotFoundError"  | "Storage Group on Symmetrix cannot be found"          | ""        |
      | "TestHost"   | "TestSG"    | "TestMV"       | "none"                       | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test cases for CreateMaskingViewWithHostGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a HostGroup <hostname>
      And I have a PortGroup
      And I have a StorageGroup <sgname>
      And I induce error <induced>
      When I call CreateMaskingViewWithHostGroup <mvname>
      Then the error message contains <errormsg>
      And I get a valid MaskingView if no error
      Examples:
      | hostname     | sgname      | mvname         | induced                      | errormsg                                              | whitelist |
      | "TestHostGrp"| "TestSG"    | "TestMV"       | "none"                       | "none"                                                | ""        |
      | "TestHostGrp"| "TestSG"    | "TestMV"       | "InitiatorGroupNotFoundError"| "Initiator Group on Symmetrix cannot be found"        | ""        |
      | "TestHostGrp"| "TestSG"    | "TestMV"       | "none"                       | "ignored via a whitelist"                             | "ignored" |

    Scenario Outline: Test cases for AddVolumesToStorageGroup
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I have a StorageGroup <sgname>
      And I have <nvols> volumes
      And I induce error <induced>
      When I call AddVolumesToStorageGroup <sgname>
      Then the error message contains <errormsg>
      And then the Volumes are part of StorageGroup if no error
      Examples:
      | nvols | sgname        |induced                   | errormsg                                                 | whitelist |
      | 5     | "TestSG"      |"none"                    | "none"                                                   | ""        |
      | 1     | "TestSG"      |"none"                    | "none"                                                   | ""        |
      | 0     | "TestSG"      |"none"                    | "At least one volume id has to be specified"             | ""        |
      | 5     | "TestSG"      |"VolumeNotAddedError"     | "A job was not returned from UpdateStorageGroup"         | ""        |
      | 3     | "TestSG"      |"UpdateStorageGroupError" | "A job was not returned from UpdateStorageGroup"         | ""        |
      | 1     | "TestSG"      |"JobFailedError"          | "The UpdateStorageGroup job failed"                      | ""        |
      | 1     | "TestSG"      |"GetJobError"             | "induced error"                                          | ""        |
      | 1     | "TestSG"      |"none"                    | "ignored via a whitelist"                                | "ignored" |

    Scenario Outline: Test case for retriving list of target IP addresses
      Given a valid connection
      And I have a whitelist of <whitelist>
      And I induce error <induced>
      When I call GetListOfTargetAddresses
      Then the error message contains <errormsg>
      And I recieve <count> IP addresses
      Examples:
      | count | induced                   | errormsg                                                 | whitelist |
      | 8     | "none"                    | "none"                                                   | ""        |
      | 0     | "GetPortError"            | "Error retrieving Port"                                  | ""        |
      | 0     | "GetDirectorError"        | "Error retrieving Director"                              | ""        |
      | 0     | "none"                    | "ignored via a whitelist"                                | "ignored" |

    Scenario Outline: Test Array whitelists
      Given a valid connection
      And I have a whitelist of <whitelist>
      And it contains <count> arrays
      And should include <included>
      And should not include <excluded>
      Examples:
      | whitelist     | count | included       | excluded     |
      | ""            | 0     | "1,2,3"        | ""           |
      | "1"           | 1     | "1"            | "2"          |
      | "1,2,3,4"     | 4     | "1,2,3,4"      | "8,9"        |

    Scenario Outline: TestCases for GetSymmetrixIDList with a Whitelist
      Given a valid connection
      And I have a whitelist of <whitelist>
      When I call GetSymmetrixIDList
      Then I get a valid Symmetrix ID List that contains <included> and does not contains <excluded>
      Examples:
      | whitelist                     | included                       | excluded         | explanation                                      |
      | ""                            | "000197802104, 000197900046"   | ""               | an empty whitelist will allow any array          |
      | "000197900046"                | "000197900046"                 | "000197802104"   | including one specific array will exclude others |
      | "000197802104, 999999999999"  | "000197802104"                 | "999999999999"   | make sure that non existent arrays are not found |

    Scenario Outline: Get Symmetrix System with a Whitelist
      Given a valid connection
      And I have a whitelist of <whitelist>
      When I call GetSymmetrixByID <id>
      Then the error message contains <errormsg>
      And I get a valid Symmetrix Object if no error
      Examples:
      | id              | whitelist             | errormsg                    |
      | "000197900046"  | ""                    | "none"                      |
      | "000000000000"  | "none"                | "ignored via a whitelist"   |
      | "000197900046"  | "000197900046"        | "none"                      |
      | "000197900046"  | "000197802104"        | "ignored via a whitelist"   |
