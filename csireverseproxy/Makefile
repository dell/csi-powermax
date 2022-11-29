# Copyright © 2020 Dell Inc. or its subsidiaries. All Rights Reserved.
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
REGISTRY?="localhost:5000/csipowermax-reverseproxy"
VERSION="latest"

coverpackages= revproxy,revproxy/pkg/config,revproxy/pkg/linkedproxy,revproxy/pkg/standaloneproxy

run:
	@./run.sh

format:
	@gofmt -w -s .

clean:
	go clean

check:
	@./check.sh

build:	check
	CGO_ENABLED=0 GOOS=linux GO111MODULE=on go build

docker-build: build
	docker build -t "$(REGISTRY):$(VERSION)" .

docker-push: docker-build
	docker push "$(REGISTRY):$(VERSION)"

unit-test:
	go test -v -coverprofile c.out -coverpkg $(coverpackages) ./...

