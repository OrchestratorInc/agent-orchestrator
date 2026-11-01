// Package hub reads Cline's native hub ownership facts. It does not select an
// AO session: callers must independently prove the exact supervised process and
// launch before applying a callback.
package hub

import (
	"encoding/json"
	"time"
)

// MaxEvents bounds a correlation attempt even when the hub has a long history.
// Older or incomplete histories deliberately provide no ownership evidence.
const MaxEvents = 4096

// Event is the supported v1 stream event envelope. Payloads are decoded only
// for registration and session creation; transcripts and other event data are
// never part of an ownership binding.
type Event struct {
	Version   string          `json:"version"`
	Name      string          `json:"event"`
	Sequence  int64           `json:"sequence"`
	SessionID string          `json:"sessionId"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload"`
}

// Binding names the client process originally registered when the native root
// was created/restored. Current client.list metadata is mutable and must not
// replace these historical facts. A Binding alone is not AO ownership proof.
type Binding struct {
	PID                  int
	ClientID             string
	RegisteredAt         time.Time
	CreatedAt            time.Time
	RegistrationSequence int64
	CreationSequence     int64
}

// Correlate extracts a root's creator from a contiguous, bounded replay through
// a previously observed hub.status boundary. Missing/pruned events, unsupported
// versions, replacement registrations, and callbacks predating the most recent
// creation provide no binding. since is the exclusive stream replay cursor.
//
// This trusts the provider's original registration facts, as other native hook
// protocols do. It does not authenticate an arbitrary client's claimed PID.
func Correlate(events []Event, since, through int64, root string, hookAt time.Time) (Binding, bool) {
	if since < 0 || through <= since || through-since > MaxEvents ||
		len(events) == 0 || len(events) > MaxEvents || root == "" || len(root) > 256 || hookAt.IsZero() {
		return Binding{}, false
	}
	last := since
	creationIndex := -1
	var creator string
	for index, event := range events {
		if event.Version != "v1" || event.Sequence != last+1 || event.Sequence > through || event.Timestamp <= 0 {
			return Binding{}, false
		}
		last = event.Sequence
		if event.Name != "session.created" {
			continue
		}
		var payload struct {
			Session struct {
				ID      string `json:"sessionId"`
				Creator string `json:"createdByClientId"`
			} `json:"session"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			return Binding{}, false
		}
		if payload.Session.ID != root {
			continue
		}
		if event.SessionID != root || payload.Session.Creator == "" || len(payload.Session.Creator) > 256 {
			return Binding{}, false
		}
		creationIndex, creator = index, payload.Session.Creator
	}
	if last != through || creationIndex < 0 {
		return Binding{}, false
	}
	creation := events[creationIndex]
	// Equality is ambiguous at the native protocol's millisecond resolution.
	// Never relabel a delayed callback with a newer creation's owner.
	if hookAt.UnixMilli() <= creation.Timestamp {
		return Binding{}, false
	}
	var binding Binding
	for index, event := range events {
		if event.Name != "hub.client.registered" && event.Name != "hub.client.disconnected" {
			continue
		}
		var payload struct {
			ClientID    string `json:"clientId"`
			ConnectedAt int64  `json:"connectedAt"`
			Metadata    struct {
				PID int `json:"pid"`
			} `json:"metadata"`
		}
		if json.Unmarshal(event.Payload, &payload) != nil {
			return Binding{}, false
		}
		if payload.ClientID != creator {
			continue
		}
		if index > creationIndex {
			if event.Name == "hub.client.registered" {
				// Reusing a live client id can relabel old session records. A new
				// process needs its own session.created event before attribution.
				return Binding{}, false
			}
			// Disconnect does not transfer the root to a participant. The
			// caller must still prove the original process/launch is alive.
			continue
		}
		if event.Name == "hub.client.disconnected" {
			binding = Binding{}
			continue
		}
		if payload.Metadata.PID <= 1 || payload.ConnectedAt <= 0 ||
			payload.ConnectedAt > creation.Timestamp || event.Timestamp > creation.Timestamp {
			return Binding{}, false
		}
		binding = Binding{
			PID: payload.Metadata.PID, ClientID: creator,
			RegisteredAt: time.UnixMilli(payload.ConnectedAt), CreatedAt: time.UnixMilli(creation.Timestamp),
			RegistrationSequence: event.Sequence, CreationSequence: creation.Sequence,
		}
	}
	return binding, binding.PID > 1
}
