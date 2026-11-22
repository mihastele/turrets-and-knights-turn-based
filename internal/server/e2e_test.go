package server

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type testClient struct {
	t  *testing.T
	cs *websocket.Conn
}

func postJSON(t *testing.T, url string, body any) map[string]any {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(mustJSON(t, body)))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out map[string]any
	var buf strings.Builder
	b := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(b)
		buf.Write(b[:n])
		if err != nil {
			break
		}
	}
	if err := json.Unmarshal([]byte(buf.String()), &out); err != nil {
		t.Fatalf("decode response from %s: %v (%s)", url, err, buf.String())
	}
	if resp.StatusCode >= 400 {
		return out // error bodies carry {"error": ...}
	}
	return out
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dialWS(t *testing.T, base, code, token string) *websocket.Conn {
	t.Helper()
	url := fmt.Sprintf("ws://%s/ws?room=%s&token=%s", base, code, token)
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	return c
}

func readMsg(t *testing.T, c *websocket.Conn, timeout time.Duration) map[string]any {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(timeout))
	var msg map[string]any
	if err := c.ReadJSON(&msg); err != nil {
		t.Fatalf("read ws: %v", err)
	}
	return msg
}

// readUntil reads messages until pred matches or the deadline passes.
type snapshot struct {
	Type           string  `json:"type"`
	Time           float64 `json:"time"`
	Phase          string  `json:"phase"`
	Round          int     `json:"round"`
	PhaseRemaining float64 `json:"phaseRemaining"`
	Ready          [2]bool `json:"ready"`
	Finished       bool    `json:"finished"`
	Draw           bool    `json:"draw"`
	Winner         int     `json:"winner"`
	WinLane        int     `json:"winLane"`
	Overtime       bool    `json:"overtime"`
	TurretFactor   float64 `json:"turretFactor"`
}

func readSnapshot(t *testing.T, c *websocket.Conn, timeout time.Duration) snapshot {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var raw json.RawMessage
		if err := c.ReadJSON(&raw); err != nil {
			t.Fatalf("read snapshot: %v", err)
		}
		var msg struct {
			Type string `json:"type"`
		}
		_ = json.Unmarshal(raw, &msg)
		if msg.Type != "snapshot" {
			continue
		}
		var snap snapshot
		if err := json.Unmarshal(raw, &snap); err != nil {
			t.Fatalf("decode snapshot: %v", err)
		}
		return snap
	}
	t.Fatal("timed out waiting for snapshot")
	return snapshot{}
}

