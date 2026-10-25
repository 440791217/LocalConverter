package camera

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Backend interface {
	Add(context.Context, string, string) error
	Remove(context.Context, string) error
	Ready(context.Context, string) (bool, error)
	WaitReady(context.Context, string) error
	Snapshot(context.Context, string) ([]byte, error)
}
type OpenRequest struct {
	DeviceID      string `json:"device_id"`
	ConfigVersion int64  `json:"config_version"`
	RTSPURL       string `json:"rtsp_url"`
}
type Session struct {
	ID            string    `json:"session_id"`
	DeviceID      string    `json:"device_id"`
	ConfigVersion int64     `json:"config_version"`
	State         string    `json:"state"`
	ExpiresAt     time.Time `json:"expires_at"`
	PreviewURL    string    `json:"preview_url"`
}
type connection struct {
	path, address string
	version       int64
	refs, pins    int
	idleAt        time.Time
}
type lease struct {
	view   Session
	source *connection
}
type Manager struct {
	mu          sync.Mutex
	options     Options
	backend     Backend
	sessions    map[string]*lease
	connections map[string]*connection
	versions    map[string]int64
	snapshots   chan struct{}
	closed      bool
}

func NewManager(o Options, b Backend) *Manager {
	return &Manager{options: o, backend: b, sessions: map[string]*lease{}, connections: map[string]*connection{}, versions: map[string]int64{}, snapshots: make(chan struct{}, o.MaxConcurrentSnapshots)}
}

func ValidateRequest(r OpenRequest) error {
	if r.DeviceID == "" || strings.TrimSpace(r.DeviceID) != r.DeviceID || len(r.DeviceID) > 128 || r.ConfigVersion < 0 {
		return failure(400, "请填写设备ID和有效配置版本")
	}
	u, err := url.Parse(r.RTSPURL)
	if err != nil || u == nil || u.Scheme != "rtsp" || u.Hostname() == "" || u.Fragment != "" || len(r.RTSPURL) > 2048 || strings.ContainsAny(r.RTSPURL, "\r\n\t ") {
		return failure(400, "请填写完整RTSP地址，特殊字符需要URL编码")
	}
	if u.Port() != "" {
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return failure(400, "RTSP端口无效")
		}
	}
	return nil
}

func (m *Manager) Open(ctx context.Context, r OpenRequest) (Session, error) {
	if err := ValidateRequest(r); err != nil {
		return Session{}, err
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Session{}, failure(503, "采集服务正在关闭")
	}
	if len(m.sessions) >= m.options.MaxSessions {
		m.mu.Unlock()
		return Session{}, failure(429, "使用会话已达上限")
	}
	if version, ok := m.versions[r.DeviceID]; ok && r.ConfigVersion < version {
		m.mu.Unlock()
		return Session{}, failure(409, "设备配置已更新，请重新获取配置")
	}
	c := m.connections[r.DeviceID]
	if c != nil && (c.address != r.RTSPURL || c.version != r.ConfigVersion) {
		if c.refs > 0 || c.pins > 0 {
			m.mu.Unlock()
			return Session{}, failure(409, "设备连接配置已变化，请先关闭该设备的旧会话再重新连接")
		}
		cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
		err := m.backend.Remove(cleanup, c.path)
		cancel()
		if err != nil {
			m.mu.Unlock()
			return Session{}, err
		}
		delete(m.connections, r.DeviceID)
		c = nil
	}
	if c == nil {
		// A released standby source must not occupy a slot for the whole idle
		// grace period when the next task camera needs that slot. Never evict
		// a source that another page or an in-flight snapshot still uses.
		if len(m.connections) >= m.options.MaxConnections {
			for device, idle := range m.connections {
				if idle.refs != 0 || idle.pins != 0 || idle.idleAt.IsZero() {
					continue
				}
				cleanup, cancel := context.WithTimeout(ctx, 3*time.Second)
				err := m.backend.Remove(cleanup, idle.path)
				cancel()
				if err != nil {
					continue
				}
				delete(m.connections, device)
				break
			}
		}
		if len(m.connections) >= m.options.MaxConnections {
			m.mu.Unlock()
			return Session{}, failure(429, "采集连接已达上限，请关闭其他摄像头后重试")
		}
		c = &connection{path: "lc_" + newID(), address: r.RTSPURL, version: r.ConfigVersion}
		if err := m.backend.Add(ctx, c.path, c.address); err != nil {
			m.mu.Unlock()
			return Session{}, err
		}
		m.connections[r.DeviceID] = c
		m.versions[r.DeviceID] = r.ConfigVersion
	}
	id := newID()
	view := Session{ID: id, DeviceID: r.DeviceID, ConfigVersion: r.ConfigVersion, State: "connecting", ExpiresAt: time.Now().Add(time.Duration(m.options.SessionTTLSeconds) * time.Second), PreviewURL: "/sessions/" + id + "/whep"}
	c.refs++
	c.idleAt = time.Time{}
	m.sessions[id] = &lease{view: view, source: c}
	m.mu.Unlock()
	if err := m.backend.WaitReady(ctx, c.path); err != nil {
		m.release(id, true)
		return Session{}, err
	}
	return m.Get(ctx, id, false)
}

