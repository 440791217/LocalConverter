package camera

import (
	"bytes"
	"strings"
	"testing"
)

func TestMediaLogRedactsURLsAcrossProcessOutputChunks(t *testing.T) {
	var output bytes.Buffer
	writer := &safeMediaLog{file: &output}
	for _, chunk := range []string{"error rtsp://operator:top", "secret@192.0.2.10/live?token=hidden", " failed\nnext line\ntrailing rtsp://user:pass@host/live"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	writer.Flush()
	result := output.String()
	for _, secret := range []string{"topsecret", "operator", "192.0.2.10", "hidden", "user:pass"} {
		if strings.Contains(result, secret) {
			t.Fatalf("media log leaked %q", secret)
		}
	}
	if !strings.Contains(result, "next line") {
		t.Fatal("ordinary diagnostics were lost")
	}
}

func TestSnapshotOutputRejectsUnboundedImageAllocation(t *testing.T) {
	buffer := &limitedImage{limit: 4}
	if _, err := buffer.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte{4, 5}); err == nil || !buffer.tooLarge || buffer.Len() != 3 {
		t.Fatal("image output limit was not enforced")
	}
}
