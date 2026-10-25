package camera

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeMedia struct {
	mu               sync.Mutex
	adds, removes    int
	waitError        error
	snapshotStarted  chan struct{}
	snapshotContinue chan struct{}
}

func (f *fakeMedia) Add(context.Context, string, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.adds++
	return nil
}
func (f *fakeMedia) Remove(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removes++
	return nil
}
func (f *fakeMedia) Ready(context.Context, string) (bool, error) { return true, nil }
func (f *fakeMedia) WaitReady(context.Context, string) error     { return f.waitError }
func (f *fakeMedia) Snapshot(ctx context.Context, path string) ([]byte, error) {
	if f.snapshotStarted != nil {
		close(f.snapshotStarted)
		select {
		case <-f.snapshotContinue:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return []byte{255, 216, 255, 217}, nil
}
func validRequest() OpenRequest {
	return OpenRequest{DeviceID: "camera-1", ConfigVersion: 1, RTSPURL: "rtsp://user:p%40ss@192.0.2.10/live?channel=1"}
}
func requireStatus(t *testing.T, err error, status int) {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) || e.Status != status {
		t.Fatalf("expected %d, got %v", status, err)
	}
}

func TestSharedConnectionAndIndependentSessions(t *testing.T) {
	b := &fakeMedia{}
	o := DefaultOptions()
	o.IdleCloseSeconds = 0
	m := NewManager(o, b)
	defer m.Close()
	a, err := m.Open(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Open(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == c.ID || b.adds != 1 {
		t.Fatal("sessions must share exactly one media source")
	}
	m.Release(a.ID)
	if b.removes != 0 {
		t.Fatal("closing one viewer removed another viewer's source")
	}
	if _, err = m.Snapshot(context.Background(), c.ID); err != nil {
		t.Fatal(err)
	}
	m.Release(c.ID)
	m.Release(c.ID)
	if b.removes != 1 {
		t.Fatalf("media removed %d times", b.removes)
	}
}

func TestNewCameraCanReplaceIdleSlotButNeverAnotherViewersSource(t *testing.T) {
	b := &fakeMedia{}
	o := DefaultOptions()
	o.MaxConnections = 1
	m := NewManager(o, b)
	defer m.Close()
	a, err := m.Open(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	next := validRequest()
	next.DeviceID = "camera-2"
	_, err = m.Open(context.Background(), next)
	requireStatus(t, err, 429)
	if b.removes != 0 {
		t.Fatal("active source was evicted")
	}
	m.Release(a.ID)
	if b.removes != 0 {
		t.Fatal("idle grace period should normally retain the source")
	}
	if _, err = m.Open(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if len(m.connections) != 1 || b.removes != 1 || b.adds != 2 {
		t.Fatal("idle source did not yield its capacity slot")
	}
}
func TestTimeoutAndFailedOpenReleaseSources(t *testing.T) {
	b := &fakeMedia{waitError: failure(504, "timeout")}
	m := NewManager(DefaultOptions(), b)
	defer m.Close()
	_, err := m.Open(context.Background(), validRequest())
	requireStatus(t, err, 504)
	if len(m.sessions) != 0 || len(m.connections) != 0 || b.removes != 1 {
		t.Fatal("failed open leaked a connection")
	}
	b.waitError = nil
	s, err := m.Open(context.Background(), validRequest())
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].view.ExpiresAt = time.Now().Add(-time.Second)
	m.cleanupLocked(time.Now())
	m.cleanupLocked(time.Now().Add(10 * time.Second))
	m.mu.Unlock()
	if _, err = m.Get(context.Background(), s.ID, true); err == nil {
		t.Fatal("expired session was renewed")
	}
	if len(m.connections) != 0 {
		t.Fatal("expired session retained media")
	}
}
func TestConfigChangesAndConcurrencyLimits(t *testing.T) {
	b := &fakeMedia{}
	o := DefaultOptions()
	o.MaxConnections = 1
	o.MaxSessions = 2
	o.IdleCloseSeconds = 0
	m := NewManager(o, b)
	defer m.Close()
	a, _ := m.Open(context.Background(), validRequest())
	r := validRequest()
	r.DeviceID = "camera-2"
	_, err := m.Open(context.Background(), r)
	requireStatus(t, err, 429)
	r = validRequest()
	r.RTSPURL = "rtsp://192.0.2.11/live"
	_, err = m.Open(context.Background(), r)
	requireStatus(t, err, 409)
	m.Release(a.ID)
	r.ConfigVersion = 2
	s, err := m.Open(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	m.Release(s.ID)
	_, err = m.Open(context.Background(), validRequest())
	requireStatus(t, err, 409)
}
func TestSnapshotLimitAndReleaseWhileCapturing(t *testing.T) {
	b := &fakeMedia{snapshotStarted: make(chan struct{}), snapshotContinue: make(chan struct{})}
	o := DefaultOptions()
	o.MaxConcurrentSnapshots = 1
	o.IdleCloseSeconds = 0
	m := NewManager(o, b)
	defer m.Close()
	s, _ := m.Open(context.Background(), validRequest())
	done := make(chan error, 1)
	go func() { _, err := m.Snapshot(context.Background(), s.ID); done <- err }()
	<-b.snapshotStarted
	_, err := m.Snapshot(context.Background(), s.ID)
	requireStatus(t, err, 429)
	m.Release(s.ID)
	if b.removes != 0 {
		t.Fatal("media removed while screenshot was reading")
	}
	close(b.snapshotContinue)
	requireStatus(t, <-done, 404)
	if b.removes != 1 {
		t.Fatal("pinned media was not released after capture")
	}
}
func TestAddressValidationAndNoCredentialExposure(t *testing.T) {
	for _, address := range []string{"http://host/live", "rtsp://host:65536/live", "rtsp://host:0/live", "rtsp://host/live#password", "rtsp://host/a b", "rtsp://"} {
		r := validRequest()
		r.RTSPURL = address
		requireStatus(t, ValidateRequest(r), 400)
	}
	r := validRequest()
	if err := ValidateRequest(r); err != nil {
		t.Fatal(err)
	}
}