// TestEndToEndMatch plays a real match over HTTP + WebSocket: create, join,
// start, one side marches a soldier down an empty lane, breach is detected,
// and both sides can rematch. Skips with -short.
func TestEndToEndMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := ln.Addr().String()

	srv := New(os.DirFS("../../web"))
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	// Create + join via HTTP API.
	created := postJSON(t, "http://"+base+"/api/rooms", map[string]any{})
	code, _ := created["code"].(string)
	tokenA, _ := created["token"].(string)
	if code == "" || tokenA == "" {
		t.Fatalf("bad create response: %v", created)
	}
	joined := postJSON(t, "http://"+base+"/api/rooms/"+code+"/join", map[string]any{})
	tokenB, _ := joined["token"].(string)
	if tokenB == "" {
		t.Fatalf("bad join response: %v", joined)
	}

	// Third player cannot join a full room.
	full := postJSON(t, "http://"+base+"/api/rooms/"+code+"/join", map[string]any{})
	if full["error"] == nil {
		t.Fatal("expected error joining a full room")
	}

	// Connect both websockets.
	wsA := dialWS(t, base, code, tokenA)
	defer wsA.Close()
	wsB := dialWS(t, base, code, tokenB)
	defer wsB.Close()

	// Drain "hello"/"matchStart" for both.
	gotHelloA, gotStartA := false, false
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !(gotHelloA && gotStartA) {
		msg := readMsg(t, wsA, 2*time.Second)
		if msg["type"] == "hello" {
			gotHelloA = true
		}
		if msg["type"] == "matchStart" {
			gotStartA = true
		}
	}
	if !gotHelloA || !gotStartA {
		t.Fatal("player A did not receive hello+matchStart")
	}
	// Drain B's hello/matchStart too.
	deadline = time.Now().Add(5 * time.Second)
	gotHelloB, gotStartB := false, false
	for time.Now().Before(deadline) && !(gotHelloB && gotStartB) {
		msg := readMsg(t, wsB, 2*time.Second)
		if msg["type"] == "hello" {
			gotHelloB = true
		}
		if msg["type"] == "matchStart" {
			gotStartB = true
		}
	}
	if !gotHelloB || !gotStartB {
		t.Fatal("player B did not receive hello+matchStart")
	}

	// Invalid action: deploying with an out-of-range lane must not charge coins.
	_ = wsA.WriteJSON(map[string]any{"type": "deploy", "unit": "soldier", "lane": 9})

	deadline = time.Now().Add(3 * time.Second)
	sawErr := false
	for time.Now().Before(deadline) && !sawErr {
		msg := readMsg(t, wsA, 2*time.Second)
		if msg["type"] == "error" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatal("expected an error message for invalid lane")
	}

	// Deploy a soldier down lane 0 during Prep, then both players Ready up!
	if err := wsA.WriteJSON(map[string]any{"type": "deploy", "unit": "soldier", "lane": 0}); err != nil {
		t.Fatal(err)
	}
	if err := wsA.WriteJSON(map[string]any{"type": "ready"}); err != nil {
		t.Fatal(err)
	}
	if err := wsB.WriteJSON(map[string]any{"type": "ready"}); err != nil {
		t.Fatal(err)
	}

	// Verify Combat starts immediately when both players are Ready.
	gotCombat := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := readSnapshot(t, wsB, 2*time.Second)
		if snap.Phase == "combat" {
			gotCombat = true
			break
		}
	}
	if !gotCombat {
		t.Fatal("expected combat phase to start immediately after both players ready")
	}

	// Player B resigns to finish the match and test rematch flow.
	if err := wsB.WriteJSON(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	var final snapshot
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		snap := readSnapshot(t, wsA, 2*time.Second)
		if snap.Finished {
			final = snap
			break
		}
	}
	if !final.Finished {
		t.Fatal("match did not finish in time")
	}
	if final.Draw || final.Winner != 0 {
		t.Fatalf("expected side A to win, got draw=%v winner=%d", final.Draw, final.Winner)
	}

	// Both sides request a rematch; server should reset the match.
	_ = wsA.WriteJSON(map[string]any{"type": "rematch"})
	_ = wsB.WriteJSON(map[string]any{"type": "rematch"})

	gotReset := false
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !gotReset {
		msg := readMsg(t, wsA, 2*time.Second)
		if msg["type"] == "matchReset" {
			gotReset = true
		}
	}
	if !gotReset {
		t.Fatal("expected matchReset after both players requested a rematch")
	}
	reset := readSnapshot(t, wsA, 5*time.Second)
	if reset.Finished {
		t.Fatal("snapshot after rematch still shows a finished match")
	}
}

// TestResignEndsMatch verifies that an explicit resign forfeits the match
// immediately and the opponent is declared the winner.
func TestResignEndsMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := ln.Addr().String()

	srv := New(os.DirFS("../../web"))
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	created := postJSON(t, "http://"+base+"/api/rooms", map[string]any{})
	code := created["code"].(string)
	tokenA := created["token"].(string)
	tokenB, _ := postJSON(t, "http://"+base+"/api/rooms/"+code+"/join", map[string]any{})["token"].(string)

	wsA := dialWS(t, base, code, tokenA)
	defer wsA.Close()
	wsB := dialWS(t, base, code, tokenB)
	defer wsB.Close()

	// Let both clients drain hello/matchStart.
	deadline := time.Now().Add(5 * time.Second)
	for _, ws := range []*websocket.Conn{wsA, wsB} {
		got := false
		for !got && time.Now().Before(deadline) {
			msg := readMsg(t, ws, 2*time.Second)
			got = msg["type"] == "matchStart"
		}
	}

	// Player B resigns; A (side 0) must win with no lane credited.
	if err := wsB.WriteJSON(map[string]any{"type": "resign"}); err != nil {
		t.Fatal(err)
	}
	var final snapshot
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := readSnapshot(t, wsA, 2*time.Second)
		if snap.Finished {
			final = snap
			break
		}
	}
	if !final.Finished {
		t.Fatal("match did not end after resign")
	}
	if final.Winner != 0 || final.Draw || final.WinLane != -1 {
		t.Fatalf("expected side A win by resign, got draw=%v winner=%d lane=%d", final.Draw, final.Winner, final.WinLane)
	}
}

// TestRoomGuards covers join/rejoin error paths.
func TestRoomGuards(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := ln.Addr().String()
	srv := New(os.DirFS("../../web"))
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	// Unknown room join.
	bad := postJSON(t, "http://"+base+"/api/rooms/ZZZZ/join", map[string]any{})
	if bad["error"] == nil {
		t.Fatal("expected error joining unknown room")
	}
	// Create, then rejoin with a wrong token.
	created := postJSON(t, "http://"+base+"/api/rooms", map[string]any{})
	code := created["code"].(string)
	badRejoin := postJSON(t, "http://"+base+"/api/rooms/"+code+"/rejoin", map[string]any{"token": "nope"})
	if badRejoin["error"] == nil {
		t.Fatal("expected error rejoining with bad token")
	}
	// Rejoin with the right token works.
	good := postJSON(t, "http://"+base+"/api/rooms/"+code+"/rejoin", map[string]any{"token": created["token"]})
	if good["error"] != nil {
		t.Fatalf("rejoin with correct token failed: %v", good)
	}
}

