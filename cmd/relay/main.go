// Command relay is the rendezvous server for dota_accept. It serves the web
// dashboard and forwards "accept" commands from dashboard visitors to desktop
// agents connected over WebSocket, keyed by their pairing code.
package main

import (
	"cmp"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gacrestani/dota_accept/internal/protocol"
)

//go:embed web
var webFS embed.FS

const (
	readTimeout    = 75 * time.Second // must exceed pingInterval
	pingInterval   = 30 * time.Second
	acceptTimeout  = 10 * time.Second
	minAcceptGap   = 2 * time.Second // per-code rate limit between accepts
	maxStatusCodes = 50
)

var codeRe = regexp.MustCompile(`^[A-Z0-9]{4,12}$`)

var upgrader = websocket.Upgrader{
	// Agents are not browsers; no origin to check.
	CheckOrigin: func(*http.Request) bool { return true },
}

// agentConn is one connected desktop agent.
type agentConn struct {
	code string
	conn *websocket.Conn

	writeMu sync.Mutex // serializes data-frame writes

	mu      sync.Mutex
	pending map[string]chan protocol.Message // accept id -> waiting HTTP handler
	closed  chan struct{}
}

func (a *agentConn) sendJSON(m protocol.Message) error {
	a.writeMu.Lock()
	defer a.writeMu.Unlock()
	a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	return a.conn.WriteJSON(m)
}

func (a *agentConn) await(id string) chan protocol.Message {
	ch := make(chan protocol.Message, 1)
	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	return ch
}

func (a *agentConn) forget(id string) {
	a.mu.Lock()
	delete(a.pending, id)
	a.mu.Unlock()
}

func (a *agentConn) resolve(m protocol.Message) {
	a.mu.Lock()
	ch := a.pending[m.ID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- m:
		default:
		}
	}
}

type hub struct {
	mu         sync.Mutex
	agents     map[string]*agentConn
	lastAccept map[string]time.Time
}

func (h *hub) register(a *agentConn) {
	h.mu.Lock()
	old := h.agents[a.code]
	h.agents[a.code] = a
	h.mu.Unlock()
	if old != nil {
		// Same code reconnected (e.g. after a network blip): drop the old one.
		old.conn.Close()
	}
}

func (h *hub) unregister(a *agentConn) {
	h.mu.Lock()
	if h.agents[a.code] == a {
		delete(h.agents, a.code)
	}
	h.mu.Unlock()
}

func (h *hub) get(code string) *agentConn {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.agents[code]
}

// tooSoon reports whether an accept for code arrived within minAcceptGap of
// the previous one, and records the attempt otherwise.
func (h *hub) tooSoon(code string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if time.Since(h.lastAccept[code]) < minAcceptGap {
		return true
	}
	h.lastAccept[code] = time.Now()
	return false
}

func (h *hub) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.URL.Query().Get("code")))
	if !codeRe.MatchString(code) {
		http.Error(w, "invalid code: expected 4-12 letters/digits", http.StatusBadRequest)
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a := &agentConn{
		code:    code,
		conn:    conn,
		pending: map[string]chan protocol.Message{},
		closed:  make(chan struct{}),
	}
	h.register(a)
	log.Printf("agent %s: connected (%s)", code, r.RemoteAddr)
	defer func() {
		close(a.closed)
		h.unregister(a)
		conn.Close()
		log.Printf("agent %s: disconnected", code)
	}()

	go func() {
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-a.closed:
				return
			case <-t.C:
				// WriteControl is safe to call concurrently with WriteJSON.
				if err := conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)); err != nil {
					return
				}
			}
		}
	}()

	conn.SetReadDeadline(time.Now().Add(readTimeout))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(readTimeout))
	})
	for {
		var m protocol.Message
		if err := conn.ReadJSON(&m); err != nil {
			return
		}
		conn.SetReadDeadline(time.Now().Add(readTimeout))
		if m.Type == protocol.TypeResult && m.ID != "" {
			a.resolve(m)
		}
	}
}

type acceptResponse struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func (h *hub) handleAccept(w http.ResponseWriter, r *http.Request) {
	code := strings.ToUpper(strings.TrimSpace(r.PathValue("code")))
	if !codeRe.MatchString(code) {
		writeJSON(w, acceptResponse{false, "invalid code"})
		return
	}
	a := h.get(code)
	if a == nil {
		writeJSON(w, acceptResponse{false, "agent is offline"})
		return
	}
	if h.tooSoon(code) {
		writeJSON(w, acceptResponse{false, "wait a couple of seconds between presses"})
		return
	}

	id := newID()
	ch := a.await(id)
	defer a.forget(id)
	start := time.Now()
	if err := a.sendJSON(protocol.Message{Type: protocol.TypeAccept, ID: id}); err != nil {
		writeJSON(w, acceptResponse{false, "agent connection lost"})
		return
	}
	select {
	case res := <-ch:
		log.Printf("agent %s: accept ok=%v (%s) in %s", code, res.OK, res.Detail, time.Since(start).Round(time.Millisecond))
		writeJSON(w, acceptResponse{res.OK, res.Detail})
	case <-time.After(acceptTimeout):
		log.Printf("agent %s: accept timed out", code)
		writeJSON(w, acceptResponse{false, "agent did not respond in time"})
	}
}

func (h *hub) handleStatus(w http.ResponseWriter, r *http.Request) {
	out := map[string]bool{}
	for i, c := range strings.Split(r.URL.Query().Get("codes"), ",") {
		if i >= maxStatusCodes {
			break
		}
		c = strings.ToUpper(strings.TrimSpace(c))
		if !codeRe.MatchString(c) {
			continue
		}
		out[c] = h.get(c) != nil
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Allows hosting the dashboard on another origin (e.g. GitHub Pages);
	// the pairing code itself is the authorization.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	json.NewEncoder(w).Encode(v)
}

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func main() {
	h := &hub{agents: map[string]*agentConn{}, lastAccept: map[string]time.Time{}}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /ws/agent", h.handleAgentWS)
	mux.HandleFunc("POST /api/accept/{code}", h.handleAccept)
	mux.HandleFunc("GET /api/status", h.handleStatus)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok")) })

	web, err := fs.Sub(webFS, "web")
	if err != nil {
		log.Fatal(err)
	}
	mux.Handle("GET /", http.FileServerFS(web))

	addr := ":" + cmp.Or(os.Getenv("PORT"), "8080")
	log.Printf("relay listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
