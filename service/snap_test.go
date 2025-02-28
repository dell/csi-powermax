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

package service

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func Test_snapCleanupQueue_Swap(t *testing.T) {
	type args struct {
		i int
		j int
	}
	tests := []struct {
		name string
		q    snapCleanupQueue
		args args
		want snapCleanupQueue
	}{
		{
			name: "successful swap",
			q: snapCleanupQueue{
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap1",
					volumeID:    "vol1",
					requestID:   "req1",
					retries:     1,
				},
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap2",
					volumeID:    "vol1",
					requestID:   "req2",
					retries:     1,
				},
			},
			args: args{
				i: 0,
				j: 1,
			},
			want: snapCleanupQueue{
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap2",
					volumeID:    "vol1",
					requestID:   "req2",
					retries:     1,
				},
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap1",
					volumeID:    "vol1",
					requestID:   "req1",
					retries:     1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.q.Swap(tt.args.i, tt.args.j)
			if !reflect.DeepEqual(tt.q, tt.want) {
				t.Errorf("Swap() failed. want: %v, have: %v", tt.want, tt.q)
			}
		})
	}
}

func Test_snapCleanupQueue_Pop(t *testing.T) {
	tests := []struct {
		name  string
		q     *snapCleanupQueue
		wantQ *snapCleanupQueue
		want  interface{}
	}{
		{
			name: "pop single element and return empty queue",
			q: &snapCleanupQueue{
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap1",
					volumeID:    "vol1",
					requestID:   "req1",
					retries:     1,
				},
			},
			wantQ: &snapCleanupQueue{},
			want: snapCleanupRequest{
				symmetrixID: "00000000001",
				snapshotID:  "snap1",
				volumeID:    "vol1",
				requestID:   "req1",
				retries:     1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.q.Pop(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("snapCleanupQueue.Pop() = %v, want %v", got, tt.want)
			}
			if !reflect.DeepEqual(tt.wantQ, tt.q) {
				t.Errorf("failed to pop from the queue. want: %v, have: %v", tt.wantQ, tt.q)
			}
		})
	}
}

func Test_snapCleanupWorker_getQueueLen(t *testing.T) {
	type fields struct {
		PollingInterval time.Duration
		Queue           snapCleanupQueue
		MaxRetries      int
	}
	tests := []struct {
		name   string
		fields fields
		want   int
	}{
		{
			name: "get queue length",
			fields: fields{
				PollingInterval: time.Second * 5,
				Queue: snapCleanupQueue{
					{
						symmetrixID: "00000000001",
						snapshotID:  "snap1",
						volumeID:    "vol1",
						requestID:   "req1",
						retries:     1,
					},
				},
				MaxRetries: 1,
			},
			want: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scw := &snapCleanupWorker{
				PollingInterval: tt.fields.PollingInterval,
				Mutex:           sync.Mutex{},
				Queue:           tt.fields.Queue,
				MaxRetries:      tt.fields.MaxRetries,
			}
			if got := scw.getQueueLen(); got != tt.want {
				t.Errorf("snapCleanupWorker.getQueueLen() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_snapCleanupWorker_queueForRetry(t *testing.T) {
	type fields struct {
		PollingInterval time.Duration
		Queue           snapCleanupQueue
		MaxRetries      int
	}
	type args struct {
		req *snapCleanupRequest
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		wantQ  snapCleanupQueue
	}{
		{
			name: "queue a cleanup request",
			fields: fields{
				PollingInterval: time.Second * 5,
				Queue:           snapCleanupQueue{},
				MaxRetries:      1,
			},
			args: args{
				req: &snapCleanupRequest{
					symmetrixID: "00000000001",
					snapshotID:  "snap1",
					volumeID:    "vol1",
					requestID:   "req1",
					retries:     1,
				},
			},
			wantQ: snapCleanupQueue{
				{
					symmetrixID: "00000000001",
					snapshotID:  "snap1",
					volumeID:    "vol1",
					requestID:   "req1",
					retries:     1,
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scw := &snapCleanupWorker{
				PollingInterval: tt.fields.PollingInterval,
				Mutex:           sync.Mutex{},
				Queue:           tt.fields.Queue,
				MaxRetries:      tt.fields.MaxRetries,
			}
			scw.queueForRetry(tt.args.req)

			// request should appear on the queue
			if !reflect.DeepEqual(tt.wantQ, scw.Queue) {
				t.Errorf("queues are not equal. failed to queue request. want: %v, have: %v", tt.wantQ, scw.Queue)
			}
		})
	}
}

func Test_snapCleanupWorker_removeItem(t *testing.T) {
	type fields struct {
		PollingInterval time.Duration
		Queue           snapCleanupQueue
		MaxRetries      int
	}
	tests := []struct {
		name   string
		fields fields
		want   *snapCleanupRequest
		wantQ  snapCleanupQueue
	}{
		{
			name: "remove an item from a non-empty queue",
			fields: fields{
				PollingInterval: time.Second * 5,
				Queue: snapCleanupQueue{
					{
						symmetrixID: "00000000001",
						snapshotID:  "snap1",
						volumeID:    "vol1",
						requestID:   "req1",
						retries:     1,
					},
				},
				MaxRetries: 1,
			},
			want: &snapCleanupRequest{
				symmetrixID: "00000000001",
				snapshotID:  "snap1",
				volumeID:    "vol1",
				requestID:   "req1",
				retries:     1,
			},
			wantQ: snapCleanupQueue{},
		},
		{
			name: "remove an item from an empty queue",
			fields: fields{
				PollingInterval: time.Second * 5,
				Queue:           snapCleanupQueue{},
				MaxRetries:      1,
			},
			want:  nil,
			wantQ: snapCleanupQueue{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scw := &snapCleanupWorker{
				PollingInterval: tt.fields.PollingInterval,
				Mutex:           sync.Mutex{},
				Queue:           tt.fields.Queue,
				MaxRetries:      tt.fields.MaxRetries,
			}
			if got := scw.removeItem(); !reflect.DeepEqual(got, tt.want) {
				t.Errorf("snapCleanupWorker.removeItem() = %v, want %v", got, tt.want)
			}
			// confirm the queue no longer contains the popped item
			if !reflect.DeepEqual(tt.wantQ, scw.Queue) {
				t.Errorf("failed to remove item from queue. want: %v, have: %v", tt.wantQ, scw.Queue)
			}
		})
	}
}

func Test_service_isSourceTaggedToDelete(t *testing.T) {
	type args struct {
		volName string
	}
	tests := []struct {
		name   string
		args   args
		wantOk bool
	}{
		{
			name: "source is not tagged for deletion",
			args: args{
				volName: "csiprefix-vol1",
			},
			wantOk: false,
		},
		{
			name: "source is tagged for deletion",
			args: args{
				volName: "csiprefix-vol1-DS",
			},
			wantOk: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}
			if gotOk := s.isSourceTaggedToDelete(tt.args.volName); gotOk != tt.wantOk {
				t.Errorf("service.isSourceTaggedToDelete() = %v, want %v", gotOk, tt.wantOk)
			}
		})
	}
}

func Test_service_startSnapCleanupWorker(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "start snap cleanup without initializing admin client",
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &service{}
			if err := s.startSnapCleanupWorker(); (err != nil) != tt.wantErr {
				t.Errorf("service.startSnapCleanupWorker() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
