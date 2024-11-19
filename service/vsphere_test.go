package service

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGetLocalMAC(t *testing.T) {
	var mac, err = getLocalMAC()
	if mac != "" {
		assert.NoError(t, err)
	}
}
