// +build linux,plugin
//go:generate go generate ./core

package main

import "C"

import (
	"github.com/dell/csi-powermax/provider"
	"github.com/dell/csi-powermax/service"
)

////////////////////////////////////////////////////////////////////////////////
//                              Go Plug-in                                    //
////////////////////////////////////////////////////////////////////////////////

// ServiceProviders is an exported symbol that provides a host program
// with a map of the service provider names and constructors.
var ServiceProviders = map[string]func() interface{}{
	service.Name: func() interface{} { return provider.New() },
}
