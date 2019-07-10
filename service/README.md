# csi-powermax - service
This directory contains the CSI Driver source code

## Unit Tests
Unit Tests exist for the CSI Driver. These tests do not modify the array or 
require a Kubernetes instance.

#### Running Unit Tests
To run these tests, from the root directory of the repository, run:
```
make unit-test
```

## Integration Tests
Integration Tests exist for the CSI Driver. These tests do not require a
Kubernetes instance but WILL MODIFY the array

#### Pre-requisites
Before running integration tests, examine the env.sh script in the root of
the repository. Within that file, two variables are defined:
* X_CSI_POWERMAX_USER
* X_CSI_POWERMAX_PASSWORD

Either change those variables to match an existing user in Unisphere, or create
a new user in Unisphere matching those credentials.

#### Running Integration Tests
To run these tests, from the root directory of the repository, run:
```
make integration-test
```

