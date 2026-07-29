package pool

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestConfigureRuntimeStateImportsLegacyHealthOnce(t *testing.T) {
	resetHealthPersistenceForTest()
	t.Cleanup(resetHealthPersistenceForTest)
	dir := t.TempDir()
	t.Cleanup(func() { _ = CloseRuntimeState() })
	legacyPath := filepath.Join(dir, "health-state.yaml")
	databasePath := filepath.Join(dir, "runtime-state.db")
	legacy := persistedHealthFile{Version: healthStateVersion, Nodes: map[string]persistedMemberHealth{
		"legacy-node": {Failures: 2, UpdatedAt: time.Now().UTC()},
	}}
	data, err := yaml.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureRuntimeState(databasePath, legacyPath); err != nil {
		t.Fatalf("configure runtime state: %v", err)
	}
	if restored, ok := restoredMemberHealth("legacy-node"); !ok || restored.Failures != 2 {
		t.Fatalf("legacy state not imported: %+v, %t", restored, ok)
	}
	if err := CloseRuntimeState(); err != nil {
		t.Fatalf("close runtime state: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("invalid: ["), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureRuntimeState(databasePath, legacyPath); err != nil {
		t.Fatalf("reopen populated runtime state should ignore legacy file: %v", err)
	}
	if restored, ok := restoredMemberHealth("legacy-node"); !ok || restored.Failures != 2 {
		t.Fatalf("SQLite state was not restored: %+v, %t", restored, ok)
	}
}
