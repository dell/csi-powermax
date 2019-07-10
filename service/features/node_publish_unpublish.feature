Feature: PowerMax CSI interface
	As a consumer of the CSI interface
	I want to test list service methods
	So that they are known to work


@nodePublish
@v1.0.0
  Scenario Outline: Node publish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call Probe
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |
    | "mount"      | "single-writer"                | "ext4"     | "none"                                       |
    | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"    |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "block"      | "multiple-writer"              | "none"     | "none"                                       |


@nodePublish
@v1.0.0
  Scenario Outline: Node publish block volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "block" access "single-writer" fstype "none"
    And get Node Publish Volume Request
    And I induce error <error>
    When I call Probe
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | error                                   | errormsg                                                          |
    | "GOFSMockBindMountError"                | "none"                                                            |
    | "GOFSMockMountError"                    | "mount induced error"                                             |
    | "NoDeviceWWNError"                      | "Device WWN required to be in PublishContext"                     |
    | "GOFSMockGetMountsError"                | "could not reliably determine existing mount status"              |
    | "NoSymlinkForNodePublish"               | "cannot find the path specified@@no such file or directory@@none"  |
    # may be different for Windows vs. Linux
    | "NoBlockDevForNodePublish"              | "is not a block device@@no such file or directory"                |
    | "TargetNotCreatedForNodePublish"        | "none"                                                            |
    # may be different for Windows vs. Linux
    | "PrivateDirectoryNotExistForNodePublish"| "cannot find the path specified@@no such file or directory@@none"  |
    | "BlockMkfilePrivateDirectoryNodePublish"| "existing path is not a directory"                                |
    | "NodePublishNoTargetPath"               | "Target Path is required"                                         |
    | "NodePublishNoVolumeCapability"         | "Volume Capability is required"                                   |
    | "NodePublishNoAccessMode"               | "Volume Access Mode is required"                                  |
    | "NodePublishNoAccessType"               | "Volume Access Type is required"                                  |
    | "NodePublishBlockTargetNotFile"         | "existing path is a directory"                                    |

@nodePublish
@v1.0.0
  Scenario Outline: Node publish mount volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "mount" access "single-writer" fstype "xfs"
    And get Node Publish Volume Request
    And I induce error <errora>
    And I induce error <errorb>
    When I call Probe
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | errora                                  | errorb                        | errormsg                                                                  |
    | "GOFSMockDevMountsError"                | "none"                        | "none"                                                                    |
    | "GOFSWWNToDevicePathError"              | "none"                        | "Unable to find device after multiple discovery attempts"                 |
    | "GOFSWWNToDevicePathError"              | "GOISCSIDiscoveryError"       | "Unable to find device after multiple discovery attempts"                 |
    | "GOFSWWNToDevicePathError"              | "GOISCSIRescanError"          | "Unable to find device after multiple discovery attempts"                 |
    | "GOFSMockMountError"                    | "none"                        | "mode conflicts with existing mounts@@mount induced error"                |
    | "GOFSMockGetMountsError"                | "none"                        | "could not reliably determine existing mount status"                      |
    # may be different for Windows vs. Linux
    | "NoSymlinkForNodePublish"               | "none"                        | "cannot find the path specified@@no such file or directory@@none"          |
    # may be different for Windows vs. Linux
    | "NoBlockDevForNodePublish"              | "none"                        | "is not a block device@@not published to node@@no such file or directory" |
    | "TargetNotCreatedForNodePublish"        | "none"                        | "none"                                                                    |
    # may be different for Windows vs. Linux
    | "PrivateDirectoryNotExistForNodePublish"| "none"                        | "cannot find the path specified@@no such file or directory@@none"          |
    | "BlockMkfilePrivateDirectoryNodePublish"| "none"                        | "existing path is not a directory"                                        |
    | "NodePublishNoTargetPath"               | "none"                        | "Target Path is required"                                                 |
    | "NodePublishNoVolumeCapability"         | "none"                        | "Volume Capability is required"                                           |
    | "NodePublishNoAccessMode"               | "none"                        | "Volume Access Mode is required"                                          |
    | "NodePublishNoAccessType"               | "none"                        | "Volume Access Type is required"                                          |
    | "NodePublishFileTargetNotDir"           | "none"                        | "existing path is not a directory"                                        |
    | "BadVolumeIdentifier"                   | "none"                        | "volume identifier malformed"                                             |


