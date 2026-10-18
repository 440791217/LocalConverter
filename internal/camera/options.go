// Package camera manages capture sessions, shared sources, and media tools.
package camera

import "fmt"

// Options controls session leases, source limits, and snapshot concurrency.
type Options struct {
	MaxConnections           int `json:"max_connections"`
	MaxSessions              int `json:"max_sessions"`
	MaxConcurrentSnapshots   int `json:"max_concurrent_snapshots"`
	HeartbeatIntervalSeconds int `json:"heartbeat_interval_seconds"`
	SessionTTLSeconds        int `json:"session_ttl_seconds"`
	IdleCloseSeconds         int `json:"idle_close_seconds"`
}

func DefaultOptions() Options {
	return Options{
		MaxConnections: 4, MaxSessions: 16, MaxConcurrentSnapshots: 2,
		HeartbeatIntervalSeconds: 10, SessionTTLSeconds: 30, IdleCloseSeconds: 5,
	}
}

func (o Options) Validate() error {
	if o.MaxConnections < 1 || o.MaxSessions < 1 || o.MaxConcurrentSnapshots < 1 {
		return fmt.Errorf("camera concurrency limits must be positive")
	}
	if o.HeartbeatIntervalSeconds < 1 || o.SessionTTLSeconds <= o.HeartbeatIntervalSeconds {
		return fmt.Errorf("session TTL must exceed the positive heartbeat interval")
	}
	if o.IdleCloseSeconds < 0 {
		return fmt.Errorf("idle close delay cannot be negative")
	}
	return nil
}
