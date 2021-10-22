module github.com/dell/csi-powermax/v2

go 1.16

replace github.com/coreos/go-systemd => github.com/coreos/go-systemd/v22 v22.0.0

require (
	github.com/container-storage-interface/spec v1.5.0
	github.com/coreos/go-systemd v0.0.0-20190612170431-362f06ec6bc1
	github.com/cucumber/godog v0.10.0
	github.com/cucumber/messages-go/v10 v10.0.3
	github.com/dell/dell-csi-extensions/replication v1.0.0
	github.com/dell/gobrick v1.2.0
	github.com/dell/gocsi v1.4.1-0.20211014153731-e18975a3a38c
	github.com/dell/gofsutil v1.5.0
	github.com/dell/goiscsi v1.2.0
	github.com/dell/gopowermax v1.6.0
	github.com/fsnotify/fsnotify v1.4.7
	github.com/golang/protobuf v1.5.2
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/googleapis/gnostic v0.4.0 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.7.0
	github.com/sirupsen/logrus v1.6.0
	github.com/spf13/viper v1.7.1
	github.com/stretchr/testify v1.7.0
	golang.org/x/net v0.0.0-20210405180319-a5a99cb37ef4
	google.golang.org/grpc v1.38.0
	k8s.io/client-go v0.18.6
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800 // indirect
)
