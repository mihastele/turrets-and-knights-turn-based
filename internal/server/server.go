// Package server runs the HTTP API, the lobby and the authoritative match
// loop. Layout:
//
//   - HTTP serves the client (static files) and room create/join endpoints.
//   - WebSocket carries intentions (client -> server) and snapshots
//     (server -> client).
//   - Each room owns its *sim.Sim; all access to sim state happens while
//     holding the room's mutex, which gives the simulation a single owner.
package server

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"lanesiege/internal/coupons"
	"lanesiege/internal/sim"
)

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

type player struct {
	token         string
	seat          int
	conn          *wsConn
	activeCoupon  string
	withoutCoupon bool
	playerID      string   // client-generated id from localStorage, used for coupon redemption records
	coupons       []string // coupon codes this player holds (validated server-side, rechecked live)
}

type wsConn struct {
	ws   *websocket.Conn
	send chan []byte
}

type room struct {
	code           string
	private        bool
	winMode        sim.WinMode
	lives          int
	breachesToWin  int
	maxBreachLoots int
	hideLuck       bool
	players        [2]*player
	game           *sim.Sim
	state          string // "waiting" | "playing" | "finished"
	rematch        [2]bool
	created        time.Time
	snapEvery      int          // tick counter for snapshot decimation
	lastEmote      [2]time.Time // per-seat emote rate limit
}

// Server owns all rooms.
type Server struct {
	balance sim.Balance

	mu         sync.Mutex
	rooms      map[string]*room
	coupons    *coupons.Store
	adminToken string

	httpServer *http.Server
	stop       chan struct{}
	stopOnce   sync.Once
	wg         sync.WaitGroup
}

// Options carries optional dependencies for NewWithOptions.
type Options struct {
	// Coupons is the coupon store; nil means an in-memory store (data is
	// lost on restart). main wires a file-backed store for production.
	Coupons *coupons.Store
	// AdminToken guards the /api/admin/* endpoints. Empty disables them.
	AdminToken string
}

// New builds a Server with default options (in-memory coupons, admin API off).
func New(webFS fs.FS) *Server { return NewWithOptions(webFS, Options{}) }

