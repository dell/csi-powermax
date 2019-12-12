package service

import (
	"fmt"
	"net/http"
	_ "net/http/pprof"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/godog"
)

var (
	testStatus    int
	testStartTime time.Time
)

func TestMain(m *testing.M) {
	testStatus = 0
	testStartTime = time.Now()

	go http.ListenAndServe("localhost:6060", nil)

	if st := m.Run(); st > testStatus {
		testStatus = st
	}

	fmt.Printf("status %d\n", testStatus)

	os.Exit(testStatus)
}

func TestGoDog(t *testing.T) {
	fmt.Printf("starting godog...\n")
	testStatus += godog.RunWithOptions("godog", func(s *godog.Suite) {
		FeatureContext(s)
	}, godog.Options{
		Format: "pretty",
		Paths:  []string{"features"},
		Tags:   "v1.0.0, v1.1.0",
		//Tags:   "wip",
	})
	fmt.Printf("godog finished\n")
	if testStatus != 0 {
		t.Error("Error encountered in godog testing")
	}
}
