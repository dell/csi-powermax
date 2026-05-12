# Copyright © 2020-2026 Dell Inc. or its subsidiaries. All Rights Reserved.
#
# Dell Technologies, Dell and other trademarks are trademarks of Dell Inc.
# or its subsidiaries. Other trademarks may be trademarks of their respective 
# owners.
#

include images.mk

run:
	@./run.sh

clean:
	rm -f cover.out coverage.txt
	rm -rf vendor csm-common.mk csm-temp-repo
	go clean

build: vendor
	GOOS=linux CGO_ENABLED=0 go build -mod=vendor

unit-test:
	go test -v -coverprofile c1.out ./...
