package server

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"testing/fstest"
)

func TestPublicLobbyAvailability(t *testing.T) {
	s := New(fstest.MapFS{})
	defer s.Close()
	s.rooms["OPEN"] = &room{code: "OPEN", state: "waiting", players: [2]*player{{conn: &wsConn{}}, nil}, lives: 3}
	s.rooms["FULL"] = &room{code: "FULL", state: "waiting", players: [2]*player{{conn: &wsConn{}}, {}}}
	s.rooms["HIDE"] = &room{code: "HIDE", state: "waiting", private: true, players: [2]*player{{conn: &wsConn{}}, nil}}
	s.rooms["GONE"] = &room{code: "GONE", state: "waiting", players: [2]*player{{}, nil}}
	response := httptest.NewRecorder()
	s.Handler().ServeHTTP(response, httptest.NewRequest("GET", "/api/rooms", nil))
	var rooms []struct {
		Code  string `json:"code"`
		Lives int    `json:"lives"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &rooms); err != nil {
		t.Fatal(err)
	}
	if len(rooms) != 1 || rooms[0].Code != "OPEN" || rooms[0].Lives != 3 {
		t.Fatalf("unexpected rooms: %+v", rooms)
	}
	// Synthetic connections have no socket for Close to shut down.
	for _, rm := range s.rooms {
		for _, p := range rm.players {
			if p != nil {
				p.conn = nil
			}
		}
	}
}
