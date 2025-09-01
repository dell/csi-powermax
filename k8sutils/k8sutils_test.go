/*
 *
 * Copyright © 2021-2024 Dell Inc. or its subsidiaries. All Rights Reserved.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

/*
Copyright © 2025 Dell Inc. or its subsidiaries. All Rights Reserved.

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

package k8sutils

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
)

const kubeconfigFilepath = "./fake-kubeconfig"

func Test_CreateKubeClientSet(t *testing.T) {
	tests := []struct {
		name       string
		kubeConfig string
		before     func(string) error
		after      func()
		wantErr    bool
	}{
		{
			name:       "valid config and namespace",
			kubeConfig: kubeconfigFilepath,
			before: func(filepath string) error {
				return createTempKubeconfig(filepath)
			},
			after: func() {
				_ = os.Remove(kubeconfigFilepath)
			},
			wantErr: false,
		},
		{
			name:       "when kubeconfig does not exist",
			kubeConfig: kubeconfigFilepath,
			before: func(_ string) error {
				// intentionally do not create the temp kubeconfig
				return nil
			},
			after:   func() {},
			wantErr: true,
		},
		{
			name:       "not in a cluster",
			kubeConfig: "",
			before:     func(_ string) error { return nil },
			after:      func() {},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// create the fake kubeconfig needed for testing
			err := tt.before(tt.kubeConfig)
			defer tt.after()
			if err != nil {
				t.Errorf("failed to create fake kubeconfig. error: %s", err.Error())
				return
			}

			clientSet, err := CreateKubeClientSet(tt.kubeConfig)
			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, clientSet)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, clientSet)
			}
		})
	}
}

func TestK8sUtils_GetNodeIPs(t *testing.T) {
	type fields struct {
		KubernetesClient *KubernetesClient
		node             *corev1.Node
	}
	type args struct {
		nodeID string
	}
	type test struct {
		name   string
		fields fields
		args   args
		before func(t test) error
		after  func(t test)
		want   string
	}
	tests := []test{
		{
			name: "Successfully gets node IP",
			fields: fields{
				KubernetesClient: &KubernetesClient{
					ClientSet: fake.NewClientset(),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node1",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "127.0.0.1",
							},
						},
					},
				},
			},
			args: args{
				nodeID: "node1",
			},
			before: func(t test) error {
				// create a node with the fake client
				_, err := t.fields.KubernetesClient.ClientSet.CoreV1().Nodes().Create(context.Background(), t.fields.node, metav1.CreateOptions{})
				return err
			},
			after: func(t test) {
				t.fields.KubernetesClient = nil
			},
			want: "127.0.0.1",
		},
		{
			name: "kube client failes to list nodes",
			fields: fields{
				KubernetesClient: &KubernetesClient{
					ClientSet: &fake.Clientset{},
				},
			},
			args: args{
				nodeID: "node1",
			},
			before: func(_ test) error { return nil },
			after: func(t test) {
				t.fields.KubernetesClient = nil
			},
			want: "",
		},
		{
			name: "Node ID isn't found",
			fields: fields{
				KubernetesClient: &KubernetesClient{
					ClientSet: fake.NewClientset(),
				},
				node: &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name: "node1",
					},
					Status: corev1.NodeStatus{
						Addresses: []corev1.NodeAddress{
							{
								Type:    corev1.NodeInternalIP,
								Address: "127.0.0.1",
							},
						},
					},
				},
			},
			args: args{
				nodeID: "bad-node-id",
			},
			before: func(t test) error {
				_, err := t.fields.KubernetesClient.ClientSet.CoreV1().Nodes().Create(context.Background(), t.fields.node, metav1.CreateOptions{})
				return err
			},
			after: func(t test) {
				t.fields.KubernetesClient = nil
			},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// set the fake kube client
			c := &K8sUtils{
				KubernetesClient: tt.fields.KubernetesClient,
			}

			// initialize resources for the fake kube client
			err := tt.before(tt)
			defer tt.after(tt)
			if err != nil {
				t.Errorf("failed to initialize resources for the fake kube client: %s", err.Error())
			}

			if got := c.GetNodeIPs(tt.args.nodeID); got != tt.want {
				t.Errorf("K8sUtils.GetNodeIPs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestK8sUtils_GetNodeLabels(t *testing.T) {
	type fields struct {
		KubernetesClient *KubernetesClient
	}
	type args struct {
		nodeFullName string
	}
	type test struct {
		name    string
		fields  fields
		args    args
		before  func(t test) error
		after   func(t test)
		want    map[string]string
		wantErr bool
	}
	labels := map[string]string{
		"csi-powermax.dellemc.com/000120001607.iscsi": "csi-powermax.dellemc.com",
	}
	tests := []test{
		{
			name: "successfully gets node labels",
			fields: fields{
				KubernetesClient: &KubernetesClient{ClientSet: fake.NewClientset()},
			},
			args: args{nodeFullName: "node1"},
			before: func(tt test) error {
				_, err := tt.fields.KubernetesClient.ClientSet.CoreV1().Nodes().Create(context.Background(), &corev1.Node{
					ObjectMeta: metav1.ObjectMeta{
						Name:   "node1",
						Labels: labels,
					},
				}, metav1.CreateOptions{})
				return err
			},
			after: func(tt test) {
				tt.fields.KubernetesClient = nil
			},
			want:    labels,
			wantErr: false,
		},
		{
			name: "when the node is not found",
			fields: fields{
				KubernetesClient: &KubernetesClient{ClientSet: fake.NewClientset()},
			},
			args:   args{nodeFullName: "node1"},
			before: func(_ test) error { return nil },
			after: func(tt test) {
				tt.fields.KubernetesClient = nil
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// create the fake kube client
			c := &K8sUtils{
				KubernetesClient: tt.fields.KubernetesClient,
			}

			// initialize kube resources for the fake client
			err := tt.before(tt)
			defer tt.after(tt)
			if err != nil {
				t.Errorf("failed to initialize resources for the fake kube client: %s", err.Error())
			}

			got, err := c.GetNodeLabels(tt.args.nodeFullName)
			if (err != nil) != tt.wantErr {
				t.Errorf("K8sUtils.GetNodeLabels() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("K8sUtils.GetNodeLabels() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_Init(t *testing.T) {
	type args struct {
		kubeConfig string
	}
	type test struct {
		name    string
		args    args
		before  func(tt test) error
		after   func()
		want    *K8sUtils
		wantErr bool
	}
	tests := []test{
		{
			name: "successfully initializes the kube client",
			args: args{kubeConfig: kubeconfigFilepath},
			before: func(tt test) error {
				return createTempKubeconfig(tt.args.kubeConfig)
			},
			after: func() {
				_ = os.Remove(kubeconfigFilepath)
				k8sUtils = nil
			},
			wantErr: false,
		},
		{
			name:    "fails when given an empty kubeconfig file path",
			args:    args{kubeConfig: ""},
			before:  func(_ test) error { return nil },
			after:   func() {},
			wantErr: true,
		},
		{
			name: "returns existing k8s clientset",
			args: args{kubeConfig: kubeconfigFilepath},
			before: func(tt test) error {
				err := createTempKubeconfig(tt.args.kubeConfig)
				if err != nil {
					return err
				}
				// initialize the k8s clientset before we start the official test
				// so the test will return the existing clientset
				tt.want, err = Init(tt.args.kubeConfig)
				return err
			},
			after: func() {
				_ = os.Remove(kubeconfigFilepath)
				k8sUtils = nil
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// create the fake kubeconfig
			err := tt.before(tt)
			defer tt.after()
			if err != nil {
				t.Errorf("failed to create the fake kubeconfig: %s", err.Error())
				return
			}

			got, err := Init(tt.args.kubeConfig)
			if (err != nil) != tt.wantErr {
				t.Errorf("Init() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				assert.NotNil(t, got)
				// if a wanted return value is specified, check it against what we got
				if tt.want != nil {
					assert.Equal(t, tt.want, got)
				}
			}
		})
	}
}

func Test_LeaderElection(t *testing.T) {
	type args struct {
		clientSet kubernetes.Interface
		lockName  string
		namespace string
		runFunc   func(ctx context.Context)
	}

	type test struct {
		name    string
		args    args
		wantErr bool
	}

	testCh := make(chan bool) // channel on which the runFunc should respond
	tests := []test{
		{
			// When the leader is elected, it should call the runFunc, at which point
			// the func should return a 'true' value to the testCh channel.
			name: "successfully starts leader election",
			args: args{
				clientSet: fake.NewClientset(),
				lockName:  "driver-csi-powermax-dellemc-com",
				namespace: "powermax",
				runFunc: func(_ context.Context) {
					t.Log("leader is elected and run func is running")
					testCh <- true
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// leaderElection.Run() func never exits during normal operation.
			// If the runFunc does not write to the testCh channel within 30 seconds,
			// consider it a failed run and cancel the context.
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			errCh := make(chan error)
			go func() {
				errCh <- LeaderElection(tt.args.clientSet, tt.args.lockName, tt.args.namespace, tt.args.runFunc)
			}()

			select {
			case err := <-errCh:
				// should only reach here if there is a config error when starting the
				// leaderElector via the leaderElector.Run() func. This is difficult to achieve in this context.
				if (err != nil) != tt.wantErr {
					t.Errorf("LeaderElection failed. err: %s", err.Error())
				}
			case pass := <-testCh:
				if pass == tt.wantErr {
					t.Errorf("failed to elect a leader and call the run func")
				}
			case <-ctx.Done():
				t.Error("timed out waiting for leader election to start")
			}
		})
	}
}

// creatTempKubeconfig creates a temporary, fake kubeconfig in the current directory
// using the given file path.
func createTempKubeconfig(filepath string) error {
	kubeconfig := `clusters:
- cluster:
    server: https://some.hostname.or.ip:6443
  name: fake-cluster
contexts:
- context:
    cluster: fake-cluster
    user: admin
  name: admin
current-context: admin
preferences: {}
users:
- name: admin`

	err := os.WriteFile(filepath, []byte(kubeconfig), 0o600)
	return err
}
