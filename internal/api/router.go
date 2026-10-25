// Package api exposes same-origin local diagnostics and camera session endpoints.
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"

	"localconverter/internal/camera"
)

//go:embed web/*
var webFiles embed.FS

type Runtime struct {
	Manager          *camera.Manager
	Media            *camera.Media
	Token            string
	Host             string
	HeartbeatSeconds int
	Shutdown         func()
	AllowedOrigins   []string
}

func (rt Runtime) validHost(host string) bool {
	return host == rt.Host || (rt.Host == "127.0.0.1:18090" && host == "localhost:18090")
}

func (rt Runtime) allowedOrigin(origin string) bool {
	if origin == "http://"+rt.Host || (rt.Host == "127.0.0.1:18090" && origin == "http://localhost:18090") {
		return true
	}
	for _, allowed := range rt.AllowedOrigins {
		if origin == allowed {
			return true
		}
	}
	return false
}

func NewRuntime(manager *camera.Manager, media *camera.Media, host string, heartbeat int, configuredToken ...string) Runtime {
	if len(configuredToken) > 0 && configuredToken[0] != "" {
		return Runtime{Manager: manager, Media: media, Token: configuredToken[0], Host: host, HeartbeatSeconds: heartbeat}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return Runtime{Manager: manager, Media: media, Token: hex.EncodeToString(b), Host: host, HeartbeatSeconds: heartbeat}
}
func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func apiError(w http.ResponseWriter, err error) {
	var e *camera.Error
	if errors.As(err, &e) {
		jsonResponse(w, e.Status, map[string]string{"error": e.Message})
		return
	}
	jsonResponse(w, 500, map[string]string{"error": "采集服务内部错误"})
}

func (rt Runtime) authorize(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rt.validHost(r.Host) {
			jsonResponse(w, 403, map[string]string{"error": "请求主机不匹配，请使用配置中的本机地址"})
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !rt.allowedOrigin(origin) {
				jsonResponse(w, 403, map[string]string{"error": "不允许该网页调用本地采集服务"})
				return
			}
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(rt.Token)) != 1 {
			jsonResponse(w, 401, map[string]string{"error": "采集服务访问凭证无效，请从服务调试页重新获取"})
			return
		}
		next(w, r)
	}
}

