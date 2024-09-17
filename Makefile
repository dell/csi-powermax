# Copyright © 2020-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#      http://www.apache.org/licenses/LICENSE-2.0
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.
#
all: clean build

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

# Tag parameters
MAJOR=1
MINOR=0
PATCH=0
NOTES=-beta
TAGMSG="CSI Spec 1.6"

check:
	GOLINT=$(GOLINT) bash check.sh --all

format:
	@gofmt -w -s .

clean:
	rm -f core/core_generated.go
	go clean

build: golint check
	go generate
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build

install:
	go generate
	GOOS=linux CGO_ENABLED=0 go install

# Tags the release with the Tag parameters set above
tag:
	-git tag -d v$(MAJOR).$(MINOR).$(PATCH)$(NOTES)
	git tag -a -m $(TAGMSG) v$(MAJOR).$(MINOR).$(PATCH)$(NOTES)

# Generates the docker container (but does not push)
docker:
	go generate
	go run core/semver/semver.go -f mk >semver.mk
	sh ./build.sh -i ubimicro -e -o

# Generates the docker container without cache (but does not push)
docker-no-cache:
	go generate
	go run core/semver/semver.go -f mk >semver.mk
	sh ./build.sh -i ubimicro -e -o -n

# Pushes container to the repository
push:	docker
	make -f docker.mk push

# Run unit tests and skip the BDD tests
unit-test: golint check
	( cd service; go clean -cache; CGO_ENABLED=0 GO111MODULE=on go test -skip TestGoDog -v -coverprofile=c.out ./... )

# Run BDD tests. Need to be root to run as tests require some system access, need to fix
bdd-test: golint check
	( cd service; go clean -cache; CGO_ENABLED=0 GO111MODULE=on go test -run TestGoDog -v -coverprofile=c.out ./... )

# Linux only; populate env.sh with the hardware parameters
integration-test:
	( cd test/integration; sh run.sh )

release:
	BUILD_TYPE="R" $(MAKE) clean build docker push

version:
	go generate
	go run core/semver/semver.go -f mk >semver.mk
	make -f docker.mk version

gosec:
ifeq (, $(shell which gosec))
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	$(shell $(GOBIN)/gosec -quiet -log gosec.log -out=gosecresults.csv -fmt=csv ./...)
else
	$(shell gosec -quiet -log gosec.log -out=gosecresults.csv -fmt=csv ./...)
endif
	@echo "Logs are stored at gosec.log, Outputfile at gosecresults.csv"

golint:
ifeq (, $(shell which golint))
	@{ \
	set -e ;\
	GOLINT_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$GOLINT_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install golang.org/x/lint/golint@latest ;\
	rm -rf $$GOLINT_GEN_TMP_DIR ;\
	}
GOLINT=$(GOBIN)/golint
else
GOLINT=$(shell which golint)
endif
