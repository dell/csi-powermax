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

package service

import (
	"context"
	"github.com/coreos/go-systemd/dbus"
	"github.com/dell/gobrick"
	log "github.com/sirupsen/logrus"
)

type customLogger struct{}

func (lg *customLogger) Info(ctx context.Context, format string, args ...interface{}) {
	log.WithFields(getLogFields(ctx)).Infof(format, args...)
}
func (lg *customLogger) Debug(ctx context.Context, format string, args ...interface{}) {
	log.WithFields(getLogFields(ctx)).Debugf(format, args...)
}
func (lg *customLogger) Error(ctx context.Context, format string, args ...interface{}) {
	log.WithFields(getLogFields(ctx)).Errorf(format, args...)
}

type iSCSIConnector interface {
	ConnectVolume(ctx context.Context, info gobrick.ISCSIVolumeInfo) (gobrick.Device, error)
	DisconnectVolumeByDeviceName(ctx context.Context, name string) error
	GetInitiatorName(ctx context.Context) ([]string, error)
}

type fcConnector interface {
	ConnectVolume(ctx context.Context, info gobrick.FCVolumeInfo) (gobrick.Device, error)
	DisconnectVolumeByDeviceName(ctx context.Context, name string) error
	GetInitiatorPorts(ctx context.Context) ([]string, error)
}

func (s *service) initISCSIConnector(chroot string) {
	if s.iscsiConnector == nil {
		setupGobrick(s)
		s.iscsiConnector = gobrick.NewISCSIConnector(
			gobrick.ISCSIConnectorParams{Chroot: chroot})
	}
}

func (s *service) initFCConnector(chroot string) {
	if s.fcConnector == nil {
		setupGobrick(s)
		s.fcConnector = gobrick.NewFCConnector(
			gobrick.FCConnectorParams{Chroot: chroot})
	}
}

func setupGobrick(srv *service) {
	gobrick.SetLogger(&customLogger{})
	//if srv.opts.EnableTracing {
	//	gobrick.SetTracer(&customTracer{})
	//}
}

// DBus is a message bus system which provides a way for applications
// to talk to each other. It is used by systemd and its auxiliary daemons
// and they expose a number of APIs on the D-Bus
type dBusConn interface {
	Close()
	ListUnits() ([]dbus.UnitStatus, error)
	StartUnit(name string, mode string, ch chan<- string) (int, error)
}

func (s *service) createDbusConnection() error {
	if s.dBusConn == nil {
		conn, err := dbus.New()
		if err != nil {
			log.Errorf("Failed to initialize connection to dbus. Error - %s", err.Error())
			return err
		}
		s.dBusConn = conn
	}
	return nil
}

func (s *service) closeDbusConnection() {
	if s.dBusConn != nil {
		s.dBusConn.Close()
		s.dBusConn = nil
	}
}
