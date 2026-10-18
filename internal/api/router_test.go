package api

import (
	"context"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"localconverter/internal/camera"
)

type proxyTestBackend struct{}

func TestConfiguredTokenSurvivesRuntimeRecreation(t *testing.T) {
	for i := 0; i < 2; i++ {
		runtime := NewRuntime(nil, nil, "127.0.0.1:18090", 10, "88888888")
		if runtime.Token != "88888888" {
			t.Fatal("configured token changed")
		}
	}
}

func (proxyTestBackend) Add(context.Context, string, string) error        { return nil }
func (proxyTestBackend) Remove(context.Context, string) error             { return nil }
func (proxyTestBackend) Ready(context.Context, string) (bool, error)      { return true, nil }
func (proxyTestBackend) WaitReady(context.Context, string) error          { return nil }
func (proxyTestBackend) Snapshot(context.Context, string) ([]byte, error) { return nil, nil }

func TestWHEPProxyChecksBrowserOriginBeforeInternalForwarding(t *testing.T) {
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		// MediaMTX rejects cross-site POSTs after the proxy removes Origin.
		if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
			w.WriteHeader(403)
			return
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Basic ") {
			t.Error("internal authentication missing")
		}
		w.Header().Set("Location", r.URL.Path+"/media-resource")
		w.WriteHeader(201)
	}))
	defer upstream.Close()
	logger := log.New(io.Discard, "", 0)
	options := camera.DefaultMediaOptions()
	options.WebRTCAddress = strings.TrimPrefix(upstream.URL, "http://")
	media := camera.NewMedia(options, t.TempDir(), logger)
	defer media.Close()
	manager := camera.NewManager(camera.DefaultOptions(), proxyTestBackend{})
	defer manager.Close()
	session, err := manager.Open(context.Background(), camera.OpenRequest{DeviceID: "camera", RTSPURL: "rtsp://camera/live"})
	if err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(manager, media, "127.0.0.1:18090", 10)
	runtime.AllowedOrigins = []string{"http://localhost:9527"}
	handler := NewHandler(logger, runtime)
	for _, site := range []string{"same-site", "cross-site"} {
		req := httptest.NewRequest("POST", "http://localhost:18090"+session.PreviewURL, strings.NewReader("offer"))
		req.Header.Set("Origin", "http://localhost:9527")
		req.Header.Set("Sec-Fetch-Site", site)
		req.Header.Set("Authorization", "Bearer "+runtime.Token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != 201 {
			t.Fatalf("%s: expected 201, got %d", site, response.Code)
		}
		if response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:9527" {
			t.Fatal("missing browser CORS response")
		}
		if response.Header().Get("Location") != session.PreviewURL+"/media-resource" {
			t.Fatal("internal location exposed")
		}
	}
	req := httptest.NewRequest("POST", "http://localhost:18090"+session.PreviewURL, nil)
	req.Header.Set("Origin", "http://untrusted.example")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("Authorization", "Bearer "+runtime.Token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != 403 || calls != 2 {
		t.Fatal("untrusted browser reached internal media server")
	}
}

func TestLocalAPIRejectsCrossOriginAndMissingToken(t *testing.T) {
	logger := log.New(io.Discard, "", 0)
	media := camera.NewMedia(camera.DefaultMediaOptions(), t.TempDir(), logger)
	defer media.Close()
	manager := camera.NewManager(camera.DefaultOptions(), media)
	defer manager.Close()
	runtime := NewRuntime(manager, media, "127.0.0.1:18090", 10)
	runtime.AllowedOrigins = []string{"http://localhost:9527"}
	handler := NewHandler(logger, runtime)
	for _, test := range []struct {
		host, origin, token string
		status              int
	}{
		{"127.0.0.1:18090", "", "", 401},
		{"127.0.0.1:18090", "https://untrusted.example", runtime.Token, 403},
		{"untrusted.example:18090", "", runtime.Token, 403},
		{"127.0.0.1:18090", "http://127.0.0.1:18090", runtime.Token, 200},
		{"localhost:18090", "http://localhost:9527", runtime.Token, 200},
		{"localhost:18090", "http://localhost:9527", "", 401},
		{"localhost:18090", "http://localhost:9528", runtime.Token, 403},
	} {
		req := httptest.NewRequest("GET", "http://"+test.host+"/tools", nil)
		req.Header.Set("Origin", test.origin)
		req.Header.Set("Authorization", "Bearer "+test.token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if response.Code != test.status {
			t.Fatalf("expected %d, got %d", test.status, response.Code)
		}
	}
	for _, path := range []string{"/tools", "/debug", "/shutdown"} {
		req := httptest.NewRequest("OPTIONS", "http://localhost:18090"+path, nil)
		req.Header.Set("Origin", "http://localhost:9527")
		req.Header.Set("Access-Control-Request-Method", "GET")
		req.Header.Set("Access-Control-Request-Headers", "authorization")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		if path == "/tools" {
			if response.Code != 204 || response.Header().Get("Access-Control-Allow-Origin") != "http://localhost:9527" {
				t.Fatal("trusted preflight rejected")
			}
		} else if response.Code != 403 {
			t.Fatal("debug or shutdown exposed cross-origin")
		}
	}
	request := httptest.NewRequest("GET", "http://127.0.0.1:18090/debug", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 || !strings.Contains(response.Body.String(), runtime.Token) {
		t.Fatal("debug page must supply the current local token")
	}
	if response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("debug page can be framed")
	}
}
