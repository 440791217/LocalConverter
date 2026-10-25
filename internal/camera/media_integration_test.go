package camera

import (
	"bytes"
	"context"
	"image/jpeg"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Opt in after installing tools: LOCALCONVERTER_MEDIA_TEST=1 go test ./internal/camera -run TestRealMediaPipeline -v.
// Generates a clearly synthetic video source; never connects to a user's camera.
func TestRealMediaPipeline(t *testing.T) {
	testRealMediaPipeline(t, "libx264")
}

func TestRealMediaPipelineRejectsH265(t *testing.T) {
	testRealMediaPipeline(t, "libx265")
}

func testRealMediaPipeline(t *testing.T, codec string) {
	if os.Getenv("LOCALCONVERTER_MEDIA_TEST") != "1" {
		t.Skip("set LOCALCONVERTER_MEDIA_TEST=1 after installing portable media tools")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	freeAddress := func() string {
		listener, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		address := listener.Addr().String()
		_ = listener.Close()
		return address
	}
	o := DefaultMediaOptions()
	o.APIAddress = freeAddress()
	o.RTSPAddress = freeAddress()
	o.WebRTCAddress = freeAddress()
	o.ICEAddress = freeAddress()
	logger := log.New(os.Stdout, "", 0)
	media := NewMedia(o, `D:\projects\logs`, logger)
	defer media.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err = media.ensure(ctx); err != nil {
		t.Fatal(err)
	}
	// Publishing is restricted to the test-owned process and loopback credentials.
	users := []any{map[string]any{"user": media.user, "pass": media.password, "ips": []string{"127.0.0.1", "::1"}, "permissions": []map[string]string{{"action": "api"}, {"action": "read"}, {"action": "publish"}}}}
	if err = media.call(ctx, http.MethodPatch, "/v3/config/global/patch", map[string]any{"authInternalUsers": users}, nil); err != nil {
		t.Fatal(err)
	}
	if err = media.call(ctx, http.MethodPost, "/v3/config/paths/add/fixture", map[string]any{"source": "publisher"}, nil); err != nil {
		t.Fatal(err)
	}
	fixtureURL := url.URL{Scheme: "rtsp", Host: o.RTSPAddress, Path: "/fixture", User: url.UserPassword(media.user, media.password)}
	fixtureCtx, stopFixture := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error", "-re", "-f", "lavfi", "-i", "testsrc=size=320x240:rate=10", "-an", "-c:v", codec, "-threads", "2", "-pix_fmt", "yuv420p", "-preset", "ultrafast", "-tune", "zerolatency", "-g", "10"}
	if codec == "libx265" {
		args = append(args, "-x265-params", "pools=none:frame-threads=2:log-level=error")
	}
	args = append(args, "-f", "rtsp", "-rtsp_transport", "tcp", fixtureURL.String())
	fixture := exec.CommandContext(fixtureCtx, o.FFmpeg, args...)
	hideWindow(fixture)
	fixture.Stdout = io.Discard
	fixture.Stderr = io.Discard
	if err = fixture.Start(); err != nil {
		stopFixture()
		t.Fatal("test fixture process could not start")
	}
	defer func() { stopFixture(); _ = fixture.Wait() }()
	for {
		var state struct {
			Ready bool `json:"ready"`
		}
		if media.call(ctx, http.MethodGet, "/v3/paths/get/fixture", nil, &state) == nil && state.Ready {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("synthetic fixture not ready")
		case <-time.After(150 * time.Millisecond):
		}
	}
	opts := DefaultOptions()
	opts.IdleCloseSeconds = 0
	manager := NewManager(opts, media)
	defer manager.Close()
	s, err := manager.Open(ctx, OpenRequest{DeviceID: "synthetic-test-only", ConfigVersion: 1, RTSPURL: fixtureURL.String()})
	if codec == "libx265" {
		if e, ok := err.(*Error); !ok || e.Status != 422 {
			t.Fatalf("H265 must be rejected: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if s.State != "ready" {
		t.Fatalf("expected ready, got %s", s.State)
	}
	image, err := manager.Snapshot(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	size, err := jpeg.DecodeConfig(bytes.NewReader(image))
	if err != nil {
		t.Fatal(err)
	}
	if size.Width != 320 || size.Height != 240 {
		t.Fatalf("incorrect snapshot dimensions: %+v", size)
	}
	path, err := manager.Path(s.ID)
	if err != nil {
		t.Fatal(err)
	}
	manager.Release(s.ID)
	if ready, _ := media.Ready(ctx, path); ready {
		t.Fatal("released source still ready")
	}
	if _, err = manager.Snapshot(ctx, s.ID); err == nil {
		t.Fatal("closed session can still capture")
	}
	stopFixture()
	media.Close()
	select {
	case <-media.done:
	default:
		t.Fatal("owned MediaMTX process was not reaped")
	}
	t.Logf("real media test: synthetic H264 RTSP source -> shared source -> JPEG %dx%d (%d bytes), source and owned process released", size.Width, size.Height, len(image))
}
