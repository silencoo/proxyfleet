package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultConfigYAML = `# ProxyFleet configuration.
# This first-run file starts the local WebUI without proxy nodes.
# Add a subscription in System settings, refresh it, and the proxy runtime
# will start automatically after usable nodes have been loaded.
mode: pool

endpoints:
  - name: default
    enabled: true
    address: 127.0.0.1
    port: 2323

pool:
  mode: sequential
  runtime_state_file: runtime-state.db

management:
  enabled: true
  listen: 127.0.0.1:9091
  password: %s
  probe_target: www.apple.com:80
  probe_mode: adaptive
  probe_interval: 5m
  probe_timeout: 10s
  probe_batch_size: 100
  probe_healthy_interval: 30m
  probe_failure_retry_interval: 1m
  probe_failure_max_interval: 1h
  probe_passive_grace: 10m
  probe_max_per_hour: 600
  probe_max_per_day: 5000
  history_enabled: true
  history_file: monitor-history.json
  history_retention: 24h
  history_interval: 1m
  audit_file: audit.log
  audit_max_entries: 1000

subscription_refresh:
  enabled: true
  max_removed_ratio: 0.5
  min_available_ratio: 0
  quarantine_new_nodes: true

log:
  output: stdout
  rotate_interval: 0s

subscriptions: []
profiles: []
nodes: []
`

// BootstrapResult reports whether this process created the first-run
// configuration. ManagementPassword is populated only for the winning creator
// so callers can print it once without re-reading or exposing existing secrets.
type BootstrapResult struct {
	Created            bool
	ManagementPassword string
}

// EnsureDefaultFile creates a safe first-run configuration when path does not
// exist. The existence check and write share the config sidecar lock so a
// concurrent user or process can never have its newly-created file replaced.
func EnsureDefaultFile(path string) (bool, error) {
	result, err := EnsureDefaultFileWithResult(path)
	return result.Created, err
}

// EnsureDefaultFileWithResult is the credential-aware first-run helper used by
// the executable bootstrap.
func EnsureDefaultFileWithResult(path string) (BootstrapResult, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return BootstrapResult{}, errors.New("config file path is empty")
	}
	path = filepath.Clean(path)

	var result BootstrapResult
	err := withFileLock(path, func() error {
		if _, err := os.Stat(path); err == nil {
			return nil
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect config file: %w", err)
		}
		password, err := generateBootstrapPassword()
		if err != nil {
			return fmt.Errorf("generate management password: %w", err)
		}
		data := fmt.Sprintf(defaultConfigYAML, password)
		if _, err := writeFileLockedSnapshot(path, []byte(data), 0o600); err != nil {
			return fmt.Errorf("create default config: %w", err)
		}
		result.Created = true
		result.ManagementPassword = password
		return nil
	})
	if err != nil {
		return BootstrapResult{}, err
	}
	return result, nil
}

func generateBootstrapPassword() (string, error) {
	const randomBytes = 24
	buffer := make([]byte, randomBytes)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}
