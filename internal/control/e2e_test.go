package control_test

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"nimbus/internal/agent"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func freeTCP(t *testing.T) string {
	t.Helper()
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := l.Addr().String()
	l.Close()
	return a
}
func freeUDP(t *testing.T) string {
	t.Helper()
	l, e := net.ListenPacket("udp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	a := l.LocalAddr().String()
	l.Close()
	return a
}
func await(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	until := time.Now().Add(d)
	for time.Now().Before(until) {
		if f() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("condition did not become true")
}
func startRealAgent(t *testing.T, f *fixture, binaryPath string) func() {
	t.Helper()
	f.s.AckTimeout = 4 * time.Second
	listen := freeUDP(t)
	c := agent.Config{ControlURL: f.internal.URL, ID: "gateway-1", Token: f.gatewayToken, Hysteria: binaryPath, TLSCert: filepath.Join(f.certDir, "gateway.crt"), TLSKey: filepath.Join(f.certDir, "gateway.key"), Listen: listen, AuthListen: freeTCP(t), StatsListen: freeTCP(t), AllowPrivate: true}
	_, gatewayPort, _ := net.SplitHostPort(listen)
	if _, e := f.s.DB.Exec(context.Background(), `UPDATE endpoints SET port=$1 WHERE id='gateway-1'`, gatewayPort); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var runErr error
	expectedTokenRotation := false
	go func() { runErr = agent.Run(ctx, c); close(done) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
			if runErr != nil && !(expectedTokenRotation && strings.Contains(runErr.Error(), "authentication rejected")) {
				t.Errorf("agent: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("agent did not stop")
		}
	})
	await(t, 10*time.Second, func() bool {
		var ready bool
		f.s.DB.QueryRow(context.Background(), `SELECT ready FROM endpoints WHERE id='gateway-1'`).Scan(&ready)
		return ready
	})
	return func() {
		expectedTokenRotation = true
		select {
		case <-done:
			if runErr == nil || !strings.Contains(runErr.Error(), "authentication rejected") {
				t.Fatalf("agent did not stop on token rotation: %v", runErr)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("agent did not stop after authentication rejection")
		}
	}
}
func tunnel(t *testing.T, f *fixture, binaryPath string, plan map[string]any) (net.Conn, <-chan struct{}) {
	t.Helper()
	target, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	targetClosed := make(chan struct{})
	go func() {
		for {
			conn, e := target.Accept()
			if e != nil {
				return
			}
			go func() {
				defer close(targetClosed)
				defer conn.Close()
				tick := time.NewTicker(100 * time.Millisecond)
				defer tick.Stop()
				for {
					select {
					case <-ctx.Done():
						return
					case <-tick.C:
						conn.SetWriteDeadline(time.Now().Add(time.Second))
						if _, writeErr := conn.Write([]byte("ping")); writeErr != nil {
							return
						}
					}
				}
			}()
		}
	}()
	candidates := plan["candidates"].([]any)
	candidate := candidates[0].(map[string]any)
	socks := freeTCP(t)
	cfg := map[string]any{"server": net.JoinHostPort("127.0.0.1", jsonNumber(candidate["port"])), "auth": candidate["credential"], "tls": map[string]any{"sni": "localhost", "ca": filepath.Join(f.certDir, "ca.crt")}, "socks5": map[string]any{"listen": socks}}
	bytes, _ := json.Marshal(cfg)
	path := filepath.Join(t.TempDir(), "client.json")
	os.WriteFile(path, bytes, 0600)
	child := exec.CommandContext(ctx, binaryPath, "client", "--config", path, "--log-level", "error")
	logPath := filepath.Join(t.TempDir(), "client.log")
	logfile, _ := os.Create(logPath)
	child.Stderr = logfile
	child.Stdout = logfile
	t.Cleanup(func() {
		logfile.Close()
		if t.Failed() {
			b, _ := os.ReadFile(logPath)
			t.Log(strings.ReplaceAll(string(b), candidate["credential"].(string), "<redacted>"))
		}
	})
	if e = child.Start(); e != nil {
		t.Fatal(e)
	}
	stop := func() { cancel(); target.Close(); _ = child.Wait() }
	t.Cleanup(stop)
	var connection net.Conn
	await(t, 5*time.Second, func() bool { connection, e = net.DialTimeout("tcp", socks, 100*time.Millisecond); return e == nil })
	connection.SetDeadline(time.Now().Add(5 * time.Second))
	if _, e = connection.Write([]byte{5, 1, 0}); e != nil {
		t.Fatal(e)
	}
	response := make([]byte, 2)
	if _, e = io.ReadFull(connection, response); e != nil || response[1] != 0 {
		t.Fatalf("SOCKS greeting: %v", e)
	}
	targetPort := target.Addr().(*net.TCPAddr).Port
	request := []byte{5, 1, 0, 1, 127, 0, 0, 1, 0, 0}
	binary.BigEndian.PutUint16(request[8:], uint16(targetPort))
	connection.Write(request)
	response = make([]byte, 10)
	if _, e = io.ReadFull(connection, response); e != nil || response[1] != 0 {
		t.Fatalf("SOCKS connect: %v reply=%v", e, response)
	}
	response = make([]byte, 4)
	if _, e = io.ReadFull(connection, response); e != nil || string(response) != "ping" {
		t.Fatalf("encrypted tunnel failed: %v", e)
	}
	connection.SetDeadline(time.Time{})
	t.Cleanup(func() { connection.Close() })
	return connection, targetClosed
}
func jsonNumber(v any) string { b, _ := json.Marshal(v); return string(b) }
func assertDisconnected(t *testing.T, c net.Conn, within time.Duration) {
	t.Helper()
	c.SetReadDeadline(time.Now().Add(within))
	buf := make([]byte, 4)
	for {
		_, e := io.ReadFull(c, buf)
		if e != nil {
			if n, ok := e.(net.Error); ok && n.Timeout() {
				t.Fatal("existing VPN stream survived revocation/expiry")
			}
			return
		}
	}
}
func realBinary(t *testing.T) string {
	t.Helper()
	p := os.Getenv("TEST_HYSTERIA_BIN")
	if p == "" {
		t.Skip("set TEST_HYSTERIA_BIN for real encrypted gateway tests")
	}
	return p
}
func TestRealGatewayDeviceRevocation(t *testing.T) {
	binaryPath := realBinary(t)
	f := newFixture(t, false)
	startRealAgent(t, f, binaryPath)
	a := f.account(1, 1)
	plan := f.must("POST", "/v1/connection-sessions", planInput(), a, 200)
	conn, _ := tunnel(t, f, binaryPath, plan)
	f.must("DELETE", "/v1/devices/"+a.device, nil, a, 204)
	assertDisconnected(t, conn, 6*time.Second)
}
func TestRealGatewayExpiryWhileControlOffline(t *testing.T) {
	binaryPath := realBinary(t)
	f := newFixture(t, false)
	f.s.LeaseTTL = 12 * time.Second
	startRealAgent(t, f, binaryPath)
	a := f.account(1, 1)
	plan := f.must("POST", "/v1/connection-sessions", planInput(), a, 200)
	conn, _ := tunnel(t, f, binaryPath, plan)
	f.internal.Close()
	assertDisconnected(t, conn, 15*time.Second)
}

func TestRealGatewayTokenRotationStopsExistingTraffic(t *testing.T) {
	binaryPath := realBinary(t)
	f := newFixture(t, false)
	expectStop := startRealAgent(t, f, binaryPath)
	a := f.account(1, 1)
	plan := f.must("POST", "/v1/connection-sessions", planInput(), a, 200)
	_, targetClosed := tunnel(t, f, binaryPath, plan)
	f.must("PATCH", "/admin/endpoints/gateway-1", map[string]any{"auth_token": "rotated-gateway-token-that-is-long-enough"}, nil, 204)
	expectStop()
	select {
	case <-targetClosed:
	case <-time.After(5 * time.Second):
		t.Fatal("gateway egress stream survived token rotation")
	}
}
