// Package features_test runs the godog acceptance suite over the real use cases and the
// in-memory doubles (no Docker).
package features_test

import (
	"testing"

	"github.com/cucumber/godog"
)

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: InitializeScenario,
		Options:             &godog.Options{Format: "pretty", Paths: []string{"."}, Strict: true, TestingT: t},
	}
	if suite.Run() != 0 {
		t.Fatal("non-zero status returned, failed to run feature tests")
	}
}
