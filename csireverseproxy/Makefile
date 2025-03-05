# Copyright © 2020-2025 Dell Inc. or its subsidiaries. All Rights Reserved.
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

run:
	@./run.sh

format:
	@gofmt -w -s .

clean:
	go clean

build:
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build

docker: build
	go run ../core/semver/semver.go -f mk >semver.mk
	make -f docker.mk docker

docker-no-cache:
	make -f docker.mk docker-no-cache

docker-push: docker
	make -f docker.mk push

unit-test:
	go test -v -coverprofile c1.out ./...

download-csm-common:
	curl -O -L https://raw.githubusercontent.com/dell/csm/main/config/csm-common.mk
