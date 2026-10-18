package camera

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("secure random source unavailable")
	}
	return b
}

type ToolStatus struct {
	FFmpeg   bool `json:"ffmpeg"`
	MediaMTX bool `json:"mediamtx"`
	Running  bool `json:"running"`
}

// Media owns one MediaMTX process; RTSP source URLs are submitted through its API,
// never written into the generated configuration or returned to the browser.
type Media struct {
	options        MediaOptions
	logDirectory   string
	logger         *log.Logger
	user, password string
	mu             sync.Mutex
	cmd            *exec.Cmd
	done           chan struct{}
	cancel         context.CancelFunc
	logFile        *os.File
	configPath     string
	client         *http.Client
	closed         bool
	previews       map[string]*previewProcess
}

func NewMedia(o MediaOptions, logDirectory string, logger *log.Logger) *Media {
	return &Media{options: o, logDirectory: logDirectory, logger: logger, user: "localconverter", password: newID(), client: &http.Client{Timeout: 3 * time.Second}, previews: make(map[string]*previewProcess)}
}

func (m *Media) Tools() ToolStatus {
	_, ff := exec.LookPath(m.options.FFmpeg)
	_, mtx := exec.LookPath(m.options.MediaMTX)
	m.mu.Lock()
	defer m.mu.Unlock()
	running := false
	if m.done != nil {
		select {
		case <-m.done:
		default:
			running = true
		}
	}
	return ToolStatus{ff == nil, mtx == nil, running}
}

type safeMediaLog struct {
	mu      sync.Mutex
	file    io.Writer
	pending []byte
}

var authURL = regexp.MustCompile(`(?i)(rtsp[s]?://)[^\s]+`)

func (w *safeMediaLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		index := bytes.IndexByte(w.pending, '\n')
		if index < 0 {
			break
		}
		line := w.pending[:index+1]
		if _, err := w.file.Write(authURL.ReplaceAll(line, []byte(`${1}[redacted]`))); err != nil {
			return 0, err
		}
		w.pending = w.pending[index+1:]
	}
	if len(w.pending) > 65536 {
		w.pending = nil
		_, err := io.WriteString(w.file, "[overlong media log line omitted]\n")
		if err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

func (w *safeMediaLog) Flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		_, _ = w.file.Write(authURL.ReplaceAll(w.pending, []byte(`${1}[redacted]`)))
		w.pending = nil
	}
}

