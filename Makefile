# Copyright © 2020-2026 Dell Inc. or its subsidiaries. All Rights Reserved.
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
include images.mk

all: build

.PHONY: all build clean unit-test bdd-test integration-test gosec go-code-tester mocks dev-images help

# This will be overridden during image build.
IMAGE_VERSION ?= 0.0.0
LDFLAGS = "-X main.ManifestSemver=$(IMAGE_VERSION)"

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

clean:
	rm -f core/core_generated.go go-code-tester *.log *.out cover* semver.mk csm-common.mk
	rm -rf csm-temp-repo vendor
	go clean
	make -C csireverseproxy clean

build: generate
	GOOS=linux CGO_ENABLED=0 go build -mod=vendor -ldflags $(LDFLAGS)

# Run unit tests
unit-test: go-code-tester
	GITHUB_OUTPUT=/dev/null \
	./go-code-tester 85 "." "" "true" "" "" "./core|./k8smock|./test/integration|./pkg/symmetrix/mocks|./pkg/config/mocks"

# Run BDD tests. Need to be root to run as tests require some system access, need to fix
bdd-test:
	(cd service; go clean -cache; CGO_ENABLED=0 go test -run TestGoDog -v -coverprofile=c.out ./...)

# Linux only; populate env.sh with the hardware parameters
integration-test:
	(cd test/integration; sh run.sh)

gosec:
ifeq (, $(shell which gosec))
	go install github.com/securego/gosec/v2/cmd/gosec@latest
	$(shell $(GOBIN)/gosec -quiet -log gosec.log -out=gosecresults.csv -fmt=csv ./...)
else
	$(shell gosec -quiet -log gosec.log -out=gosecresults.csv -fmt=csv ./...)
endif
	@echo "Logs are stored at gosec.log, Outputfile at gosecresults.csv"

go-code-tester:
	git clone --depth 1 git@github.com:dell/actions.git temp-repo
	cp temp-repo/go-code-tester/entrypoint.sh ./go-code-tester
	chmod +x go-code-tester
	rm -rf temp-repo

mocks:
	go generate ./...

# Build images for development with dev tag and outside container build
dev-images: build
	@echo "Building development images with dev tag..."
	$(eval include csm-common.mk)
	@echo "Building: $(IMAGE_REGISTRY)/$(IMAGE_NAME):dev"
	$(BUILDER) build --pull -f Dockerfile.dev -t "$(IMAGE_REGISTRY)/$(IMAGE_NAME):dev" \
		--build-arg BASEIMAGE=$(CSM_BASEIMAGE) \
		--build-arg VERSION="dev" .

# Show help for available targets
help:
	@echo "Available targets:"
	@echo "  build          - Build the Go binary"
	@echo "  images         - Build container images with timestamp tag"
	@echo "  dev-images     - Build container images with 'dev' tag for development (builds binary first, then copies to image)"
	@echo "  images-no-cache- Build container images with --no-cache"
	@echo "  push           - Push container images to registry"
	@echo "  unit-test      - Run unit tests"
	@echo "  bdd-test       - Run BDD tests"
	@echo "  integration-test - Run integration tests"
	@echo "  gosec          - Run security scan"
	@echo "  clean          - Clean build artifacts"
	@echo "  mocks          - Generate mocks"
	@echo "  help           - Show this help message"
