# csi-powermax - pmax
This directory contains a lighweight Go wrapper around the Unisphere REST API

## Unit Tests
Unit Tests exist for the wrapper. These tests do not modify the array.

#### Running Unit Tests
To run these tests, from this directory, run:
```
make unit-test
```

## Integration Tests
Integration Tests exist for the wrapper as well. These tests WILL MODIFY the array.

#### Pre-requisites
Before running integration tests, examine the pmax/inttest/pmax_integration_test.go file of
the repository. Within that file, two variables are defined:
* username
* password

Either change those variables to match an existing user in Unisphere, or create
a new user in Unisphere matching those credentials.

#### Running Integration Tests
To run these tests, from the this directory, run:

For full tests:
```
make int-test
```

For an abbreviated set of tests:
```
make short-int-test
```

