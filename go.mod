module github.com/dell/csi-powermax

go 1.16

replace github.com/coreos/go-systemd => github.com/coreos/go-systemd/v22 v22.0.0

require (
	github.com/container-storage-interface/spec v1.3.0
	github.com/coreos/go-systemd v0.0.0-20190612170431-362f06ec6bc1
	github.com/cucumber/godog v0.10.0
	github.com/cucumber/messages-go/v10 v10.0.3
	github.com/dell/dell-csi-extensions/replication v0.2.0
	github.com/dell/gobrick v1.1.0
	github.com/dell/gocsi v1.3.0
	github.com/dell/gofsutil v1.5.0
	github.com/dell/goiscsi v1.2.0
	github.com/dell/gopowermax v1.5.0
	github.com/golang/protobuf v1.4.3
	github.com/google/gofuzz v1.2.0 // indirect
	github.com/googleapis/gnostic v0.4.0 // indirect
	github.com/hashicorp/go-uuid v1.0.1 // indirect
	github.com/kubernetes-csi/csi-lib-utils v0.7.0
	github.com/sirupsen/logrus v1.4.2
	github.com/stretchr/testify v1.6.1
	golang.org/x/crypto v0.0.0-20200622213623-75b288015ac9
	golang.org/x/net v0.0.0-20200822124328-c89045814202
	golang.org/x/sync v0.0.0-20200625203802-6e8e738ad208 // indirect
	golang.org/x/xerrors v0.0.0-20200804184101-5ec99f83aff1 // indirect
	google.golang.org/appengine v1.6.1 // indirect
	google.golang.org/grpc v1.29.0
	k8s.io/client-go v0.18.6
	k8s.io/utils v0.0.0-20200912215256-4140de9c8800 // indirect
)
