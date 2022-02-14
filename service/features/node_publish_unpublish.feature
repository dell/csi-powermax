Feature: PowerMax CSI interface
	As a consumer of the CSI interface
	I want to test list service methods
	So that they are known to work


@nodePublish
@v1.0.0
  Scenario Outline: Node publish various use cases from examples
    Given a PowerMax service
    And I set transport protocol to <transport>
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | transport  | voltype      | access                         | fstype     | errormsg                                     |
    | "FC"       | "mount"      | "single-writer"                | "xfs"      | "none"                                       |
    | "FC"       | "mount"      | "single-writer"                | "ext4"     | "none"                                       |
    | "FC"       | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"    |
    | "FC"       | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "FC"       | "block"      | "multiple-writer"              | "none"     | "none"                                       |
    | "ISCSI"    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |
    | "ISCSI"    | "mount"      | "single-writer"                | "ext4"     | "none"                                       |
    | "ISCSI"    | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"    |
    | "ISCSI"    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "ISCSI"    | "block"      | "multiple-writer"              | "none"     | "none"                                       |
    | "FC"       | "mount"      | "single-node-single-writer"    | "none"     | "none"                                       |
    | "FC"       | "mount"      | "single-node-multi-writer"     | "none"     | "none"                                       |
    | "ISCSI"    | "mount"      | "single-node-single-writer"    | "none"     | "none"                                       |
    | "ISCSI"    | "mount"      | "single-node-multi-writer"     | "none"     | "none"                                       |

@nodePublish
@v1.0.0
  Scenario Outline: Node stage block volume various induced error use cases from examples
    Given a PowerMax service
    And I set transport protocol to <transport>
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "block" access "single-writer" fstype "none"
    And get Node Publish Volume Request
    And I induce error <error>
    When I call NodeStageVolume
    Then the error contains <errormsg>

    Examples:
    | transport | error                                   | errormsg                                                          |
    | "FC"      | "InduceOverloadError"                   | "overload"                                                        |
    | "FC"      | "TargetNotCreatedForNodePublish"        | "none"                                                            |
    | "FC"      | "BadVolumeIdentifier"                   | "bad volume identifier" |
    | "FC"      | "UnspecifiedNodeName"                   | "Error getting NodeName from the environment"                     |
    | "FC"      | "NodePublishNoTargetPath"               | "Target Path is required"                                         |
    | "FC"      | "GobrickConnectError"                   | "induced ConnectVolumeError"                                      |
    | "ISCSI"   | "TargetNotCreatedForNodePublish"        | "none"                                                            |
    | "ISCSI"   | "BadVolumeIdentifier"                   | "bad volume identifier" |
    | "ISCSI"   | "UnspecifiedNodeName"                   | "Error getting NodeName from the environment"                     |
    | "ISCSI"   | "NodePublishNoTargetPath"               | "Target Path is required"                                         |
    | "ISCSI"   | "GobrickConnectError"                   | "induced ConnectVolumeError"                                      |


