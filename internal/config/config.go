// Package config reads and validates service configuration.
package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"

	"localconverter/internal/camera"
)

type Config struct {
	Listen         string              `json:"listen"`
	LogDirectory   string              `json:"log_directory"`
	Camera         camera.Options      `json:"camera"`
	Media          camera.MediaOptions `json:"media"`
	AllowedOrigins []string            `json:"allowed_origins"`
	AccessToken    string              `json:"access_token"`
}

func Default() Config {
	return Config{
		Listen: "127.0.0.1:18090", LogDirectory: `D:\projects\logs`,
		Camera:         camera.DefaultOptions(),
		Media:          camera.DefaultMediaOptions(),
		AllowedOrigins: []string{"http://localhost:9527", "http://127.0.0.1:9527"},
		AccessToken:    "88888888",
	}
}

// Load overlays a JSON file on defaults. An empty path uses defaults.
// Validate separately so command-line overrides can be applied first.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, fmt.Errorf("open configuration: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	target := &cfg
	if err := decoder.Decode(&target); err != nil {
		return cfg, fmt.Errorf("decode configuration: %w", err)
	}
	if target == nil {
		return cfg, fmt.Errorf("configuration must be a JSON object")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return cfg, fmt.Errorf("configuration must contain exactly one JSON object")
	}
	return cfg, nil
}

func (c Config) Validate() error {
	for _, origin := range c.AllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Hostname() == "*" {
			return fmt.Errorf("allowed_origins must contain exact HTTP origins without paths")
		}
	}
	host, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return fmt.Errorf("listen must contain a host and port")
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("only numeric loopback addresses are supported until access authentication is implemented")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return fmt.Errorf("listen port must be between 1 and 65535")
	}
	if !filepath.IsAbs(c.LogDirectory) {
		return fmt.Errorf("log_directory must be an absolute path")
	}
	if err := c.Media.Validate(); err != nil {
		return err
	}
	for _, address := range []string{c.Media.APIAddress, c.Media.RTSPAddress, c.Media.WebRTCAddress, c.Media.ICEAddress} {
		if address == c.Listen {
			return fmt.Errorf("HTTP and media listeners must not overlap")
		}
	}
	return c.Camera.Validate()
}
