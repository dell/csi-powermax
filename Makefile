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
include overrides.mk

all: build unit-test

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

format:
	@gofmt -w -s .

clean:
	rm -f core/core_generated.go go-code-tester *.log *.out cover* 
	go clean
	make -C csireverseproxy clean

build:
	go generate
	CGO_ENABLED=0 go build

# Generates the docker container (but does not push)
docker:
	go generate
	go run core/semver/semver.go -f mk >semver.mk
	make -f docker.mk docker
	# build the reverseproxy container as part of this target
	( cd csireverseproxy; make docker )


# Generates the docker container without cache (but does not push)
docker-no-cache:
	go generate
	go run core/semver/semver.go -f mk >semver.mk
	make -f docker.mk docker-no-cache
	# build the reverseproxy container as part of this target
	( cd csireverseproxy; make docker-no-cache )

# Pushes container to the repository
push:	docker
	make -f docker.mk push

# Run unit tests
unit-test: go-code-tester
	GITHUB_OUTPUT=/dev/null \
	./go-code-tester 85 "." "" "true" "" "" "./core|./k8smock|./test/integration|./pkg/symmetrix/mocks|./pkg/config/mocks"

# Run BDD tests. Need to be root to run as tests require some system access, need to fix
bdd-test:
	( cd service; go clean -cache; CGO_ENABLED=0 go test -run TestGoDog -v -coverprofile=c.out ./... )

# Linux only; populate env.sh with the hardware parameters
integration-test:
	( cd test/integration; sh run.sh )

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

.PHONY: actions action-help
actions: ## Run all GitHub Action checks that run on a pull request creation
	@echo "Running all GitHub Action checks for pull request events..."
	@act -l | grep -v ^Stage | grep pull_request | grep -v image_security_scan | awk '{print $$2}' | while read WF; do \
		echo "Running workflow: $${WF}"; \
		act pull_request --no-cache-server --platform ubuntu-latest=ghcr.io/catthehacker/ubuntu:act-latest --job "$${WF}"; \
	done

go-code-tester:
	curl -o go-code-tester -L https://raw.githubusercontent.com/dell/common-github-actions/main/go-code-tester/entrypoint.sh \
	&& chmod +x go-code-tester

action-help: ## Echo instructions to run one specific workflow locally
	@echo "GitHub Workflows can be run locally with the following command:"
	@echo "act pull_request --no-cache-server --platform ubuntu-latest=ghcr.io/catthehacker/ubuntu:act-latest --job <jobid>"
	@echo ""
	@echo "Where '<jobid>' is a Job ID returned by the command:"
	@echo "act -l"
	@echo ""
	@echo "NOTE: if act is not installed, it can be downloaded from https://github.com/nektos/act"