func (m *Media) ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return failure(503, "采集服务正在关闭")
	}
	if m.done != nil {
		select {
		case <-m.done:
			if m.cancel != nil {
				m.cancel()
			}
			if m.logFile != nil {
				_ = m.logFile.Close()
			}
			if m.configPath != "" {
				_ = os.Remove(m.configPath)
			}
		default:
			return nil
		}
	}
	if _, err := exec.LookPath(m.options.FFmpeg); err != nil {
		return failure(503, "未找到FFmpeg，请运行媒体工具安装脚本或检查media.ffmpeg配置")
	}
	if _, err := exec.LookPath(m.options.MediaMTX); err != nil {
		return failure(503, "未找到MediaMTX，请运行媒体工具安装脚本或检查media.mediamtx配置")
	}
	permission := []map[string]string{{"action": "api"}, {"action": "read"}, {"action": "publish"}}
	configuration := map[string]any{
		"logLevel": "warn", "logDestinations": []string{"stdout"}, "api": true, "apiAddress": m.options.APIAddress,
		"rtsp": true, "rtspAddress": m.options.RTSPAddress, "rtspTransports": []string{"tcp"},
		"rtmp": false, "hls": false, "srt": false, "moq": false, "playback": false, "metrics": false, "pprof": false,
		"webrtc": true, "webrtcAddress": m.options.WebRTCAddress, "webrtcLocalUDPAddress": m.options.ICEAddress,
		"webrtcLocalTCPAddress": "", "webrtcIPsFromInterfaces": false, "webrtcAdditionalHosts": []string{"127.0.0.1", "::1"}, "webrtcICEServers2": []any{}, "webrtcAllowOrigins": []string{},
		"authInternalUsers": []any{map[string]any{"user": m.user, "pass": m.password, "ips": []string{"127.0.0.1", "::1"}, "permissions": permission}},
		"paths":             map[string]any{},
	}
	data, _ := json.Marshal(configuration)
	if err := os.MkdirAll(".cache/media", 0700); err != nil {
		return failure(503, "无法创建媒体运行目录")
	}
	file, err := os.CreateTemp(".cache/media", "mediamtx-*.yml")
	if err != nil {
		return failure(503, "无法创建媒体配置")
	}
	m.configPath = file.Name()
	_ = file.Chmod(0600)
	if _, err = file.Write(data); err != nil {
		_ = file.Close()
		_ = os.Remove(m.configPath)
		return failure(503, "无法写入媒体配置")
	}
	_ = file.Close()
	logName := fmt.Sprintf("localconverter-mediamtx-%s-%d.log", time.Now().Format("20060102-150405"), os.Getpid())
	m.logFile, err = os.OpenFile(filepath.Join(m.logDirectory, logName), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_ = os.Remove(m.configPath)
		return failure(503, "无法创建媒体日志")
	}
	processCtx, cancel := context.WithCancel(context.Background())
	m.cancel = cancel
	m.cmd = exec.CommandContext(processCtx, m.options.MediaMTX, m.configPath)
	hideWindow(m.cmd)
	writer := &safeMediaLog{file: m.logFile}
	m.cmd.Stdout = writer
	m.cmd.Stderr = writer
	if err = m.cmd.Start(); err != nil {
		cancel()
		_ = m.logFile.Close()
		_ = os.Remove(m.configPath)
		return failure(503, "MediaMTX启动失败，请检查可执行文件")
	}
	releaseChild, err := protectProcess(m.cmd)
	if err != nil {
		cancel()
		_ = m.cmd.Wait()
		_ = m.logFile.Close()
		_ = os.Remove(m.configPath)
		return failure(503, "无法设置媒体子进程退出保护")
	}
	done := make(chan struct{})
	m.done = done
	cmd := m.cmd
	go func() { _ = cmd.Wait(); writer.Flush(); releaseChild(); close(done) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			return failure(503, "媒体启动请求已取消")
		case <-done:
			return failure(503, "MediaMTX退出，请检查媒体日志和端口占用")
		case <-deadline.C:
			cancel()
			return failure(503, "MediaMTX启动超时，请检查媒体日志和端口占用")
		case <-ticker.C:
			if m.call(ctx, http.MethodGet, "/v3/config/global/get", nil, nil) == nil {
				return nil
			}
		}
	}
}

func (m *Media) call(ctx context.Context, method, path string, body any, result any) error {
	var reader io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		reader = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://"+m.options.APIAddress+path, reader)
	if err != nil {
		return failure(503, "媒体请求创建失败")
	}
	req.SetBasicAuth(m.user, m.password)
	req.Header.Set("Content-Type", "application/json")
	response, err := m.client.Do(req)
	if err != nil {
		return failure(503, "媒体服务不可用")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return failure(response.StatusCode, "媒体操作失败")
	}
	if result != nil {
		if json.NewDecoder(io.LimitReader(response.Body, 1024*1024)).Decode(result) != nil {
			return failure(502, "媒体响应格式错误")
		}
	}
	return nil
}

