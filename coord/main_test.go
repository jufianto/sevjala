package coord

import (
	"os"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/jufianto/sevjala/dump/dumptest"
)

func TestMain(m *testing.M) {
	dumptest.Main()
	keyring.MockInit()
	os.Exit(m.Run())
}