@nodePublish
@v1.0.0
  Scenario Outline: Node publish block volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "block" access "single-writer" fstype "none"
    And get Node Publish Volume Request
    And I induce error <error>
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | error                                   | errormsg                                                          |
    | "GOFSMockBindMountError"                | "none"                                                            |
    | "UnspecifiedNodeName"                   | "Error getting NodeName from the environment"                     |
    | "GOFSMockMountError"                    | "error bind mounting to target path"                              |
    | "NoDeviceWWNError"                      | "Device WWN required to be in PublishContext"                     |
    | "GOFSMockGetMountsError"                | "Could not getDevMounts for"                                      |
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
  Scenario Outline: Node publish block volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "block" access "single-node-single-writer" fstype "none"
    And get Node Publish Volume Request
    And I induce error <error>
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | error                                   | errormsg                                                          |
    | "GOFSMockBindMountError"                | "none"                                                            |
    | "UnspecifiedNodeName"                   | "Error getting NodeName from the environment"                     |
    | "GOFSMockMountError"                    | "error bind mounting to target path"                              |
    | "NoDeviceWWNError"                      | "Device WWN required to be in PublishContext"                     |
    | "GOFSMockGetMountsError"                | "Could not getDevMounts for"                                      |
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
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | errora                                  | errorb                        | errormsg                                                                  |
    | "GOFSMockDevMountsError"                | "none"                        | "none"                                                                    |
    | "GOFSWWNToDevicePathError"              | "none"                        | "Device path not found for WWN"                                           |
    | "GOFSWWNToDevicePathError"              | "GOISCSIDiscoveryError"       | "Device path not found for WWN"                                           |
    | "GOFSWWNToDevicePathError"              | "GOISCSIRescanError"          | "Device path not found for WWN"                                           |
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
    | "NodePublishRequestReadOnly"            | "none"                        | "none"                                                                    |
    | "BadVolumeIdentifier"                   | "none"                        | "bad volume identifier"                                             |
    | "PrivMountAlreadyMounted"               | "none"                        | "none"                                                                    |
    | "PrivMountByDifferentDev"               | "none"                        | "mounted by different device"                                             |
    | "PrivMountByDifferentDev"               | "GOFSMockGetMountsError"      | "could not reliably determine existing mount status"                      |
    | "PrivMountByDifferentDir"               | "none"                        | "device already in use and mounted elsewhere"                             |
    | "MountTargetAlreadyMounted"             | "none"                        | "none"                                                                    |
    | "MountTargetAlreadyMounted"             | "NodePublishRequestReadOnly"  | "volume previously published with different mount options"                |


@nodePublish
@v1.0.0
  Scenario Outline: Node publish mount volumes various induced error use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "mount" access "single-node-single-writer" fstype "xfs"
    And get Node Publish Volume Request
    And I induce error <errora>
    And I induce error <errorb>
    When I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | errora                                  | errorb                        | errormsg                                                                  |
    | "GOFSMockDevMountsError"                | "none"                        | "none"                                                                    |
    | "GOFSWWNToDevicePathError"              | "none"                        | "Device path not found for WWN"                                           |
    | "GOFSWWNToDevicePathError"              | "GOISCSIDiscoveryError"       | "Device path not found for WWN"                                           |
    | "GOFSWWNToDevicePathError"              | "GOISCSIRescanError"          | "Device path not found for WWN"                                           |
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
    | "NodePublishRequestReadOnly"            | "none"                        | "none"                                                                    |
    | "BadVolumeIdentifier"                   | "none"                        | "bad volume identifier"                                             |
    | "PrivMountAlreadyMounted"               | "none"                        | "none"                                                                    |
    | "PrivMountByDifferentDev"               | "none"                        | "mounted by different device"                                             |
    | "PrivMountByDifferentDev"               | "GOFSMockGetMountsError"      | "could not reliably determine existing mount status"                      |
    | "PrivMountByDifferentDir"               | "none"                        | "device already in use and mounted elsewhere"                             |
    | "MountTargetAlreadyMounted"             | "none"                        | "none"                                                                    |
    | "MountTargetAlreadyMounted"             | "NodePublishRequestReadOnly"  | "volume previously published with different mount options"                |

@nodePublish
@v1.0.0
  Scenario Outline: Node publish various use cases from examples when volume already published
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    And I call NodePublishVolume
    And I change the target path
    And I call NodePublishVolume
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                             |
    | "block"      | "single-writer"                | "none"     | "Access mode conflicts with existing mounts"         |
    | "block"      | "multiple-writer"              | "none"     | "none"                                               |
    | "block"      | "multiple-reader"              | "none"     | "none"                                               |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                               |
    | "mount"      | "single-writer"                | "ext4"     | "none"                                               |
    | "mount"      | "multiple-writer"              | "ext4"     | "Mount volumes do not support AccessMode"            |
    | "mount"      | "single-node-single-writer"    | "none"     | "none"					        |
    | "mount"      | "single-node-multiple-writer"  | "none"     | "Unknown Access Mode"                                |
    | "block"      | "single-node-single-writer"    | "none"     | "Access mode conflicts with existing mounts"         |
    | "block"      | "single-node-multiple-writer"  | "none"     | "Unknown Access Mode"                                |

