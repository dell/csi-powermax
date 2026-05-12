Feature: PowerMax CSI Interface
    As a consumer of the CSI interface
    I want to test snapshot interfaces
    So that they are known to work
@v2.17.0
    Scenario: 10.4 Create a volume clone with same size
        Given a PowerMax service
        And a 104 array
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And a valid volume with size of 54614 CYL
        When I call Create Volume from Volume
        Then a valid CreateVolumeResponse is returned

@v2.17.0
    Scenario: 10.4 Idempotent volume clone
        Given a PowerMax service
        And a 104 array
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And a valid volume with size of 54614 CYL
        When I call Create Volume from Volume
        Then a valid CreateVolumeResponse is returned
        When I call Create Volume from Volume
        Then a valid CreateVolumeResponse is returned

@v2.17.0
    Scenario: 10.4 Create a volume clone without specifying a source
        Given a PowerMax service
        And a 104 array
        And I induce error "NoVolumeSource"
        And I call Create Volume from Volume
        Then the error contains "VolumeContentSource is missing volume and snapshot source"

@v2.17.0
    Scenario: 10.4 Create a volume clone with non-existent source
        Given a PowerMax service
        And a 104 array
        And I induce error "NonExistentVolume"
        And I call Create Volume from Volume
        Then the error contains "Volume content source couldn't be found in the array"

@v2.17.0
    Scenario: 10.4 Create a volume clone from invalid volume
        Given a PowerMax service
        And a 104 array
        And I induce error "InvalidVolumeID"
        And I call Create Volume from Volume
        Then the error contains "not in supported format"

@v2.17.0
    Scenario: 10.4 Create a volume clone with smaller capacity
        Given a PowerMax service
        And a 104 array
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I induce error "WrongCapacity"
        And I call Create Volume from Volume
        Then the error contains "Requested capacity is smaller than the source"

@v2.17.0
    Scenario: 10.4 Create a volume clone with larger capacity succeeds
        Given a PowerMax service
        And a 104 array
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And a larger capacity than the source
        And I call Create Volume from Volume
        Then a valid CreateVolumeResponse is returned

@v2.17.0
    Scenario: 10.4 Create a volume clone with CreateVolume error
        Given a PowerMax service
        And a 104 array
        And I call CreateVolume "volume1"
        And a valid CreateVolumeResponse is returned
        And I induce error "CreateVolumeError"
        And I call Create Volume from Volume
        Then the error contains "Failed to create volume"