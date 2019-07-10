package k8integration

import (
	"fmt"
	"os"
	"testing"

	"github.com/DATA-DOG/godog"
)

func TestMain(m *testing.M) {
	podstatus := ControllerIsRunning()
	if podstatus != nil {
		fmt.Println("controller plugin service is not running ")
		os.Exit(0)
	}
	exitVal := godog.RunWithOptions("godog", func(s *godog.Suite) {
		FeatureContext(s)
	}, godog.Options{
		Format: "pretty",
		Paths:  []string{"features"},
		Tags:   "current",
	})
	if st := m.Run(); st > exitVal {
		exitVal = st
	}
	os.Exit(exitVal)
}
