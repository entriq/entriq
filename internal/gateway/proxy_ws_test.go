package gateway

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entriq/internal/config"
)

// wsEchoServer returns an httptest.Server that performs a raw WebSocket
// upgrade handshake and then echoes every frame payload back to the client.
// It only handles unfragmented, unmasked text frames (opcode 0x1) for simplicity.
func wsEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
			http.Error(w, "not a ws request", http.StatusBadRequest)
			return
		}

		// Compute accept key per RFC 6455
		key := r.Header.Get("Sec-WebSocket-Key")
		h := sha1.New()
		h.Write([]byte(key + "258EAFA5-E914-47DA-95CA-5AB5DC11885B"))
		accept := base64.StdEncoding.EncodeToString(h.Sum(nil))

		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Fatal("server does not support hijack")
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			t.Fatalf("hijack: %v", err)
		}
		defer conn.Close()

		// Send 101 response
		bufrw.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
		bufrw.WriteString("Upgrade: websocket\r\n")
		bufrw.WriteString("Connection: Upgrade\r\n")
		bufrw.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n")
		bufrw.WriteString("\r\n")
		bufrw.Flush()

		// Echo loop: read a frame, write it back
		for {
			frame, err := readWSFrame(conn)
			if err != nil {
				return // client closed
			}
			writeWSFrame(conn, frame)
		}
	}))
}

// readWSFrame reads one unfragmented WS text frame and returns the payload.
func readWSFrame(r io.Reader) ([]byte, error) {
	header := make([]byte, 2)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	masked := header[1]&0x80 != 0
	length := int(header[1] & 0x7F)

	switch {
	case length == 126:
		ext := make([]byte, 2)
		if _, err := io.ReadFull(r, ext); err != nil {
			return nil, err
		}
		length = int(ext[0])<<8 | int(ext[1])
	case length == 127:
		ext := make([]byte, 8)
		if _, err := io.ReadFull(r, ext); err != nil {
			return nil, err
		}
		length = 0
		for _, b := range ext {
			length = length<<8 | int(b)
		}
	}

	var maskKey [4]byte
	if masked {
		if _, err := io.ReadFull(r, maskKey[:]); err != nil {
			return nil, err
		}
	}

	payload := make([]byte, length)
	if _, err := io.ReadFull(r, payload); err != nil {
		return nil, err
	}

	if masked {
		for i := range payload {
			payload[i] ^= maskKey[i%4]
		}
	}

	return payload, nil
}

// writeWSFrame writes an unmasked text frame.
func writeWSFrame(w io.Writer, payload []byte) {
	frame := []byte{0x81} // FIN + text opcode
	length := len(payload)
	if length < 126 {
		frame = append(frame, byte(length))
	} else if length < 65536 {
		frame = append(frame, 126, byte(length>>8), byte(length))
	}
	frame = append(frame, payload...)
	w.Write(frame)
}

// buildMaskedFrame builds a masked text frame (client → server must be masked).
func buildMaskedFrame(payload []byte) []byte {
	frame := []byte{0x81} // FIN + text opcode
	length := len(payload)
	maskKey := [4]byte{0x12, 0x34, 0x56, 0x78}

	if length < 126 {
		frame = append(frame, byte(length)|0x80) // mask bit set
	} else if length < 65536 {
		frame = append(frame, 126|0x80, byte(length>>8), byte(length))
	}
	frame = append(frame, maskKey[:]...)
	masked := make([]byte, length)
	for i, b := range payload {
		masked[i] = b ^ maskKey[i%4]
	}
	frame = append(frame, masked...)
	return frame
}

func TestIsWebSocketUpgrade(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string]string
		expected bool
	}{
		{"valid upgrade", map[string]string{"Connection": "Upgrade", "Upgrade": "websocket"}, true},
		{"case insensitive", map[string]string{"Connection": "upgrade", "Upgrade": "WebSocket"}, true},
		{"missing upgrade header", map[string]string{"Connection": "Upgrade"}, false},
		{"missing connection header", map[string]string{"Upgrade": "websocket"}, false},
		{"wrong upgrade value", map[string]string{"Connection": "Upgrade", "Upgrade": "h2c"}, false},
		{"empty headers", map[string]string{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/", nil)
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			if got := IsWebSocketUpgrade(req); got != tt.expected {
				t.Errorf("IsWebSocketUpgrade() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestWebSocketProxyEcho(t *testing.T) {
	// Start a WS echo backend
	backend := wsEchoServer(t)
	defer backend.Close()

	// Build a minimal gateway config pointing at the echo backend
	cfg := &config.GatewayConfig{
		Global: config.GlobalSettings{
			DefaultTimeout: 30 * time.Second,
		},
		Services: []config.Service{
			{
				Name: "echo",
				URL:  backend.URL,
				Routes: []config.Route{
					{
						Path:      "/ws",
						Methods:   []string{"GET"},
						MatchType: "prefix",
					},
				},
			},
		},
	}

	transport := CreateTransport(cfg.Global.ConnectionPool)
	router := NewRouter(cfg)

	match, err := router.Match("GET", "/ws")
	if err != nil {
		t.Fatalf("route match failed: %v", err)
	}

	handler := NewProxyHandler(match, cfg, transport)

	// Start an HTTP server in front of the proxy handler
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if IsWebSocketUpgrade(r) {
			handler.ServeWebSocket(w, r)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer proxy.Close()

	// Connect to the proxy via raw TCP and perform a WS handshake
	conn, err := net.DialTimeout("tcp", strings.TrimPrefix(proxy.URL, "http://"), 5*time.Second)
	if err != nil {
		t.Fatalf("dial proxy: %v", err)
	}
	defer conn.Close()

	// Send upgrade request
	wsKey := base64.StdEncoding.EncodeToString([]byte("test-key-1234567"))
	handshake := fmt.Sprintf("GET /ws HTTP/1.1\r\nHost: localhost\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", wsKey)
	if _, err := conn.Write([]byte(handshake)); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	// Read the 101 response
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatalf("read upgrade response: %v", err)
	}
	if resp.StatusCode != 101 {
		t.Fatalf("expected 101, got %d", resp.StatusCode)
	}

	// Send a masked text frame with "hello"
	msg := []byte("hello")
	if _, err := conn.Write(buildMaskedFrame(msg)); err != nil {
		t.Fatalf("write frame: %v", err)
	}

	// Read the echoed frame back
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	echo, err := readWSFrame(reader)
	if err != nil {
		t.Fatalf("read echo frame: %v", err)
	}

	if string(echo) != "hello" {
		t.Errorf("echo = %q, want %q", echo, "hello")
	}
}