func (m *Media) Add(ctx context.Context, path, address string) error {
	if err := m.ensure(ctx); err != nil {
		return err
	}
	return m.call(ctx, http.MethodPost, "/v3/config/paths/add/"+path, map[string]any{"source": address, "sourceOnDemand": false, "rtspTransport": "tcp"}, nil)
}
func (m *Media) Remove(ctx context.Context, path string) error {
	if err := m.removePreview(ctx, path); err != nil {
		return err
	}
	err := m.call(ctx, http.MethodDelete, "/v3/config/paths/delete/"+path, nil, nil)
	if e, ok := err.(*Error); ok && e.Status == 404 {
		return nil
	}
	return err
}
func (m *Media) Ready(ctx context.Context, path string) (bool, error) {
	var state struct {
		Ready  bool     `json:"ready"`
		Tracks []string `json:"tracks"`
	}
	err := m.call(ctx, http.MethodGet, "/v3/paths/get/"+path, nil, &state)
	if e, ok := err.(*Error); ok && e.Status == 404 {
		return false, nil
	}
	if err != nil || !state.Ready {
		return state.Ready, err
	}
	if needsPreviewConversion(state.Tracks) {
		if err := m.ensurePreview(ctx, path); err != nil {
			return false, err
		}
		var preview struct {
			Ready bool `json:"ready"`
		}
		err = m.call(ctx, http.MethodGet, "/v3/paths/get/"+m.PreviewPath(path), nil, &preview)
		if e, ok := err.(*Error); ok && e.Status == 404 {
			return false, nil
		}
		return preview.Ready, err
	}
	return true, nil
}
func (m *Media) WaitReady(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.options.ConnectTimeoutSeconds)*time.Second)
	defer cancel()
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	for {
		ready, err := m.Ready(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return failure(504, "摄像头连接超时，请检查RTSP地址、认证和工位网络")
			}
			return err
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return failure(504, "摄像头连接超时，请检查RTSP地址、认证和工位网络")
		case <-ticker.C:
		}
	}
}

func (m *Media) PreviewTarget() (*url.URL, string, string) {
	u, _ := url.Parse("http://" + m.options.WebRTCAddress)
	return u, m.user, m.password
}
func (m *Media) Snapshot(ctx context.Context, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(m.options.SnapshotTimeoutSeconds)*time.Second)
	defer cancel()
	u := url.URL{Scheme: "rtsp", Host: m.options.RTSPAddress, Path: "/" + path, User: url.UserPassword(m.user, m.password)}
	cmd := exec.CommandContext(ctx, m.options.FFmpeg, "-hide_banner", "-loglevel", "error", "-nostdin", "-rtsp_transport", "tcp", "-timeout", "5000000", "-i", u.String(), "-map", "0:v:0", "-an", "-sn", "-dn", "-frames:v", "1", "-threads", "1", "-c:v", "mjpeg", "-q:v", "3", "-f", "image2pipe", "pipe:1")
	hideWindow(cmd)
	output := &limitedImage{limit: 5 * 1024 * 1024}
	diagnostic := &limitedImage{limit: 8192}
	cmd.Stdout = output
	cmd.Stderr = diagnostic
	var runErr error
	if runErr = cmd.Start(); runErr == nil {
		releaseChild, err := protectProcess(cmd)
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, failure(503, "无法设置截图子进程退出保护")
		}
		runErr = cmd.Wait()
		releaseChild()
	}
	if runErr != nil {
		message := authURL.ReplaceAllString(diagnostic.String(), `${1}[redacted]`)
		message = strings.ReplaceAll(message, m.password, "[redacted]")
		m.logger.Printf("snapshot failed: %s", strings.TrimSpace(message))
		if ctx.Err() != nil {
			return nil, failure(504, "摄像头截图超时")
		}
		if output.tooLarge {
			return nil, failure(413, "截图超过5MiB，请选择较低分辨率的码流")
		}
		return nil, failure(502, "截图失败，请检查码流和媒体工具")
	}
	data := output.Bytes()
	if len(data) < 4 || data[0] != 255 || data[1] != 216 || data[len(data)-2] != 255 || data[len(data)-1] != 217 {
		return nil, failure(502, "摄像头未返回有效JPEG图片")
	}
	return data, nil
}

type limitedImage struct {
	bytes.Buffer
	limit    int
	tooLarge bool
}

func (b *limitedImage) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		b.tooLarge = true
		return 0, fmt.Errorf("image too large")
	}
	return b.Buffer.Write(p)
}

func (m *Media) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
	for _, preview := range m.previews {
		preview.cancel()
	}
	for _, preview := range m.previews {
		select {
		case <-preview.done:
		case <-time.After(5 * time.Second):
		}
	}
	m.previews = make(map[string]*previewProcess)
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		select {
		case <-m.done:
		case <-time.After(5 * time.Second):
		}
	}
	if m.logFile != nil {
		_ = m.logFile.Close()
	}
	if m.configPath != "" {
		_ = os.Remove(m.configPath)
	}
}
