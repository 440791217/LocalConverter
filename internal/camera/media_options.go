package camera

import (
	"fmt"
	"net"
	"strconv"
)

type MediaOptions struct {
	FFmpeg                 string `json:"ffmpeg"`
	MediaMTX               string `json:"mediamtx"`
	APIAddress             string `json:"api_address"`
	RTSPAddress            string `json:"rtsp_address"`
	WebRTCAddress          string `json:"webrtc_address"`
	ICEAddress             string `json:"ice_address"`
	ConnectTimeoutSeconds  int    `json:"connect_timeout_seconds"`
	SnapshotTimeoutSeconds int    `json:"snapshot_timeout_seconds"`
}

func DefaultMediaOptions() MediaOptions {
	return MediaOptions{FFmpeg: "tools/ffmpeg/ffmpeg.exe", MediaMTX: "tools/mediamtx/mediamtx.exe",
		APIAddress: "127.0.0.1:19997", RTSPAddress: "127.0.0.1:18554", WebRTCAddress: "127.0.0.1:18889", ICEAddress: "127.0.0.1:18189",
		ConnectTimeoutSeconds: 15, SnapshotTimeoutSeconds: 10}
}

func (o MediaOptions) Validate() error {
	if o.FFmpeg == "" || o.MediaMTX == "" {
		return fmt.Errorf("media executable paths are required")
	}
	seen := map[string]bool{}
	for _, address := range []string{o.APIAddress, o.RTSPAddress, o.WebRTCAddress, o.ICEAddress} {
		host, port, err := net.SplitHostPort(address)
		ip := net.ParseIP(host)
		n, e := strconv.Atoi(port)
		if err != nil || e != nil || ip == nil || !ip.IsLoopback() || n < 1 || n > 65535 || seen[address] {
			return fmt.Errorf("media listeners must be distinct numeric loopback addresses with valid ports")
		}
		seen[address] = true
	}
	if o.ConnectTimeoutSeconds < 1 || o.ConnectTimeoutSeconds > 60 || o.SnapshotTimeoutSeconds < 1 || o.SnapshotTimeoutSeconds > 60 {
		return fmt.Errorf("media timeouts must be between 1 and 60 seconds")
	}
	return nil
}