func (m *Manager) active(id string) (*lease, error) {
	s := m.sessions[id]
	if s == nil || !time.Now().Before(s.view.ExpiresAt) {
		return nil, failure(404, "会话不存在或已超时，请重新打开摄像头")
	}
	return s, nil
}
func (m *Manager) Get(ctx context.Context, id string, renew bool) (Session, error) {
	m.mu.Lock()
	s, err := m.active(id)
	if err != nil {
		m.mu.Unlock()
		return Session{}, err
	}
	if renew {
		s.view.ExpiresAt = time.Now().Add(time.Duration(m.options.SessionTTLSeconds) * time.Second)
	}
	view := s.view
	path := s.source.path
	m.mu.Unlock()
	ready, err := m.backend.Ready(ctx, path)
	if err != nil {
		return Session{}, err
	}
	view.State = "reconnecting"
	if ready {
		view.State = "ready"
	}
	return view, nil
}
func (m *Manager) Path(id string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.active(id)
	if err != nil {
		return "", err
	}
	return s.source.path, nil
}

func (m *Manager) release(id string, force bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return
	}
	delete(m.sessions, id)
	s.source.refs--
	if s.source.refs == 0 {
		s.source.idleAt = time.Now().Add(time.Duration(m.options.IdleCloseSeconds) * time.Second)
		if force {
			s.source.idleAt = time.Now()
		}
	}
	m.cleanupLocked(time.Now())
}
func (m *Manager) Release(id string) { m.release(id, false) }

func (m *Manager) cleanupLocked(now time.Time) {
	for id, s := range m.sessions {
		if !now.Before(s.view.ExpiresAt) {
			delete(m.sessions, id)
			s.source.refs--
			if s.source.refs == 0 {
				s.source.idleAt = now.Add(time.Duration(m.options.IdleCloseSeconds) * time.Second)
			}
		}
	}
	for device, c := range m.connections {
		if c.refs == 0 && c.pins == 0 && !c.idleAt.IsZero() && !now.Before(c.idleAt) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := m.backend.Remove(ctx, c.path)
			cancel()
			if err == nil {
				delete(m.connections, device)
			}
		}
	}
}
func (m *Manager) Reap(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			m.mu.Lock()
			m.cleanupLocked(now)
			m.mu.Unlock()
		}
	}
}

func (m *Manager) Snapshot(ctx context.Context, id string) ([]byte, error) {
	select {
	case m.snapshots <- struct{}{}:
		defer func() { <-m.snapshots }()
	default:
		return nil, failure(429, "截图任务已满，请稍后重试")
	}
	m.mu.Lock()
	s, err := m.active(id)
	if err != nil {
		m.mu.Unlock()
		return nil, err
	}
	s.source.pins++
	path := s.source.path
	m.mu.Unlock()
	defer func() { m.mu.Lock(); s.source.pins--; m.cleanupLocked(time.Now()); m.mu.Unlock() }()
	data, err := m.backend.Snapshot(ctx, path)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	_, err = m.active(id)
	m.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return data, nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	m.sessions = map[string]*lease{}
	for device, c := range m.connections {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = m.backend.Remove(ctx, c.path)
		cancel()
		delete(m.connections, device)
	}
}