// NewWithOptions builds a Server with explicit coupon store and admin token.
// webFS must contain the client (index.html, style.css, client.js) at its
// root; pass os.DirFS(dir) in development or the embedded filesystem for a
// self-contained binary. It is served at "/".
func NewWithOptions(webFS fs.FS, opts Options) *Server {
	store := opts.Coupons
	if store == nil {
		store = coupons.New()
	}
	s := &Server{
		balance:    sim.DefaultBalance(),
		rooms:      map[string]*room{},
		coupons:    store,
		adminToken: opts.AdminToken,
		stop:       make(chan struct{}),
	}
	if err := store.PurgeExpired(); err != nil {
		log.Printf("purging expired coupons: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/rooms", s.handleCreateRoom)
	mux.HandleFunc("GET /api/rooms", s.handleListRooms)
	mux.HandleFunc("POST /api/rooms/{code}/join", s.handleJoinRoom)
	mux.HandleFunc("POST /api/rooms/{code}/rejoin", s.handleRejoin)
	mux.HandleFunc("POST /api/rooms/{code}/leave", s.handleLeave)
	mux.HandleFunc("POST /api/coupons/redeem", s.handleRedeemCoupon)
	mux.HandleFunc("POST /api/coupons/status", s.handleCouponStatus)
	mux.HandleFunc("POST /api/admin/coupons", s.handleAdminCreateCoupon)
	mux.HandleFunc("GET /api/admin/coupons", s.handleAdminListCoupons)
	mux.HandleFunc("POST /api/admin/coupons/{code}/revoke", s.handleAdminRevokeCoupon)
	mux.HandleFunc("GET /ws", s.handleWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	s.httpServer = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.wg.Add(1)
	go s.tickLoop()
	s.wg.Add(1)
	go s.janitor()
	return s
}

// Handler exposes the root handler (used by ListenAndServe).
func (s *Server) Handler() http.Handler { return s.httpServer.Handler }

// Serve serves on an existing listener (used by tests and main).
func (s *Server) Serve(l net.Listener) error {
	return s.httpServer.Serve(l)
}

// ListenAndServe serves until the server is closed.
func (s *Server) ListenAndServe(addr string) error {
	s.httpServer.Addr = addr
	return s.httpServer.ListenAndServe()
}

// Close stops background goroutines and hijacked sockets, then shuts the
// HTTP listener down. Safe to call multiple times.
func (s *Server) Close() {
	s.stopOnce.Do(func() { close(s.stop) })
	// Drop every live match connection so its readPump/writePump exits.
	s.mu.Lock()
	for _, rm := range s.rooms {
		for _, p := range rm.players {
			if p != nil && p.conn != nil {
				_ = p.conn.ws.Close()
			}
		}
	}
	s.mu.Unlock()
	_ = s.httpServer.Close()
	s.wg.Wait()
}

// ---------------------------------------------------------------------------
// Room lifecycle (HTTP)
// ---------------------------------------------------------------------------

const roomCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

func randomCode() (string, error) {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	for i := range buf {
		buf[i] = roomCodeAlphabet[int(buf[i])%len(roomCodeAlphabet)]
	}
	return string(buf), nil
}

func randomToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", buf), nil
}

func randomSeed() int64 {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return int64(binary.LittleEndian.Uint64(buf[:]))
}

// lobbyIdentity carries the client-generated player id and any coupon codes
// the client holds. Both are optional; missing values just mean no entitlement.
type lobbyIdentity struct {
	private        bool
	publicOnly     bool
	playerID       string
	coupons        []string
	winMode        sim.WinMode
	lives          int
	breachesToWin  int
	maxBreachLoots int
	hideLuck       bool
	invalidErr     string
}

// parseLobbyIdentity reads the optional identity body sent with create/join/
// rejoin requests. Coupon codes are validated against the store right away;
// only currently-valid codes are stored on the player.
func (s *Server) parseLobbyIdentity(r *http.Request) lobbyIdentity {
	id := lobbyIdentity{
		winMode:        sim.WinModeLives,
		lives:          1,
		breachesToWin:  3,
		maxBreachLoots: 5,
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		return id
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return id
	}
	var raw struct {
		Private        bool     `json:"private"`
		PublicOnly     bool     `json:"publicOnly"`
		PlayerID       string   `json:"playerId"`
		Coupons        []string `json:"coupons"`
		WinMode        *string  `json:"winMode"`
		Lives          *int     `json:"lives"`
		BreachesToWin  *int     `json:"breachesToWin"`
		MaxBreachLoots *int     `json:"maxBreachLoots"`
		HideLuck       *bool    `json:"hideLuck"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		id.invalidErr = "invalid request body"
		return id
	}
	id.playerID = normalizePlayerID(raw.PlayerID)
	id.private = raw.Private
	id.publicOnly = raw.PublicOnly
	if raw.WinMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*raw.WinMode))
		switch sim.WinMode(mode) {
		case sim.WinModeLives, sim.WinModeBreaches:
			id.winMode = sim.WinMode(mode)
		default:
			id.invalidErr = "winMode must be 'lives' or 'breaches'"
			return id
		}
	}
	if raw.Lives != nil {
		id.lives = *raw.Lives
		if id.lives < 1 || id.lives > 10 {
			id.invalidErr = "lives must be between 1 and 10"
			return id
		}
	}
	if raw.BreachesToWin != nil {
		id.breachesToWin = *raw.BreachesToWin
		if id.breachesToWin < 1 || id.breachesToWin > sim.NumLanes {
			id.invalidErr = fmt.Sprintf("breachesToWin must be between 1 and %d", sim.NumLanes)
			return id
		}
	}
	if raw.MaxBreachLoots != nil {
		id.maxBreachLoots = *raw.MaxBreachLoots
		if id.maxBreachLoots < 1 || id.maxBreachLoots > 15 {
			id.invalidErr = "maxBreachLoots must be between 1 and 15"
			return id
		}
	}
	if raw.HideLuck != nil {
		id.hideLuck = *raw.HideLuck
	}
	id.coupons = s.entitledCoupons(raw.Coupons)
	return id
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func (s *Server) handleCreateRoom(w http.ResponseWriter, r *http.Request) {
	identity := s.parseLobbyIdentity(r)
	if identity.invalidErr != "" {
		writeErr(w, http.StatusBadRequest, identity.invalidErr)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var code string
	for i := 0; i < 20; i++ {
		c, err := randomCode()
		if err == nil {
			if _, exists := s.rooms[c]; !exists {
				code = c
				break
			}
		}
	}
	if code == "" {
		writeErr(w, http.StatusInternalServerError, "could not allocate room code")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not allocate token")
		return
	}
	s.rooms[code] = &room{
		code:           code,
		private:        identity.private,
		winMode:        identity.winMode,
		lives:          identity.lives,
		breachesToWin:  identity.breachesToWin,
		maxBreachLoots: identity.maxBreachLoots,
		hideLuck:       identity.hideLuck,
		players:        [2]*player{{token: token, seat: 0, playerID: identity.playerID, coupons: identity.coupons}, nil},
		game:           sim.New(randomSeed(), s.balance),
		state:          "waiting",
		created:        time.Now(),
	}
	s.rooms[code].game.SetWinMode(identity.winMode, identity.breachesToWin)
	s.rooms[code].game.SetLives(identity.lives)
	s.rooms[code].game.SetMaxBreachLoots(identity.maxBreachLoots)
	s.rooms[code].game.SetHideLuck(identity.hideLuck)
	log.Printf("room %s created", code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code": code, "token": token, "seat": 0, "side": "A",
		"entitled": len(identity.coupons) > 0,
	})
}

func (s *Server) handleListRooms(w http.ResponseWriter, r *http.Request) {
	type listing struct {
		Code           string      `json:"code"`
		WinMode        sim.WinMode `json:"winMode"`
		Lives          int         `json:"lives"`
		BreachesToWin  int         `json:"breachesToWin"`
		MaxBreachLoots int         `json:"maxBreachLoots"`
		HideLuck       bool        `json:"hideLuck"`
		created        time.Time
	}
	rooms := []listing{}
	s.mu.Lock()
	for _, rm := range s.rooms {
		if !rm.private && rm.state == "waiting" && rm.players[0] != nil && rm.players[0].conn != nil && rm.players[1] == nil {
			rooms = append(rooms, listing{rm.code, rm.winMode, rm.lives, rm.breachesToWin, rm.maxBreachLoots, rm.hideLuck, rm.created})
		}
	}
	s.mu.Unlock()
	sort.Slice(rooms, func(i, j int) bool { return rooms[i].created.Before(rooms[j].created) })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, rooms)
}

func (s *Server) handleJoinRoom(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	identity := s.parseLobbyIdentity(r)
	s.mu.Lock()
	defer s.mu.Unlock()

	rm, ok := s.rooms[code]
	if !ok {
		writeErr(w, http.StatusNotFound, "room not found")
		return
	}
	if identity.publicOnly && (rm.private || rm.state != "waiting" || rm.players[0] == nil || rm.players[0].conn == nil) {
		writeErr(w, http.StatusConflict, "room is no longer available")
		return
	}
	if rm.players[1] != nil {
		writeErr(w, http.StatusConflict, "room is full")
		return
	}
	token, err := randomToken()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not allocate token")
		return
	}
	rm.players[1] = &player{token: token, seat: 1, playerID: identity.playerID, coupons: identity.coupons}
	log.Printf("room %s joined by player 2", code)
	writeJSON(w, http.StatusOK, map[string]any{
		"code": code, "token": token, "seat": 1, "side": "B",
		"entitled": len(identity.coupons) > 0,
	})
}

// handleRejoin lets a disconnected/refreshed client reclaim its seat with
// its token. Used by the browser on page load. The body may also carry an
// updated playerId/coupons list (e.g. the player redeemed a coupon while
// away), which is applied before the hello payload is built.
func (s *Server) handleRejoin(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	var body struct {
		Token    string   `json:"token"`
		PlayerID string   `json:"playerId"`
		Coupons  []string `json:"coupons"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm, ok := s.rooms[code]
	if !ok {
		writeErr(w, http.StatusNotFound, "room not found")
		return
	}
	for _, p := range rm.players {
		if p != nil && p.token == body.Token {
			if id := normalizePlayerID(body.PlayerID); id != "" {
				p.playerID = id
			}
			if body.Coupons != nil {
				p.coupons = s.entitledCoupons(body.Coupons)
			}
			writeJSON(w, http.StatusOK, map[string]any{
				"code": code, "token": p.token, "seat": p.seat,
				"side":     map[int]string{0: "A", 1: "B"}[p.seat],
				"entitled": s.playerEntitled(p),
			})
			return
		}
	}
	writeErr(w, http.StatusForbidden, "bad token")
}

func (s *Server) handleLeave(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !readJSONBody(w, r, &body) {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rm := s.rooms[strings.ToUpper(strings.TrimSpace(r.PathValue("code")))]
	if rm == nil {
		writeErr(w, http.StatusNotFound, "room not found")
		return
	}
	for _, p := range rm.players {
		if p != nil && p.token == body.Token {
			if rm.state == "playing" {
				rm.game.Resign(p.seat)
				rm.state = "finished"
				s.releaseCouponsLocked(rm)
				s.broadcastSnapshotLocked(rm)
			}
			if rm.state == "waiting" {
				delete(s.rooms, rm.code)
			}
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
			return
		}
	}
	writeErr(w, http.StatusForbidden, "bad token")
}
