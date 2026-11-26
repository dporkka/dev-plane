package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadPolicyRejectsUnsafeOrMalformedFiles(t *testing.T) {
	const valid = `{"work_item_id":"work","gates":{"build":{}}}`
	root := t.TempDir()
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
		valid      bool
	}{
		{"valid shape", valid, 0600, true},
		{"world writable", valid, 0666, false},
		{"empty gates", `{"work_item_id":"work","gates":{}}`, 0600, false},
		{"unknown field", `{"work_item_id":"work","gates":{"build":{}},"extra":true}`, 0600, false},
		{"trailing object", valid + "{}", 0600, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(root, tc.name)
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tc.mode); err != nil {
				t.Fatal(err)
			}
			_, err := readPolicy(path)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected policy result: %v", err)
			}
		})
	}
	target := filepath.Join(root, "valid shape")
	link := filepath.Join(root, "symlink")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := readPolicy(link); err == nil {
		t.Fatal("accepted symlink policy")
	}
}
