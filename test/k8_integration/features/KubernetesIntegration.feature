Feature: Powermax Kubernetes CSI interface
  As a consumer of the CSI interface
  I want to run a system test in Kubernetes
  So that I know the service functions correctly.

@current
 Scenario: Create basic volume
   Given Kubernetes session is active
   When I call Create Volume
   Then verify volumes are created
   And I validate volume details against Unisphere
   When I call DeleteVolume
   Then verify volumes are deleted

@current
  Scenario: Create volume with volume name
    Given Kubernetes session is active
    When I call Create Volume with Name "vol1"
    Then verify volumes are created
    And I validate volume details against Unisphere
    When I call DeleteVolume
    Then verify volumes are deleted

@current
  Scenario: Create volume with volume name and size 
    Given Kubernetes session is active
    When I call Create Volume with Name and Size "vol1" "10Gi"
    Then verify volumes are created
    And I validate volume details against Unisphere
    When I call DeleteVolume
    Then verify volumes are deleted


@current
  Scenario Outline: Create volume with different sizes
    Given Kubernetes session is active
    When I call Create Volume with Different Size <size>
    Then verify volumes are created
    And I validate volume details against Unisphere
    When I call DeleteVolume
    Then verify volumes are deleted

    Examples:
    | size        |
    | "1Gi"       |
    | "10Gi"      |
    | "100Gi"     |
    | "49Mi"       |
    | "50Mi"      |
    | "100Mi"     |
    | "1Ti"       |

@current
  Scenario Outline: Create volume with different SL
    Given Kubernetes session is active
    When I call Create Volume with SL Option <sl>
    Then verify volumes are created
    And I validate volume details against Unisphere
    When I call DeleteVolume
    Then verify volumes are deleted

    Examples:
    | sl          |
    | "Silver"    |
    | "Platinum"  |
    | "Bronze"    |
    | "Optimized" |
    | "Diamond"   |
    | "Gold"      |

@current
  Scenario Outline: Create multiple volumes
    Given Kubernetes session is active
    When I call Create multiple volumes <numberOfVolumes>
    Then verify volumes are created
    And I validate volume details against Unisphere
    When I call DeleteVolume
    Then verify volumes are deleted

    Examples:
    | numberOfVolumes |
    | 2               |
    | 5               |
    | 10              |
    | 50              |
    | 100             |

@current
  Scenario: Create volume with Invalid SLO
    Given Kubernetes session is active
    When I call Create Volume with SL Option "Iron"
    Then verify volumes are not created

