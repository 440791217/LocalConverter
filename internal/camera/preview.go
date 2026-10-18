package camera

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// One shared compatibility encoder belongs to the original camera connection.
// Snapshots continue to read the original source, preserving its resolution.
type previewProcess struct {
	path   string
	cancel context.CancelFunc
	done   chan struct{}
}

func needsPreviewConversion(tracks []string) bool {
	for _, track := range tracks {
		if strings.EqualFold(track, "H265") {
			return true
		}
	}
	return false
}

func (m *Media) PreviewPath(source string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if preview := m.previews[source]; preview != nil {
		return preview.path
	}
	return source
}

func (m *Media) internalRTSP(path string) string {
	return (&url.URL{Scheme: "rtsp", Host: m.options.RTSPAddress, Path: "/" + path, User: url.UserPassword(m.user, m.password)}).String()
}

func (m *Media) ensurePreview(ctx context.Context, source string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return failure(503, "采集服务正在关闭")
	}
	if m.previews[source] != nil {
		return nil
	}
	path := source + "_h264"
	if err := m.call(ctx, http.MethodPost, "/v3/config/paths/add/"+path, map[string]any{"source": "publisher"}, nil); err != nil {
		return err
	}
	name := fmt.Sprintf("localconverter-preview-%s-%d-%s.log", time.Now().Format("20060102-150405"), os.Getpid(), newID())
	file, err := os.OpenFile(filepath.Join(m.logDirectory, name), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		_ = m.call(ctx, http.MethodDelete, "/v3/config/paths/delete/"+path, nil, nil)
		return failure(503, "无法创建预览转换日志")
	}
	processCtx, cancel := context.WithCancel(context.Background())
	preview := &previewProcess{path: path, cancel: cancel, done: make(chan struct{})}
	m.previews[source] = preview
	m.logger.Print("H265 camera uses shared H264 compatibility preview (up to 1280px, 15fps); snapshots retain source resolution")
	go func() {
		defer close(preview.done)
		defer file.Close()
		writer := &safeMediaLog{file: file}
		defer writer.Flush()
		for processCtx.Err() == nil {
			cmd := exec.CommandContext(processCtx, m.options.FFmpeg,
				"-hide_banner", "-loglevel", "warning", "-nostdin", "-rtsp_transport", "tcp", "-timeout", "5000000",
				"-threads", "2", "-i", m.internalRTSP(source), "-map", "0:v:0", "-an", "-sn", "-dn",
				"-vf", "scale=w='min(1280,iw)':h=-2,fps=15", "-c:v", "libx264", "-preset", "ultrafast", "-tune", "zerolatency",
				"-profile:v", "baseline", "-pix_fmt", "yuv420p", "-threads", "2", "-crf", "23", "-g", "15", "-bf", "0",
				"-f", "rtsp", "-rtsp_transport", "tcp", m.internalRTSP(path))
			hideWindow(cmd)
			cmd.Stdout = writer
			cmd.Stderr = writer
			if err := cmd.Start(); err == nil {
				release, err := protectProcess(cmd)
				if err != nil {
					_ = cmd.Process.Kill()
					_ = cmd.Wait()
					m.logger.Print("preview encoder process protection failed")
					return
				}
				_ = cmd.Wait()
				release()
			}
			if processCtx.Err() != nil {
				return
			}
			m.logger.Print("preview encoder stopped; retrying in one second")
			timer := time.NewTimer(time.Second)
			select {
			case <-processCtx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
	}()
	return nil
}

func (m *Media) removePreview(ctx context.Context, source string) error {
	m.mu.Lock()
	preview := m.previews[source]
	if preview != nil {
		delete(m.previews, source)
		preview.cancel()
	}
	m.mu.Unlock()
	if preview == nil {
		return nil
	}
	select {
	case <-preview.done:
	case <-ctx.Done():
		return failure(504, "预览转换停止超时")
	}
	err := m.call(ctx, http.MethodDelete, "/v3/config/paths/delete/"+preview.path, nil, nil)
	if e, ok := err.(*Error); ok && e.Status == 404 {
		return nil
	}
	return err
}
