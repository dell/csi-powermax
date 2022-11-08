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
	"fmt"
	"strconv"
	"testing"
)

var vmh *VMHost

func init() {
	vcenterHost := "dldv2226.drm.lab.emc.com"
	vcenterUsername := "administrator@vsphere.local"
	vcenterPassword := "Remember12#"
	vcenterInsecure, _ := strconv.ParseBool("true")

	var err error
	vmh, err = NewVMHost(vcenterInsecure, vcenterHost, vcenterUsername, vcenterPassword)
	if err != nil {
		panic(err)
	}
}

func TestGetLocalMac(*testing.T) {
	mac, err := getLocalMAC()
	if err != nil {
		panic(err)
	}

	fmt.Println(mac)
}

func TestFindVM(*testing.T) {
	vm, err := vmh.findVM(vmh.mac)
	if err != nil {
		panic(err)
	}

	fmt.Println(fmt.Sprintf("%+v", vm))
}

func TestRescanAllHba(t *testing.T) {
	host, err := vmh.VM.HostSystem(vmh.Ctx)
	if err != nil {
		panic(err)
	}
	if err := vmh.RescanAllHba(host); err != nil {
		panic(err)
	}
}

func TestAttachRDM(*testing.T) {
	if err := vmh.AttachRDM(vmh.VM, "60000970000196701380533030313142"); err != nil {
		panic(err)
	}
}

func TestDetachRDM(*testing.T) {
	if err := vmh.DetachRDM(vmh.VM, "60000970000196701380533030313142"); err != nil {
		panic(err)
	}
}
