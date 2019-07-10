package pmax

import (
	"fmt"
	"github.com/DATA-DOG/godog"
	"github.com/dell/csi-powermax/pmax/mock"
	"net/http/httptest"
	"os"
	"testing"
)

var mockServer *httptest.Server

func TestMain(m *testing.M) {
	status := 0

	// Start the mock server.
	handler := mock.GetHandler()
	mockServer = httptest.NewServer(handler)
	fmt.Printf("mockServer listening on %s\n", mockServer.URL)

	status = godog.RunWithOptions("godog", func(s *godog.Suite) {
		UnitTestContext(s)
	}, godog.Options{
		Format: "pretty",
		Paths:  []string{"unittest"},
		Tags:   "",
	})

	if st := m.Run(); st > status {
		status = st
	}
	fmt.Printf("status %d\n", status)

	os.Exit(status)
}
