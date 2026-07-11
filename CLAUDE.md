# Dota Remote Accept — project context

Remote match-accept for Dota 2: a `relay` server routes ACCEPT presses from a
web dashboard to Windows `agent`s by permanent pairing code; the agent focuses
the Dota 2 window and injects Enter (scan-code SendInput). See README.md for
architecture and deploy docs.

## Build / run

- `./build.sh` builds `bin/relay` (linux) and `bin/dota-accept-agent.exe`
  (windows cross-compile). `RELAY_URL=wss://... ./build.sh` bakes the agent's
  default relay URL. Needs only a Go toolchain (`GO` env overrides the path,
  default `~/.local/go/bin/go`).
- Local e2e test without Windows: `./bin/relay`, then
  `DOTA_ACCEPT_FAKE=1 go run ./cmd/agent` (fakes the keypress), then exercise
  `POST /api/accept/<code>` or the dashboard at http://localhost:8080.

## Current state (2026-07-10)

- Code complete and smoke-tested end-to-end on Linux (status, accept
  round-trip, rate limit, offline handling).
- **Deployed.** Relay runs on the homeserver `tartarus` (Fedora Atomic,
  x86_64) as a systemd *user* service `dota-relay.service`
  (`~/.config/systemd/user/`, binary copied to `~/.local/bin/dota-relay`,
  `PORT=8844` because 8080 is taken by a podman container; lingering is on).
  Public via Tailscale Funnel: **https://tartarus.taild545f.ts.net** →
  localhost:8844. Verified from the public internet including a WSS agent
  round-trip. Update flow: rebuild on laptop (Projects/ is syncthing-synced
  to tartarus), then `cp ~/Projects/dota_accept/bin/relay
  ~/.local/bin/dota-relay && systemctl --user restart dota-relay` on tartarus.
- Production agent exe: `RELAY_URL=wss://tartarus.taild545f.ts.net ./build.sh`.
- Optional (not done): set GitHub repo variable `RELAY_URL` and push a tag
  `v*` so `.github/workflows/release.yml` publishes the exe on a release.

## Untested / known risks

- Never run on a real Windows PC yet. Two things need live validation:
  (1) Dota's match-ready dialog treating Enter as Accept, and
  (2) focus-stealing (Alt-tap + SetForegroundWindow in
  `cmd/agent/input_windows.go`) working while the user is AFK.
  Agreed fallback if Enter fails: mouse click at the ACCEPT button position.

## Conventions

- Plain JS/HTML dashboard, no framework, single embedded file
  (`cmd/relay/web/index.html`).
- Only dependency is `gorilla/websocket`; keep it that way unless there's a
  strong reason.
- The agent must never send input to anything but the verified Dota 2 window —
  that containment is the security model.
