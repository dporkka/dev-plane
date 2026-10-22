package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAndShowDeviceIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keys", "device.pem")
	var first, second bytes.Buffer
	if err := run([]string{"init", "--key", path}, &first); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"show", "--key", path}, &second); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() || !strings.Contains(first.String(), "device_id=dp_") || !strings.Contains(first.String(), "public_key=") {
		t.Fatalf("unexpected identity output: %s", first.String())
	}
	if err := run([]string{"init", "--key", path}, &bytes.Buffer{}); err == nil {
		t.Fatal("identity overwrite accepted")
	}
}
func TestCLIRequiresExplicitPathAndCommand(t *testing.T) {
	for _, args := range [][]string{{}, {"init"}, {"show", "--key", ""}, {"run", "--key", "file"}} {
		if err := run(args, &bytes.Buffer{}); err == nil {
			t.Fatalf("unexpected success for %v", args)
		}
	}
}
