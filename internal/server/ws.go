package server

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"lanesiege/internal/coupons"
	"lanesiege/internal/sim"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 4096,
	CheckOrigin:     func(r *http.Request) bool { return true }, // local game
}

// clientMsg is the envelope for everything a client may send.
type clientMsg struct {
	Type   string `json:"type"`
	Lane   int    `json:"lane"`
	Unit   string `json:"unit"`
	Track  string `json:"track"` // upgrade track: damage, attackSpeed, hp, shield, special
	Emote  string `json:"emote"`
	Effect string `json:"effect"` // coupon field effect id for "fieldEffect"
	Slot   *int   `json:"slot"`   // turret slot for upgradeTurret (omitted = lowest built slot); ignored for turret builds (auto)
}

const writeWait = 5 * time.Second
const pongWait = 60 * time.Second
const pingPeriod = 30 * time.Second

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("room")))
	token := r.URL.Query().Get("token")

	s.mu.Lock()
	rm, ok := s.rooms[code]
	var pl *player
	if ok {
		for _, p := range rm.players {
			if p != nil && p.token == token {
				pl = p
			}
		}
	}
	s.mu.Unlock()
	if !ok || pl == nil {
		http.Error(w, "unknown room or token", http.StatusForbidden)
		return
	}

	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("ws upgrade: %v", err)
		return
	}
	conn := &wsConn{ws: ws, send: make(chan []byte, 64)}

	s.mu.Lock()
	old := pl.conn
	pl.conn = conn
	bothConnected := rm.players[0] != nil && rm.players[1] != nil &&
		rm.players[0].conn != nil && rm.players[1].conn != nil
	justStarted := false
	_ = bothConnected
	s.mu.Unlock()

	if old != nil {
		// A refreshed tab replaces the stale connection. Closing the websocket
		// makes the old readPump exit; its defer closes old.send exactly once.
		_ = old.ws.Close()
	}
	// Notify the other player (if any) that we're here.
	s.mu.Lock()
	for _, p := range rm.players {
		if p != nil && p != pl && p.conn != nil {
			s.sendToLocked(p.conn, map[string]any{"type": "opponentConnected"})
		}
	}
	s.mu.Unlock()

	go s.writePump(conn)
	go s.readPump(r, code, token, pl, conn)

	// Tell this client its identity + the balance table (single source of truth).
	s.sendTo(conn, map[string]any{
		"type":    "hello",
		"seat":    pl.seat,
		"code":    code,
		"balance": s.balance,
		"coupon":  s.couponHello(pl),
	})
	if justStarted {
		log.Printf("room %s match started", code)
		s.broadcast(rm, map[string]any{"type": "matchStart"})
		s.broadcastSnapshot(rm)
	} else {
		// Reconnecting mid-match (or waiting): send the current state.
		s.mu.Lock()
		state := rm.state
		s.mu.Unlock()
		if state != "waiting" {
			s.sendTo(conn, map[string]any{"type": "matchStart"})
			s.broadcastSnapshot(rm)
		}
	}
}

func (s *Server) readPump(r *http.Request, code, token string, pl *player, conn *wsConn) {
	defer func() {
		s.mu.Lock()
		if cur, ok := s.rooms[code]; ok {
			for _, p := range cur.players {
				if p != nil && p.conn == conn {
					p.conn = nil
				}
			}
			s.sendRawLockedExcept(cur, conn, map[string]any{
				"type": "opponentDisconnected", "seat": pl.seat,
			})
		}
		s.mu.Unlock()
		close(conn.send)
		_ = conn.ws.Close()
	}()

	conn.ws.SetReadLimit(4096)
	_ = conn.ws.SetReadDeadline(time.Now().Add(pongWait))
	conn.ws.SetPongHandler(func(string) error {
		return conn.ws.SetReadDeadline(time.Now().Add(pongWait))
	})

	for {
		_, data, err := conn.ws.ReadMessage()
		if err != nil {
			return
		}
		var msg clientMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			s.sendTo(conn, map[string]any{"type": "error", "message": "bad message"})
			continue
		}
		s.handleClientMsg(code, pl, conn, msg)
	}
}

