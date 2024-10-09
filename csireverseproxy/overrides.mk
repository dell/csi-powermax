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

# overrides file
# this file, included from the Makefile, will overlay default values with environment variables
#

# DEFAULT values
# ubi9/ubi-micro:9.2-13
DEFAULT_GOIMAGE=$(shell sed -En 's/^go (.*)$$/\1/p' go.mod)
DEFAULT_REGISTRY="sample_registry"
DEFAULT_IMAGENAME="csipowermax-reverseproxy"
DEFAULT_BUILDSTAGE="final"
DEFAULT_IMAGETAG=""

# set the GOIMAGE if needed
ifeq ($(GOIMAGE),)
export GOIMAGE="$(DEFAULT_GOIMAGE)"
endif

# set the REGISTRY if needed
ifeq ($(REGISTRY),)
export REGISTRY="$(DEFAULT_REGISTRY)"
endif

# set the PROXY_IMAGENAME if needed
ifeq ($(PROXY_IMAGENAME),)
export PROXY_IMAGENAME="$(DEFAULT_IMAGENAME)"
endif

#set the PROXY_IMAGETAG if needed
ifneq ($(DEFAULT_IMAGETAG), "") 
export PROXY_IMAGETAG="$(DEFAULT_IMAGETAG)"
endif

# set the BUILDSTAGE if needed
ifeq ($(BUILDSTAGE),)
export BUILDSTAGE="$(DEFAULT_BUILDSTAGE)"
endif

# figure out if podman or docker should be used (use podman if found)
ifneq (, $(shell which podman 2>/dev/null))
export BUILDER=podman
else
export BUILDER=docker
endif

# target to print some help regarding these overrides and how to use them
overrides-help:
	@echo
	@echo "The following environment variables can be set to control the build"
	@echo
	@echo "GOIMAGE   - The version of Go to build with, default is: $(DEFAULT_GOIMAGE)"
	@echo "              Current setting is: $(GOIMAGE)"
	@echo "REGISTRY    - The registry to push images to, default is: $(DEFAULT_REGISTRY)"
	@echo "              Current setting is: $(REGISTRY)"
	@echo "PROXY_IMAGENAME   - The image name to be built, defaut is: $(DEFAULT_IMAGENAME)"
	@echo "              Current setting is: $(PROXY_IMAGENAME)"
	@echo "PROXY_IMAGETAG    - The image tag to be built, default is an empty string which will determine the tag by examining annotated tags in the repo."
	@echo "              Current setting is: $(PROXY_IMAGETAG)"
	@echo "BUILDSTAGE  - The Dockerfile build stage to execute, default is: $(DEFAULT_BUILDSTAGE)"
	@echo "              Stages can be found by looking at the Dockerfile"
	@echo "              Current setting is: $(BUILDSTAGE)"
	@echo
        
	
