package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadPreservesDefaultsAndRejectsMistypedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"listen":"127.0.0.1:18091","camera":{"max_connections":2}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Listen != "127.0.0.1:18091" || cfg.Camera.MaxConnections != 2 || cfg.Camera.SessionTTLSeconds != 30 {
		t.Fatalf("partial configuration lost defaults: %+v", cfg)
	}
	for _, input := range []string{
		`{"listen_port":18091}`, `{"camera":{"max_connection":2}}`,
		`{"listen":123}`, `{} {}`, `null`, `[]`, ``,
	} {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Errorf("invalid configuration was accepted: %s", input)
		}
	}
}

func TestValidationRejectsUnsafeListenerAndBrokenLeaseSettings(t *testing.T) {
	base := Default()
	base.LogDirectory = t.TempDir()
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"0.0.0.0:18090", "192.168.1.10:18090", ":18090", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1"} {
		cfg := base
		cfg.Listen = address
		if err := cfg.Validate(); err == nil {
			t.Errorf("invalid listener accepted: %s", address)
		}
	}
	mutations := []func(*Config){
		func(c *Config) { c.Camera.MaxConnections = 0 },
		func(c *Config) { c.Camera.MaxSessions = -1 },
		func(c *Config) { c.Camera.MaxConcurrentSnapshots = 0 },
		func(c *Config) { c.Camera.HeartbeatIntervalSeconds = 0 },
		func(c *Config) { c.Camera.SessionTTLSeconds = c.Camera.HeartbeatIntervalSeconds },
		func(c *Config) { c.Camera.IdleCloseSeconds = -1 },
		func(c *Config) { c.LogDirectory = "relative-logs" },
	}
	for i, mutate := range mutations {
		cfg := base
		mutate(&cfg)
		if err := cfg.Validate(); err == nil {
			t.Errorf("invalid setting accepted: case %d", i)
		}
	}
	base.Listen = "[::1]:18090"
	if err := base.Validate(); err != nil {
		t.Fatalf("IPv6 loopback should be accepted: %v", err)
	}
}