func (s *Server) handleClientMsg(code string, pl *player, conn *wsConn, msg clientMsg) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.rooms[code]
	if !ok {
		return
	}

	if msg.Type == "withoutCoupon" {
		if rm.state == "waiting" {
			pl.withoutCoupon = true
			pl.coupons = nil
		}
		return
	}

	if msg.Type == "rematch" {
		if rm.state != "finished" {
			return
		}
		rm.rematch[pl.seat] = true
		s.broadcastLocked(rm, map[string]any{"type": "rematchStatus", "seat": pl.seat})
		if rm.rematch[0] && rm.rematch[1] {
			rm.state = "waiting"
			rm.players[0].withoutCoupon = false
			rm.players[1].withoutCoupon = false
		}

		return
	}

	if msg.Type == "emote" {
		// Quick canned reactions — a bit of table talk without a chat system.
		allowed := map[string]bool{"nice": true, "gg": true, "lol": true, "ouch": true, "wow": true}
		if !allowed[msg.Emote] || (rm.state != "playing" && rm.state != "finished") {
			return // unknown emote or match not on; silently drop
		}
		if time.Since(rm.lastEmote[pl.seat]) < 1500*time.Millisecond {
			return // rate limited
		}
		rm.lastEmote[pl.seat] = time.Now()
		s.broadcastLocked(rm, map[string]any{"type": "emote", "seat": pl.seat, "emote": msg.Emote})
		return
	}

	if msg.Type == "resign" {
		if rm.state != "playing" {
			return
		}
		rm.game.Resign(pl.seat)
		rm.state = "finished"
		s.releaseCouponsLocked(rm)
		log.Printf("room %s: side %d resigned", code, pl.seat)
		s.broadcastSnapshotLocked(rm)
		return
	}

	if rm.state != "playing" {
		s.sendToLocked(conn, map[string]any{"type": "error", "message": "match is not running"})
		return
	}

	var action sim.Action
	action.Side = pl.seat
	action.Slot = -1                       // default: lowest built slot (upgrade) / auto (build)
	action.Entitled = s.playerEntitled(pl) // checked here (not at hello time) so expiry/revocation applies immediately
	if msg.Slot != nil {
		action.Slot = *msg.Slot
	}
	switch msg.Type {
	case "ready":
		action.Kind = sim.ActionReady
	case "recallUnit":
		action.Kind = sim.ActionRecallUnit
		action.Lane = msg.Lane
		action.Unit = sim.UnitType(msg.Unit)
	case "deploy":
		action.Kind = sim.ActionDeploy
		action.Lane = msg.Lane
		action.Unit = sim.UnitType(msg.Unit)
	case "turret":
		action.Kind = sim.ActionBuildTurret
		action.Lane = msg.Lane
	case "factory":
		action.Kind = sim.ActionBuildFactory
	case "upgradeFactory":
		action.Kind = sim.ActionUpgradeFactory
	case "upgradeUnit":
		action.Kind = sim.ActionUpgradeUnit
		action.Unit = sim.UnitType(msg.Unit)
		action.Track = sim.UpgradeTrack(msg.Track)
	case "upgradeTurret":
		action.Kind = sim.ActionUpgradeTurret
		action.Lane = msg.Lane
		action.Track = sim.UpgradeTrack(msg.Track)
	case "fieldEffect":
		action.Kind = sim.ActionFieldEffect
		action.Lane = msg.Lane
		action.Effect = strings.ToLower(strings.TrimSpace(msg.Effect))
	default:
		s.sendToLocked(conn, map[string]any{"type": "error", "message": "unknown action"})
		return
	}

	if err := rm.game.ApplyAction(action); err != nil {
		s.sendToLocked(conn, map[string]any{"type": "error", "message": err.Error()})
		return
	}
	// Echo an immediate snapshot so the actor sees the result without
	// waiting for the next 10 Hz broadcast.
	s.broadcastSnapshotLocked(rm)
}

// ---------------------------------------------------------------------------
// Game loop
// ---------------------------------------------------------------------------

func (s *Server) tickLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second / sim.TickRate)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			s.tickAll()
		}
	}
}

func (s *Server) tickAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rm := range s.rooms {
		if rm.state == "waiting" && rm.players[0] != nil && rm.players[1] != nil && rm.players[0].conn != nil && rm.players[1].conn != nil {
			s.tryStartLocked(rm)
		}
		if rm.state != "playing" || rm.game == nil {
			continue
		}
		for _, p := range rm.players {
			if p != nil && p.activeCoupon != "" && !s.playerEntitled(p) {
				p.activeCoupon = ""
				if p.conn != nil {
					s.sendToLocked(p.conn, map[string]any{"type": "couponUpdate", "coupon": s.couponHello(p)})
				}
			}
		}
		rm.game.Step(sim.TickDT)
		rm.snapEvery++
		if rm.game.Finished {
			rm.state = "finished"
			s.releaseCouponsLocked(rm)
			log.Printf("room %s finished (winner=%d draw=%v lane=%d)", rm.code, rm.game.Winner, rm.game.Draw, rm.game.WinLane)
		}
		if rm.snapEvery >= sim.SnapshotEvery || rm.game.Finished {
			rm.snapEvery = 0
			s.broadcastSnapshotLocked(rm)
		}
	}
}