@nodePublish
@v1.0.0
  Scenario Outline: Node idempotent publish volume already published to same target
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
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
    | "mount"      | "single-node-single-writer"    | "none"     | "access mode conflicts with existing mounts" |
    | "mount"      | "single-node-multiple-writer"  | "none"     | "Unknown Access Mode"                        |


@nodePublish
@v1.1.0
  Scenario Outline: Node Unstage various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
    And get Node Publish Volume Request
    And I call NodeStageVolume
    And I call NodePublishVolume
    And I call NodeUnpublishVolume
    And I call NodeUnstageVolume
    And there are no remaining mounts
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | fstype     | errormsg                                     |
    | "block"      | "single-writer"                | "none"     | "none"                                       |
    | "block"      | "multiple-writer"              | "none"     | "none"                                       |
    | "mount"      | "single-writer"                | "xfs"      | "none"                                       |

@nodePublish
@v1.1.0
  Scenario Outline: Node Unstage with various induced errors
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype "mount" access "single-writer" fstype "xfs"
    And get Node Publish Volume Request
    And I call NodeStageVolume
    And I call NodePublishVolume
    And I call NodeUnpublishVolume
    And get Node Publish Volume Request
    And I induce error <induced>
    And I induce error <induced2>
    And I call NodeUnstageVolume
    Then the error contains <errormsg>

    Examples:
    | induced                    | induced2                   | errormsg                                            |
    | "none"                     | "none"                     | "none"                                              |
    | "InduceOverloadError"      | "none"                     | "overload"                                          |
    | "GOFSMockUnmountError"     | "none"                     | "none"                             |
    | "GobrickDisconnectError"   | "none"                     | "disconnectVolume exceeded retry limit"             |
    | "NodeUnpublishNoTargetPath"| "none"                     | "Staging Target argument is required"               |
    | "InvalidVolumeID"          | "none"                     | "badVolumeID"                           |
#    | "InvalidVolumeID"          | "InvalidateNodeID"         | "Error getting NodeName from the environment"       |

@nodePublish
@v1.0.0
  Scenario Outline: Node Unpublish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
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
  Scenario Outline: Multipath node Unpublish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published multipath volume
    And a capability with voltype <voltype> access <access> fstype "none"
    And get Node Publish Volume Request
    And I call NodeStageVolume
    And I call NodePublishVolume
    And I induce error <error>
    And I call NodeUnpublishVolume
    And I call NodeUnstageVolume
    And there are no remaining mounts
    Then the error contains <errormsg>

    Examples:
    | voltype      | access                         | error                            | errormsg                                     |
    | "block"      | "single-writer"                | "none"                           | "none"                                       |
    | "block"      | "multiple-writer"              | "none"                           | "none"                                       |
    | "mount"      | "single-writer"                | "none"                           | "none"                                       |

@nodePublish
@v1.0.0
  Scenario Outline: Idempotent Node Unpublish various use cases from examples
    Given a PowerMax service
    And I have a Node "node1" with MaskingView
    And a controller published volume
    And a capability with voltype <voltype> access <access> fstype <fstype>
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
    And I call NodePublishVolume
    And I induce error <error>
    And I call NodeUnpublishVolume
    Then the error contains <errormsg>

    Examples:
    | error                                   | errormsg                                                    |
    # The spec says should return Volume Not Found, but Kubernetes will loop forever trying to Unpublish it, so treat it idempotently.
    #| "NodeUnpublishBadVolume"                | "Volume cannot be found"                                    |
    | "NodeUnpublishBadVolume"                | "none"                                                      |
    | "GOFSMockGetMountsError"                | "could not reliably determine existing mount status"        |
    | "NodeUnpublishNoTargetPath"             | "target path required"                                      |
    | "GOFSMockUnmountError"                  | "Error unmounting target"                                   |
    | "PrivateDirectoryNotExistForNodePublish"| "none"                                                      |
    | "GOFSWWNToDevicePathError"              | "none"                                                      |

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

