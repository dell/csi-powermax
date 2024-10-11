all: clean build

# Tag parameters
MAJOR=1
MINOR=0
PATCH=0
NOTES=-beta
TAGMSG="CSI Spec 1.0"

check:
	@./check.sh

format:
	@gofmt -w -s .

clean:
	rm -f core/core_generated.go
	go clean

build:
	@./check.sh
	go generate
	GOOS=linux CGO_ENABLED=0 go build

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
	make -f docker.mk docker

# Pushes container to the repository
push:	docker
	make -f docker.mk push

# Windows or Linux; requires no hardware
unit-test:
	( cd service; go clean -cache; go test -v -coverprofile=c.out ./... )

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
	gosec -quiet -log gosec.log -out=gosecresults.csv -fmt=csv ./...
