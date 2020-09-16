
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

