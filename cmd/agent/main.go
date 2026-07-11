// Command agent runs on the PC that has Dota 2 open. It connects out to the
// relay, identifies itself with a permanent pairing code, and presses Enter
// in the Dota 2 window whenever an "accept" command arrives.
package main

import (
	"crypto/rand"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/gacrestani/dota_accept/internal/protocol"
)

// defaultRelay is baked in at build time:
//
//	go build -ldflags "-X main.defaultRelay=wss://dota.example.com" ./cmd/agent
var defaultRelay = "ws://localhost:8080"

// codeAlphabet skips easily-confused characters (I/L/O/U/0/1).
const codeAlphabet = "ABCDEFGHJKMNPQRSTVWXYZ23456789"

type config struct {
	Code  string `json:"code"`
	Relay string `json:"relay"`
}

func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "DotaAccept", "config.json"), nil
}

func loadOrCreate(relayOverride string) (config, string, error) {
	path, err := configPath()
	if err != nil {
		return config{}, "", err
	}
	var cfg config
	if data, err := os.ReadFile(path); err == nil {
		json.Unmarshal(data, &cfg)
	}
	changed := false
	if cfg.Code == "" {
		cfg.Code = newCode()
		changed = true
	}
	if relayOverride != "" && relayOverride != cfg.Relay {
		cfg.Relay = relayOverride
		changed = true
	}
	if cfg.Relay == "" {
		cfg.Relay = defaultRelay
		changed = true
	}
	if changed {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return cfg, path, err
		}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			return cfg, path, err
		}
	}
	return cfg, path, nil
}

func newCode() string {
	const n = 6
	out := make([]byte, 0, n)
	buf := make([]byte, 1)
	limit := byte(256 - 256%len(codeAlphabet)) // rejection sampling: no modulo bias
	for len(out) < n {
		rand.Read(buf)
		if buf[0] >= limit {
			continue
		}
		out = append(out, codeAlphabet[int(buf[0])%len(codeAlphabet)])
	}
	return string(out)
}

func banner(cfg config, path string) {
	line := strings.Repeat("=", 50)
	fmt.Println(line)
	fmt.Println("  DOTA REMOTE ACCEPT — AGENT")
	fmt.Println()
	fmt.Printf("  Your code:   %s\n", cfg.Code)
	fmt.Println()
	fmt.Println("  Give this code to whoever should be able to")
	fmt.Println("  accept matches for you. Keep this window open")
	fmt.Println("  while you queue.")
	fmt.Println()
	fmt.Printf("  Relay:  %s\n", cfg.Relay)
	fmt.Printf("  Config: %s\n", path)
	fmt.Println(line)
}

// fatal prints the error and waits for Enter so the message is readable when
// the exe was launched by double-click (console would close instantly).
func fatal(msg string) {
	fmt.Fprintln(os.Stderr, "ERROR:", msg)
	fmt.Fprintln(os.Stderr, "Press Enter to exit...")
	fmt.Scanln()
	os.Exit(1)
}

func main() {
	relayFlag := flag.String("relay", "", "relay URL, e.g. wss://dota.example.com (persisted to config)")
	flag.Parse()

	cfg, path, err := loadOrCreate(*relayFlag)
	if err != nil {
		fatal(err.Error())
	}
	banner(cfg, path)

	backoff := 2 * time.Second
	for {
		start := time.Now()
		if err := runSession(cfg); err != nil {
			log.Printf("connection lost: %v", err)
		}
		if time.Since(start) > time.Minute {
			backoff = 2 * time.Second // the last connection was healthy; start over
		}
		log.Printf("reconnecting in %s...", backoff)
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
		}
	}
}

func runSession(cfg config) error {
	url := strings.TrimRight(cfg.Relay, "/") + "/ws/agent?code=" + cfg.Code
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	log.Printf("connected to relay — waiting for accept commands")

	var writeMu sync.Mutex // serializes result writes from accept goroutines

	resetDeadline := func() { conn.SetReadDeadline(time.Now().Add(90 * time.Second)) }
	resetDeadline()
	conn.SetPingHandler(func(data string) error {
		resetDeadline()
		return conn.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(10*time.Second))
	})

	for {
		var m protocol.Message
		if err := conn.ReadJSON(&m); err != nil {
			return err
		}
		resetDeadline()
		if m.Type != protocol.TypeAccept {
			continue
		}
		go func(id string) {
			log.Printf("ACCEPT command received — pressing Enter in Dota 2")
			detail, err := pressAccept()
			res := protocol.Message{Type: protocol.TypeResult, ID: id, OK: err == nil, Detail: detail}
			if err != nil {
				res.Detail = err.Error()
				log.Printf("accept failed: %v", err)
			} else {
				log.Printf("accept done: %s", detail)
			}
			writeMu.Lock()
			conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			conn.WriteJSON(res)
			writeMu.Unlock()
		}(m.ID)
	}
}
