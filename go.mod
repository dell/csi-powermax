module github.com/dell/csi-powermax

go 1.13

replace github.com/coreos/go-systemd => github.com/coreos/go-systemd/v22 v22.0.0

require (
	github.com/DATA-DOG/godog v0.7.13
	github.com/container-storage-interface/spec v1.1.0
	github.com/coreos/go-systemd v0.0.0-20190612170431-362f06ec6bc1
	github.com/dell/gobrick v1.0.0
	github.com/dell/gofsutil v1.3.0
	github.com/dell/goiscsi v1.2.0
	github.com/dell/gopowermax v1.2.0
	github.com/gogo/protobuf v1.2.0 // indirect
	github.com/golang/protobuf v1.3.1
	github.com/rexray/gocsi v1.1.0
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.5.1
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9
	golang.org/x/lint v0.0.0-20200302205851-738671d3881b // indirect
	golang.org/x/net v0.0.0-20200822124328-c89045814202
	golang.org/x/tools v0.0.0-20200916150407-587cf2330ce8 // indirect
	google.golang.org/grpc v1.19.0
)
