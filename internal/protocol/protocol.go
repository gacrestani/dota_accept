// Package protocol defines the messages exchanged between the relay and
// desktop agents over the agent WebSocket.
package protocol

const (
	TypeAccept = "accept" // relay -> agent: press accept now
	TypeResult = "result" // agent -> relay: outcome of an accept command
)

type Message struct {
	Type   string `json:"type"`
	ID     string `json:"id,omitempty"` // correlates a result with the accept that caused it
	OK     bool   `json:"ok,omitempty"`
	Detail string `json:"detail,omitempty"`
}