func NewHandler(logger *log.Logger, rt Runtime) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]string{"service": "localconverter", "status": "ok"})
	})
	page := template.Must(template.ParseFS(webFiles, "web/index.html"))
	servePage := func(w http.ResponseWriter, r *http.Request) {
		if !rt.validHost(r.Host) {
			http.Error(w, "请求主机不匹配", 403)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := page.Execute(w, map[string]any{"Token": rt.Token, "HeartbeatSeconds": rt.HeartbeatSeconds}); err != nil {
			logger.Print("debug page response failed")
		}
	}
	mux.HandleFunc("GET /{$}", servePage)
	mux.HandleFunc("GET /debug", servePage)
	assets, _ := fs.Sub(webFiles, "web")
	mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	mux.HandleFunc("GET /tools", rt.authorize(func(w http.ResponseWriter, r *http.Request) { jsonResponse(w, 200, rt.Media.Tools()) }))
	mux.HandleFunc("POST /shutdown", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			jsonResponse(w, 403, map[string]string{"error": "请在采集服务本机关闭服务"})
			return
		}
		if rt.Shutdown == nil {
			jsonResponse(w, 503, map[string]string{"error": "当前实例不支持关闭"})
			return
		}
		jsonResponse(w, 202, map[string]string{"status": "stopping"})
		rt.Shutdown()
	}))
	mux.HandleFunc("POST /sessions", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
			jsonResponse(w, 415, map[string]string{"error": "请使用application/json请求"})
			return
		}
		var input camera.OpenRequest
		decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&input); err != nil {
			jsonResponse(w, 400, map[string]string{"error": "请求JSON格式或字段不正确"})
			return
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			jsonResponse(w, 400, map[string]string{"error": "请求只能包含一个JSON对象"})
			return
		}
		session, err := rt.Manager.Open(r.Context(), input)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 201, session)
	}))
	mux.HandleFunc("GET /sessions/{id}", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		s, err := rt.Manager.Get(r.Context(), r.PathValue("id"), false)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, s)
	}))
	mux.HandleFunc("POST /sessions/{id}/heartbeat", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		s, err := rt.Manager.Get(r.Context(), r.PathValue("id"), true)
		if err != nil {
			apiError(w, err)
			return
		}
		jsonResponse(w, 200, s)
	}))
	mux.HandleFunc("DELETE /sessions/{id}", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		rt.Manager.Release(r.PathValue("id"))
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /sessions/{id}/snapshot", rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		data, err := rt.Manager.Snapshot(r.Context(), r.PathValue("id"))
		if err != nil {
			apiError(w, err)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}))
	proxy := rt.authorize(func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		path, err := rt.Manager.Path(id)
		if err != nil {
			apiError(w, err)
			return
		}
		// H.264 is forwarded without re-encoding.
		prefix := "/sessions/" + id + "/whep"
		suffix := strings.TrimPrefix(r.URL.Path, prefix)
		if suffix != "" && (strings.Contains(strings.TrimPrefix(suffix, "/"), "/") || strings.Contains(suffix, "..")) {
			http.NotFound(w, r)
			return
		}
		target, user, password := rt.Media.PreviewTarget()
		backend := httputil.NewSingleHostReverseProxy(target)
		original := backend.Director
		backend.Director = func(req *http.Request) {
			original(req)
			req.URL.Path = "/" + path + "/whep" + suffix
			req.URL.RawPath = ""
			req.URL.RawQuery = ""
			req.Host = target.Host
			req.Header.Del("Origin")
			// The gateway has already checked the browser origin and bearer token.
			// This internal request is server-to-server, so browser fetch metadata
			// must not make MediaMTX treat it as an unapproved cross-site POST.
			req.Header.Del("Sec-Fetch-Site")
			req.SetBasicAuth(user, password)
		}
		backend.ModifyResponse = func(response *http.Response) error {
			if location := response.Header.Get("Location"); location != "" {
				u, err := url.Parse(location)
				if err != nil {
					return err
				}
				expected := "/" + path + "/whep"
				if !strings.HasPrefix(u.Path, expected) {
					return errors.New("invalid media resource")
				}
				response.Header.Set("Location", prefix+strings.TrimPrefix(u.Path, expected))
			}
			response.Header.Del("Access-Control-Allow-Origin")
			response.Header.Set("Cache-Control", "no-store")
			return nil
		}
		backend.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
			jsonResponse(w, 502, map[string]string{"error": "视频信令连接失败，请重新连接摄像头"})
		}
		backend.ServeHTTP(w, r)
	})
	mux.HandleFunc("POST /sessions/{id}/whep", proxy)
	mux.HandleFunc("OPTIONS /sessions/{id}/whep", proxy)
	mux.HandleFunc("PATCH /sessions/{id}/whep/{resource}", proxy)
	mux.HandleFunc("DELETE /sessions/{id}/whep/{resource}", proxy)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rt.validHost(r.Host) {
			http.Error(w, "请求主机不匹配", 403)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && origin != "http://"+r.Host {
			if !rt.allowedOrigin(origin) || r.URL.Path == "/debug" || r.URL.Path == "/" || strings.HasPrefix(r.URL.Path, "/assets/") || r.URL.Path == "/shutdown" {
				jsonResponse(w, 403, map[string]string{"error": "不允许该网页调用此接口"})
				return
			}
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Expose-Headers", "Location, Link, ETag, Accept-Patch")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				method := r.Header.Get("Access-Control-Request-Method")
				if method != "GET" && method != "POST" && method != "DELETE" && method != "PATCH" && method != "OPTIONS" {
					w.WriteHeader(403)
					return
				}
				for _, h := range strings.Split(r.Header.Get("Access-Control-Request-Headers"), ",") {
					switch strings.ToLower(strings.TrimSpace(h)) {
					case "", "authorization", "content-type", "if-match":
					default:
						w.WriteHeader(403)
						return
					}
				}
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, PATCH, OPTIONS")
				w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match")
				w.WriteHeader(204)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' blob:; media-src 'self' blob:; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}