@nodePublish
@v1.0.0
   Scenario Outline: UnmountPrivMount
     Given a PowerMax service
     And a private mount <mnta>
     And a private mount <mntb>
     And I induce error <induced>
     When I call unmountPrivMount
     Then the error contains <errormsg>
     And lastUnmounted should be <lastUnmounted>

     Examples:
     | mnta                   | mntb                    | induced                           | lastUnmounted | errormsg                             |
     | "none"                 | "none"                  | "none"                            | "true"        | "none"                               |
     | "test/mnt1"            | "none"                  | "none"                            | "true"        | "none"                               |
     | "test/mnt1"            | "none"                  | "GOFSMockGetMountsError"          | "false"       | "getMounts induced error"            |
     | "test/mnt1"            | "none"                  | "GOFSMockUnmountError"            | "false"       | "unmount induced error"              |
     | "test/mnt1"            | "test/mnt2"             | "GOFSMockGetMountsError"          | "false"       | "getMounts induced error"            |
     | "test/mnt1"            | "test/mnt2"             | "none"                            | "false"       | "none"                               |


@v1.1.0
  Scenario Outline: Call verifyAndUpdateInitiatorsInADiffHost in various scenarios without modifying host name
    Given a PowerMax service
    And I induce error <induced>
    And I have a Node <node1> with Host
    When I call verifyAndUpdateInitiatorsInADiffHost for node <node2>
    Then <nvalid> valid initiators are returned
    And the error contains <errormsg>

    Examples:
    |node1          | node2                    | nvalid          | induced                                  | errormsg                                            |
    |"host1"        | "host2"                  | 0               | "none"                                   | "is already a part of a different host"             |
    |"host1"        | "host1"                  | 1               | "none"                                   | "none"                                              |
    |"host1"        | "host1"                  | 0               | "GetInitiatorByIDError"                  | "none"                                              |

@v1.4.0
  Scenario Outline: Call verifyAndUpdateInitiatorsInADiffHost in various scenarios with modifying host name
    Given a PowerMax service
    And I induce error <induced>
    And I set ModifyHostName to <modify>
    And I have a NodeNameTemplate <template>
    And I have a Node <node1> with Host
    When I call verifyAndUpdateInitiatorsInADiffHost for node <node2>
    Then <nvalid> valid initiators are returned
    And the error contains <errormsg>

    Examples:
    |node1          | node2     | nvalid   | modify   | template                  | induced                   | errormsg                                            |
    |"host1"        | "host2"   | 0        | false    | "temp-csi-%host1%"        | "none"                    | "is already a part of a different host"             |
    |"host1"        | "host1"   | 1        | false    | "temp-csi-%host1%"        | "none"                    | "none"                                              |
    |"host1"        | "host1"   | 0        | false    | "temp-csi-%host1%"        | "GetInitiatorByIDError"   | "none"                                              |
    |"host1"        | "host2"   | 0        | false    | "temp.!csi-%host1%"       | "none"                    | "is already a part of a different host"             |
    |"host1"        | "host2"   | 1        | true     | "temp-csi-%host1%"        | "none"                    | "none"                                              |
    |"host1"        | "host2"   | 0        | true     | "temp-csi-%host1%"        | "UpdateHostError"         | "Error updating Host"                               |
    |"host1"        | "host2"   | 1        | true     | "temp-csi-%host1"         | "none"                    | "none"                                              |
    |"host1"        | "host2"   | 1        | true     | "temp.!csi-%host1%"       | "none"                    | "none"                                              |
    
@v1.4.0
  Scenario Outline: Test buildHostIDFromTemplate
    Given a PowerMax service
    And I have a NodeNameTemplate <template>
    When I call buildHostIDFromTemplate for node <node>
    Then the error contains <errormsg>

    Examples:
    | template             | node     | errormsg                 |
    | "temp.!csi-%host%"   | "host1"  | "not acceptable"         |
    | "-temp.!csi-%host%"  | "host1"  | "not acceptable"         |
    | "0temp.csi-%host%"   | "host1"  | "not acceptable"         |
    | "?temp-csi-%host%"   | "host1"  | "not acceptable"         |
    | "-csi-/-%host%"      | "host1"  | "not acceptable"         |
    | "temp-csi-%host"     | "host1"  | "not formed correctly"   |
    | "%host%"             | "host1"  | "none"                   |
    | "0csi-%host%_node"   | "host1"  | "none"                   |
    | "%host%-csi-__"      | "host1"  | "none"                   |
