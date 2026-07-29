package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareConfigFileCreatesAndPreservesDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.yaml")

	resolved, bootstrap, err := prepareConfigFile(path)
	if err != nil {
		t.Fatalf("prepare config: %v", err)
	}
	if !filepath.IsAbs(resolved) {
		t.Fatalf("resolved path is not absolute: %q", resolved)
	}
	if !bootstrap.Created {
		t.Fatal("first prepare did not report creation")
	}
	if len(bootstrap.ManagementPassword) < 32 {
		t.Fatalf("generated management password is too short: %d", len(bootstrap.ManagementPassword))
	}
	first, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("read generated config: %v", err)
	}

	resolvedAgain, secondBootstrap, err := prepareConfigFile(path)
	if err != nil {
		t.Fatalf("prepare existing config: %v", err)
	}
	if resolvedAgain != resolved {
		t.Fatalf("resolved path changed: %q != %q", resolvedAgain, resolved)
	}
	if secondBootstrap.Created {
		t.Fatal("existing config was reported as newly created")
	}
	if secondBootstrap.ManagementPassword != "" {
		t.Fatal("existing config password was unexpectedly exposed")
	}
	second, err := os.ReadFile(resolved)
	if err != nil {
		t.Fatalf("read preserved config: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("existing config was replaced")
	}
}