@nodePublish
@v1.0.0
  Scenario Outline: Node publish various use cases from examples when volume already published
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call Probe
    And I call NodePublishVolume
    And I change the target path
    And I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                             |
    | "block"      | "single-writer"                | "none"     | "access mode conflicts with existing mounts"         |
    | "block"      | "multiple-writer"              | "none"     | "none"                                               |
# The following line seems like the wrong behavior; shouldn't this be allowed?
    | "block"      | "multiple-reader"              | "none"     | "access mode conflicts with existing mounts"         |
    | "mount"      | "single-writer"                | "xfs"      | "access mode conflicts with existing mounts"         |
    | "mount"      | "single-writer"                | "ext4"     | "access mode conflicts with existing mounts"         |
    | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"            |

@nodePublish
@v1.0.0
  Scenario Outline: Node idempotent publish volume already published to same target
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call Probe
    And I call NodePublishVolume
    And I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                             |
    | "block"      | "multiple-writer"              | "none"     | "none"                                               |

@nodePublish
@v1.0.0
  Scenario Outline: Node publish various use cases from examples when read-only mount volume already published
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    And get Node Publish Volume Request
    And I mark request read only
    When I call Probe
    And I call NodePublishVolume
    And I change the target path
    And I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "mount"      | "single-reader"                | "none"     | "none"                                       |
    | "mount"      | "single-reader"                | "xfs"      | "none"                                       |
    | "mount"      | "multiple-reader"              | "ext4"     | "Invalid access mode"                        |
    | "mount"      | "single-writer"                | "ext4"     | "access mode conflicts with existing mounts" |
    | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"    |
    | "block"      | "multiple-reader"              | "none"     | "read only not supported for Block Volume"   |


@nodePublish
@v1.0.0
  Scenario Outline: Node Unpublish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call Probe
    And I call NodePublishVolume
    And I call NodeUnpublishVolume
    And there are no remaining mounts
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "block"      | "multiple-writer"              | "none"     | "none"                                       |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |

@nodePublish
@v1.0.0
  Scenario Outline: Idempotent Node Unpublish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call Probe
    And I call NodePublishVolume
    And I call NodeUnpublishVolume
    And I call NodeUnpublishVolume
    And there are no remaining mounts
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "block"      | "multiple-writer"              | "none"     | "none"                                       |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |

@nodePublish
@v1.0.0
  Scenario Outline: Node Unpublish mount volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "mount" access "single-writer" fstype "xfs"
    And get Node Publish Volume Request
    When I call Probe
    And I call NodePublishVolume
    And I induce error <error>
    And I call NodeUnpublishVolume
    Then the error contains <errormsg>

    Examples:
    | error                                   | errormsg                                                    |
    | "NodeUnpublishBadVolume"                | "Volume cannot be found"                                    |
    | "GOFSMockGetMountsError"                | "could not reliably determine existing mount status"        |
    | "NodeUnpublishNoTargetPath"             | "target path required"                                      |
    | "GOFSMockUnmountError"                  | "Error unmounting target"                                   |
    | "PrivateDirectoryNotExistForNodePublish"| "none"                                                      |
    | "BadVolumeIdentifier"                   | "volume identifier malformed"                               |
    | "GOFSWWNToDevicePathError"              | "none"                                                      |
    | "GOFSRmoveBlockDeviceError"             | "they weren't successfully deleted"                         |

@nodePublish
@v1.0.0
  Scenario Outline: Node publish with a failed NodeProbe
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    When I invalidate the NodeID
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call NodePublishVolume
    Then the error contains "Error getting NodeName from the environment"

  Examples:
    | voltype      | access                         | fstype     |
    | "mount"      | "single-writer"                | "xfs"      |

@nodePublish
@v1.0.0
  Scenario Outline: GetTargetsForMaskingView with various induced error
    Given a PowerMax service
    When I call Probe
    And I request a PortGroup
    And I have a Node "node1" with MaskingView
    And I induce error <induced>
    And I call GetTargetsForMaskingView
    Then the error contains <errormsg>
    And the result has <numports> ports

    Examples:
    | induced                   | errormsg                                       | numports  |
    | "NoArray"                 | "No array specified"                           | "0"       |
    | "GetPortError"            | "none"                                         | "0"       |
    | "none"                    | "none"                                         | "2"       |
    | "GetPortGroupError"       | "Error retrieving Port Group"                  | "0"       |