// tryStartLocked assigns at most one coupon to each seat under the server lock.
func (s *Server) tryStartLocked(rm *room) {
	busy := map[string]bool{}
	for _, other := range s.rooms {
		if other.state == "playing" {
			for _, p := range other.players {
				if p != nil && p.activeCoupon != "" {
					busy[p.activeCoupon] = true
				}
			}
		}
	}
	candidates := [2][]string{}
	all := s.coupons.List()
	for seat, p := range rm.players {
		if p.withoutCoupon {
			continue
		}
		for _, c := range all {
			for _, code := range p.coupons {
				if coupons.Normalize(c.Code) == coupons.Normalize(code) && !busy[coupons.Normalize(c.Code)] && s.coupons.Status(code).Valid {
					candidates[seat] = append(candidates[seat], coupons.Normalize(c.Code))
				}
			}
		}
	}
	chosen := [2]string{}
	// Choose a pair that gives both seats access whenever possible.
	for _, a := range candidates[0] {
		for _, b := range candidates[1] {
			if a != b {
				chosen = [2]string{a, b}
				break
			}
		}
		if chosen[1] != "" {
			break
		}
	}
	if chosen[0] == "" && len(candidates[0]) > 0 {
		chosen[0] = candidates[0][0]
	}
	if chosen[1] == "" && len(candidates[1]) > 0 && candidates[1][0] != chosen[0] {
		chosen[1] = candidates[1][0]
	}
	blocked := false
	for i, p := range rm.players {
		busyValid := false
		for _, code := range p.coupons {
			if s.coupons.Status(code).Valid {
				busyValid = true
			}
		}
		if !p.withoutCoupon && busyValid && chosen[i] == "" {
			s.sendToLocked(p.conn, map[string]any{"type": "couponWait", "message": "Coupon is in use or has reached its usage limit."})
			blocked = true
		}
	}
	if blocked {
		return
	}
	codes := []string{}
	for _, code := range chosen {
		if code != "" {
			codes = append(codes, code)
		}
	}
	if err := s.coupons.StartUses(codes); err != nil {
		s.broadcastLocked(rm, map[string]any{"type": "couponWait", "message": "Could not start with coupon. Please wait or play without it."})
		return
	}
	rematch := rm.rematch[0] && rm.rematch[1]
	if rematch {
		rm.game = sim.New(randomSeed(), s.balance)
		rm.game.SetLives(rm.lives)
		rm.snapEvery = 0
		rm.rematch = [2]bool{}
	}
	for i, p := range rm.players {
		p.activeCoupon = chosen[i]
		var exhausted string
		if chosen[i] != "" && s.coupons.Status(chosen[i]).Reason == "exhausted" {
			exhausted = chosen[i]
			remaining := p.coupons[:0]
			for _, code := range p.coupons {
				if coupons.Normalize(code) != exhausted {
					remaining = append(remaining, code)
				}
			}
			p.coupons = remaining
		}
		s.sendToLocked(p.conn, map[string]any{"type": "couponUpdate", "coupon": s.couponHello(p), "exhaustedCode": exhausted})
	}
	rm.state = "playing"
	if rematch {
		s.broadcastLocked(rm, map[string]any{"type": "matchReset"})
	} else {
		s.broadcastLocked(rm, map[string]any{"type": "matchStart"})
	}
	s.broadcastSnapshotLocked(rm)
}

func (s *Server) releaseCouponsLocked(rm *room) {
	for _, p := range rm.players {
		if p != nil {
			p.activeCoupon = ""
		}
	}
}

func (s *Server) janitor() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-ticker.C:
			if err := s.coupons.PurgeExpired(); err != nil {
				log.Printf("purging expired coupons: %v", err)
			}
			s.mu.Lock()
			for code, rm := range s.rooms {
				connected := false
				for _, p := range rm.players {
					if p != nil && p.conn != nil {
						connected = true
					}
				}
				if !connected && time.Since(rm.created) > 15*time.Minute {
					log.Printf("room %s expired", code)
					delete(s.rooms, code)
				}
			}
			s.mu.Unlock()
		}
	}
}

// ---------------------------------------------------------------------------
// Broadcast helpers
// ---------------------------------------------------------------------------

func (s *Server) sendTo(c *wsConn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	select {
	case c.send <- data:
	default: // slow consumer: drop rather than block the match
	}
}

// sendToLocked is sendTo for call sites that already hold s.mu.
func (s *Server) sendToLocked(c *wsConn, v any) { s.sendTo(c, v) }

func (s *Server) broadcastLocked(rm *room, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, p := range rm.players {
		if p != nil && p.conn != nil {
			select {
			case p.conn.send <- data:
			default:
			}
		}
	}
}

func (s *Server) sendRawLockedExcept(rm *room, except *wsConn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	for _, p := range rm.players {
		if p != nil && p.conn != nil && p.conn != except {
			select {
			case p.conn.send <- data:
			default:
			}
		}
	}
}

func (s *Server) broadcast(rm *room, v any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastLocked(rm, v)
}

func (s *Server) broadcastSnapshot(rm *room) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.broadcastSnapshotLocked(rm)
}

func (s *Server) broadcastSnapshotLocked(rm *room) {
	if rm.game == nil {
		return
	}
	pair := rm.game.SnapshotPair()
	for seat, p := range rm.players {
		if p != nil && p.conn != nil {
			s.sendToLocked(p.conn, pair[seat])
		}
	}
}

func (s *Server) writePump(c *wsConn) {
	defer func() { _ = c.ws.Close() }()
	ping := time.NewTicker(pingPeriod)
	defer ping.Stop()
	for {
		select {
		case msg, ok := <-c.send:
			if !ok {
				_ = c.ws.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		case <-ping.C:
			_ = c.ws.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.ws.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
