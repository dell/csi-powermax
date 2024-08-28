/*
Copyright © 2021 Dell Inc. or its subsidiaries. All Rights Reserved.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package integration_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/credentials/insecure"

	csi "github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/cucumber/godog"
	"github.com/dell/csi-powermax/v2/provider"
	"github.com/dell/csi-powermax/v2/service"
	"github.com/dell/gocsi/utils"
	"google.golang.org/grpc"
)

const (
	datafile       = "/tmp/datafile"
	datadir        = "/tmp/datadir"
	defaultTimeout = 5 * time.Minute
)

var grpcClient *grpc.ClientConn

func readTimeoutFromEnv() time.Duration {
	contextTimeout := defaultTimeout
	if timeoutStr := os.Getenv("X_CSI_UNISPHERE_TIMEOUT"); timeoutStr != "" {
		if timeout, err := time.ParseDuration(timeoutStr); err == nil {
			contextTimeout = timeout
		}
	}
	return contextTimeout
}

func TestMain(m *testing.M) {
	var stop func()
	// Block must be enabled for many tests to work
	os.Setenv(service.EnvEnableBlock, "true")
	ctx, cancel := context.WithTimeout(context.Background(), readTimeoutFromEnv())
	defer cancel()
	fmt.Println("*** calling startServer ***")
	var err error
	grpcClient, stop, err = startServer(ctx)
	if err != nil {
		log.Errorf("Failed to create a grpc client: %s", err.Error())
		os.Exit(1)
	}
	fmt.Println("*** back from startServer ***")

	// Make the directory and file needed for NodePublish, these are:
	//  /tmp/datadir    -- for file system mounts
	//  /tmp/datafile   -- for block bind mounts
	fmt.Printf("Checking %s\n", datadir)
	var fileMode os.FileMode
	fileMode = 0o777
	err = os.Mkdir(datadir, fileMode)
	if err != nil && !os.IsExist(err) {
		fmt.Printf("%s: %s\n", datadir, err)
	}
	fmt.Printf("Checking %s\n", datafile)
	file, err := os.Create(datafile)
	if err != nil && !os.IsExist(err) {
		fmt.Printf("%s %s\n", datafile, err)
	}
	if file != nil {
		file.Close()
	}

	time.Sleep(10 * time.Second)
	var exitVal int
	if st := m.Run(); st > exitVal {
		exitVal = st
	}
	write, err := os.Create("powermax_integration_testresults.xml")
	runOptions := godog.Options{
		Output: write,
		Format: "junit",
		Paths:  []string{"features"},
		Tags:   "@v1.0.0,@v1.1.0,@v1.2.0,@v1.4.0,@v1.6.0,@v2.2.0,@v2.4.0,@v2.7.0,@2.12.0",
	}
	godogExit := godog.TestSuite{
		Name:                "CSI Powermax Int Tests",
		ScenarioInitializer: FeatureContext,
		Options:             &runOptions,
	}.Run()

	if godogExit > exitVal {
		exitVal = godogExit
	}
	stop()
	os.Exit(exitVal)
}

func TestIdentityGetPluginInfo(t *testing.T) {
	ctx := context.Background()
	fmt.Printf("testing GetPluginInfo\n")
	client := csi.NewIdentityClient(grpcClient)
	info, err := client.GetPluginInfo(ctx, &csi.GetPluginInfoRequest{})
	if err != nil {
		fmt.Printf("GetPluginInfo %s:\n", err.Error())
		t.Error("GetPluginInfo failed")
	} else {
		fmt.Printf("testing GetPluginInfo passed: %s\n", info.GetName())
	}
}

func TestNodeGetInfo(t *testing.T) {
	ctx := context.Background()
	fmt.Printf("testing NodeGetInfo\n")
	client := csi.NewNodeClient(grpcClient)
	info, err := client.NodeGetInfo(ctx, &csi.NodeGetInfoRequest{})
	if err != nil {
		fmt.Printf("NodeGetInfo %s:\n", err.Error())
		t.Error("NodeGetInfo failed")
	} else {
		if info.AccessibleTopology == nil {
			t.Error("NodeGetInfo failed, no topology keys found")
		}
		fmt.Printf("testing NodeGetInfo passed:\n %+v\n", info)
	}
}

func startServer(ctx context.Context) (*grpc.ClientConn, func(), error) {
	// Create a new SP instance and serve it with a piped connection.
	sp := provider.New()
	lis, err := utils.GetCSIEndpointListener()
	if err != nil {
		fmt.Printf("couldn't open listener: %s\n", err.Error())
		return nil, nil, err
	}
	go func() {
		fmt.Printf("starting server\n")
		if err := sp.Serve(ctx, lis); err != nil {
			fmt.Printf("http: Server closed")
		}
	}()
	network, addr, err := utils.GetCSIEndpoint()
	if err != nil {
		return nil, nil, err
	}
	fmt.Printf("network %v addr %v\n", network, addr)

	clientOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff:           backoff.DefaultConfig,
			MinConnectTimeout: 10 * time.Second,
		}),
		grpc.WithBlock(),
	}

	var conn *grpc.ClientConn
	ready := make(chan bool)
	address := "unix:" + addr
	go func() {
		// Create a client for the piped connection.
		fmt.Printf("calling gprc.DialContext, ctx %v, addr %s, clientOpts %v\n", ctx, addr, clientOpts)
		conn, err = grpc.Dial(address, clientOpts...)
		close(ready)
	}()

	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			log.Infof("Still connecting to %s", address)
		case <-ready:
			if err != nil {
				return nil, nil, err
			}
			return conn, func() {
				conn.Close()
				sp.GracefulStop(ctx)
			}, err
		}
	}
}