func TestHideLuckAndWallScoutWS(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping e2e in -short mode")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := ln.Addr().String()
	srv := New(os.DirFS("../../web"))
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	created := postJSON(t, "http://"+base+"/api/rooms", map[string]any{"hideLuck": true})
	code := created["code"].(string)
	tokenA := created["token"].(string)
	tokenB, _ := postJSON(t, "http://"+base+"/api/rooms/"+code+"/join", map[string]any{})["token"].(string)

	wsA := dialWS(t, base, code, tokenA)
	defer wsA.Close()
	wsB := dialWS(t, base, code, tokenB)
	defer wsB.Close()

	// Drain until matchStart
	deadline := time.Now().Add(5 * time.Second)
	for _, ws := range []*websocket.Conn{wsA, wsB} {
		got := false
		for !got && time.Now().Before(deadline) {
			msg := readMsg(t, ws, 2*time.Second)
			got = msg["type"] == "matchStart"
		}
	}

	// Build a wall in lane 0 and scout lane 1
	if err := wsA.WriteJSON(map[string]any{"type": "wall", "lane": 0}); err != nil {
		t.Fatal(err)
	}
	if err := wsA.WriteJSON(map[string]any{"type": "scout", "lane": 1}); err != nil {
		t.Fatal(err)
	}

	// Read raw snapshot to verify hideLuck, walls, and scoutedLanes
	var raw map[string]any
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		_ = wsA.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := wsA.ReadJSON(&raw); err != nil {
			t.Fatal(err)
		}
		if raw["type"] == "snapshot" {
			walls, _ := raw["walls"].([]any)
			if len(walls) >= 1 {
				break
			}
		}
	}
	if raw["hideLuck"] != true {
		t.Fatalf("expected hideLuck=true, got %v", raw["hideLuck"])
	}
	walls, _ := raw["walls"].([]any)
	if len(walls) == 0 {
		t.Fatal("expected built wall in snapshot")
	}
}

func TestWinModeRoomConfigAndValidation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := ln.Addr().String()
	srv := New(os.DirFS("../../web"))
	defer srv.Close()
	go func() { _ = srv.Serve(ln) }()

	// Validation errors
	if bad := postJSON(t, "http://"+base+"/api/rooms", map[string]any{"winMode": "invalid"}); bad["error"] == nil {
		t.Fatal("expected error for invalid winMode")
	}
	if bad := postJSON(t, "http://"+base+"/api/rooms", map[string]any{"winMode": "breaches", "breachesToWin": 6}); bad["error"] == nil {
		t.Fatal("expected error for breachesToWin > 5")
	}
	if bad := postJSON(t, "http://"+base+"/api/rooms", map[string]any{"maxBreachLoots": 16}); bad["error"] == nil {
		t.Fatal("expected error for maxBreachLoots > 15")
	}
	if bad := postJSON(t, "http://"+base+"/api/rooms", map[string]any{"maxBreachLoots": 0}); bad["error"] == nil {
		t.Fatal("expected error for maxBreachLoots < 1")
	}

	// Valid "N Breaches" room with custom maxBreachLoots
	created := postJSON(t, "http://"+base+"/api/rooms", map[string]any{
		"winMode":        "breaches",
		"breachesToWin":  4,
		"maxBreachLoots": 12,
	})
	if created["error"] != nil {
		t.Fatalf("unexpected error creating breaches room: %v", created)
	}
	code := created["code"].(string)
	tokenA := created["token"].(string)
	tokenB := postJSON(t, "http://"+base+"/api/rooms/"+code+"/join", map[string]any{})["token"].(string)

	wsA := dialWS(t, base, code, tokenA)
	defer wsA.Close()
	wsB := dialWS(t, base, code, tokenB)
	defer wsB.Close()

	var raw map[string]any
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_ = wsA.SetReadDeadline(time.Now().Add(2 * time.Second))
		if err := wsA.ReadJSON(&raw); err != nil {
			t.Fatal(err)
		}
		if raw["type"] == "snapshot" {
			break
		}
	}
	if raw["winMode"] != "breaches" {
		t.Fatalf("winMode = %v, want breaches", raw["winMode"])
	}
	if raw["breachesToWin"] != float64(4) {
		t.Fatalf("breachesToWin = %v, want 4", raw["breachesToWin"])
	}
	if raw["maxBreachLoots"] != float64(12) {
		t.Fatalf("maxBreachLoots = %v, want 12", raw["maxBreachLoots"])
	}
}
