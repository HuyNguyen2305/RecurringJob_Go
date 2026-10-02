package e2e_test

import (
	"os"
	"testing"

	"recurringjob/test/helpers"
)

func TestMain(m *testing.M) {
	helpers.ApplyLocalTZ() // TEST_LOCAL_TZ=7 or -8 re-runs the suite in another zone
	os.Exit(m.Run())
}
