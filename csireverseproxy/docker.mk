# Copyright © 2024 Dell Inc. or its subsidiaries. All Rights Reserved.
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

# docker makefile, included from Makefile, will build/push images with docker or podman
#

# Includes the following generated file to get semantic version information
include semver.mk

ifdef NOTES
	RELNOTE="-$(NOTES)"
else
	RELNOTE=
endif

ifeq ($(PROXY_IMAGETAG),)
PROXY_IMAGETAG="v$(MAJOR).$(MINOR).$(PATCH)$(RELNOTE)"
endif


docker:
	@echo "Base Images is set to: $(BASEIMAGE)"
	@echo "Building: $(REGISTRY)/$(PROXY_IMAGENAME):$(PROXY_IMAGETAG)"
	$(BUILDER) build $(NOCACHE) -t "$(REGISTRY)/$(PROXY_IMAGENAME):$(PROXY_IMAGETAG)" --target $(BUILDSTAGE) --build-arg GOPROXY --build-arg BASEIMAGE=$(BASEIMAGE) --build-arg GOIMAGE=$(DEFAULT_GOIMAGE)  .

docker-no-cache:
	@echo "Building with --no-cache ..."
	@make docker NOCACHE=--no-cache

push:
	@echo "Pushing: $(REGISTRY)/$(PROXY_IMAGENAME):$(PROXY_IMAGETAG)"
	$(BUILDER) push "$(REGISTRY)/$(PROXY_IMAGENAME):$(PROXY_IMAGETAG)"

build-base-image: download-csm-common
	$(eval include csm-common.mk)
	@echo "Building base image from $(DEFAULT_BASEIMAGE) and loading dependencies..."
	./buildubimicro.sh $(DEFAULT_BASEIMAGE)
	@echo "Base image build: SUCCESS"
	$(eval BASEIMAGE=localhost/csipowermax-ubimicro:latest)

download-csm-common:
	curl -O -L https://raw.githubusercontent.com/dell/csm/main/config/csm-common.mk