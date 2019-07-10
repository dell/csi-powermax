
# These lists contain applicable files 
srcfiles=		authenticate.go interface.go system.go sloprovisioning.go
integrationfiles=	inttest/pmax_integration_test.go
unitfiles=		unit_test.go unit_steps_test.go

# These variables should be set for your Unisphere installation
Endpoint="https://10.247.73.133:8443"
Username=			# Leave blank for the default username
Password=			# Leave blank for the default password
DefaultStorageGroup="CSI-Integration-Test"
DefaultStoragePool="SRP_1"
VolumePrefix="XX"		# Use a two letter initial sequence to identify your files
SymmetrixID="000197900046"

all: unit-test int-test check

unit-test:
	go clean -cache
	go test -v -coverprofile=c.out $(unitfiles) $(srcfiles)

int-test: 
	Endpoint=$(Endpoint) Username=$(Username) Password=$(Password) DefaultStorageGroup=$(DefaultStorageGroup) \
		 VolumePrefix=$(VolumePrefix) SymmetrixID=$(SymmetrixID) DefaultStoragePool=$(DefaultStoragePool)\
		 go test -v -timeout 20m -coverprofile=c.out -coverpkg github.com/dell/csi-powermax/pmax \
		 $(integrationfiles) 

short-int-test: 
	Endpoint=$(Endpoint) Username=$(Username) Password=$(Password) DefaultStorageGroup=$(DefaultStorageGroup) \
		 VolumePrefix=$(VolumePrefix) SymmetrixID=$(SymmetrixID) \
		 go test -v -short -timeout 20m -coverprofile=c.out -coverpkg github.com/dell/csi-powermax/pmax \
		 $(integrationfiles) 

gocover:
	go tool cover -html=c.out

check:
	gofmt -w $(srcfiles) $(unitfiles) $(integrationfiles)
	golint -set_exit_status
	go vet
