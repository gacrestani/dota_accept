# Dota Remote Accept

Accept Dota 2 matches for your friends, remotely. A friend queues, walks away
from their PC, and when the match pops anyone in the party can open a web page
and press **ACCEPT** for them — the app presses Enter in the Dota 2 window on
their machine.

```
 you (phone/browser)          relay (your server)         friend's PC
┌────────────────────┐  POST  ┌───────────────────┐  WS  ┌──────────────────┐
│  web dashboard     │ ─────► │  routes commands  │ ───► │  agent.exe       │
│  [ ACCEPT: Bob ]   │        │  by pairing code  │      │  focuses Dota 2, │
└────────────────────┘        └───────────────────┘      │  presses Enter   │
                                                         └──────────────────┘
```

Two binaries, one repo:

| Piece | Runs on | What it does |
|---|---|---|
| `relay` | your server | Serves the web dashboard, relays accept commands to agents over WebSocket |
| `dota-accept-agent.exe` | friend's Windows PC | Shows a permanent pairing code, waits for commands, presses Enter in Dota 2 |

The agent connects *outward* to the relay, so friends never touch their router
or firewall. Each PC gets a **permanent code** on first run (stored in
`%AppData%\DotaAccept\config.json`), so the dashboard can keep a saved list of
friends.

This is plain OS-level input injection (like AutoHotkey) — it never reads or
touches game memory.

## Try it locally

```sh
./build.sh          # builds bin/relay and bin/dota-accept-agent.exe
./bin/relay         # serves http://localhost:8080
```

Open http://localhost:8080, then on a Windows machine run the agent and add
its code in the dashboard. (On Linux you can dry-run the agent with
`DOTA_ACCEPT_FAKE=1 go run ./cmd/agent` — it fakes the keypress so you can see
the whole round trip.)

## Deploy the relay

The relay is one static binary / tiny Docker image. GitHub Pages can't host it
(it needs WebSockets), but any container host can:

1. Push this repo to GitHub.
2. Create a free web service on [Render](https://render.com) or
   [Koyeb](https://koyeb.com) pointed at the repo — both detect the
   `Dockerfile` automatically. Any VPS with `docker run -p 8080:8080` works too.
3. Point a subdomain of your domain at it (CNAME) and make sure it has HTTPS —
   the host usually does this for you. Your relay is now
   `https://dota.yourdomain.com` (WebSocket: `wss://dota.yourdomain.com`).

Free tiers that sleep when idle are fine in practice: a connected agent keeps
the service awake, and an agent that starts while it sleeps wakes it and
reconnects on its own within a minute.

## Build the agent for your friends

```sh
RELAY_URL=wss://dota.yourdomain.com ./build.sh
```

Send friends `bin/dota-accept-agent.exe`. Or automate it: the included GitHub
Actions workflow (`.github/workflows/release.yml`) builds the exe and attaches
it to a GitHub release whenever you push a tag like `v1.0.0` — set the
repository variable `RELAY_URL` first, then friends download from your
Releases page.

> Windows SmartScreen may warn about the unsigned exe on first run —
> "More info" → "Run anyway".

### Friend setup (the whole thing)

1. Run `dota-accept-agent.exe`. A console window shows the code, e.g. `H8JBRZ`.
2. Tell the code to whoever should be able to accept. Once.
3. Keep the window open while queueing.

### Dashboard

Add each friend's name + code once — it's saved in the browser. Or share a
self-adding link: `https://dota.yourdomain.com/?code=H8JBRZ&name=Bob`.
The green dot shows whether their agent is connected. When the match pops,
hit **ACCEPT**.

## How the keypress works

The agent finds the window titled `Dota 2` (and verifies it belongs to
`dota2.exe` before sending anything), brings it to the foreground, and injects
Enter via `SendInput` at scan-code level — that reaches the game even in
fullscreen. Dota's match-ready dialog treats Enter as Accept. If a setup ever
disagrees, the fallback plan is a mouse click at the dialog's ACCEPT button —
open an issue.

## Security model

- The pairing code is the only credential: anyone who has it can trigger a
  keypress on that PC. Codes are 6 chars from a 30-char alphabet (~700M
  combinations), accepts are rate-limited per code (1 per 2 s), and the agent
  **only ever presses Enter in the Dota 2 window** — if Dota isn't running,
  a command does nothing. Worst case for a leaked code: someone can press
  Enter in your Dota client.
- Run the relay behind HTTPS (`wss://`) so codes aren't visible on the wire.
- The relay keeps no database; it only knows which codes are connected right
  now.

## Development notes

- Go toolchain lives at `~/.local/go` (installed userspace for Silverblue).
- Layout: `cmd/relay` (server + embedded `web/` dashboard), `cmd/agent`
  (Windows input in `input_windows.go`, non-Windows stub in `input_stub.go`),
  `internal/protocol` (shared message types).
- The agent ↔ relay protocol is 2 JSON message types over one WebSocket:
  `accept` (relay→agent) and `result` (agent→relay), correlated by `id`.
