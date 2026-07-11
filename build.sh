#!/usr/bin/env bash
# Builds both binaries into bin/.
#
#   ./build.sh                                   # agent defaults to ws://localhost:8080
#   RELAY_URL=wss://dota.example.com ./build.sh  # bake your relay URL into the agent exe
set -euo pipefail
cd "$(dirname "$0")"

GO="${GO:-$HOME/.local/go/bin/go}"
RELAY_URL="${RELAY_URL:-ws://localhost:8080}"

echo "building relay (linux) ..."
"$GO" build -ldflags "-s -w" -o bin/relay ./cmd/relay

echo "building agent (windows, default relay: $RELAY_URL) ..."
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 "$GO" build \
  -ldflags "-s -w -X main.defaultRelay=$RELAY_URL" \
  -o bin/dota-accept-agent.exe ./cmd/agent

echo "done:"
ls -la bin/
