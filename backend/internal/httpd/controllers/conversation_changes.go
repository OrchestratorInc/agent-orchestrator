package controllers

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"sort"
	"strconv"
	"time"

	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

// conversationChangeOverlap re-sends rows written shortly before the cursor's
// read. A writer stamps updated_at before its transaction commits, so a row can
// become visible after a read that started later than its timestamp. Missing a
// row is still safe: the page digest no longer matches and the client reads the
// full page.
const conversationChangeOverlap = 2 * time.Second

// conversationChangeCursor is the opaque changedSince token. It names the newest
// page it was cut from and the facts that make a row-level answer valid.
type conversationChangeCursor struct {
	Version        int    `json:"v"`
	ConversationID string `json:"c"`
	BranchID       string `json:"b,omitempty"`
	// EditFloorSequence decides every message's editAvailable flag without
	// touching the message row, so a change to it needs the full page.
	EditFloorSequence int64 `json:"e,omitempty"`
	LatestSequence    int64 `json:"s"`
	// ReadAt is the daemon clock before the page was read, in Unix nanoseconds.
	ReadAt int64 `json:"t"`
}

func encodeConversationChangeCursor(s chatsvc.Snapshot, readAt time.Time) string {
	raw, _ := json.Marshal(conversationChangeCursor{
		Version:           1,
		ConversationID:    s.Conversation.ID,
		BranchID:          s.Conversation.ActiveBranchID,
		EditFloorSequence: s.EditFloorSequence,
		LatestSequence:    s.Conversation.LatestSequence,
		ReadAt:            readAt.UnixNano(),
	})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func decodeConversationChangeCursor(token string) (conversationChangeCursor, error) {
	var cursor conversationChangeCursor
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err == nil {
		err = json.Unmarshal(raw, &cursor)
	}
	if err != nil || cursor.Version != 1 || cursor.ConversationID == "" {
		return conversationChangeCursor{}, fmt.Errorf("changedSince must be a changeCursor from this endpoint")
	}
	return cursor, nil
}

// conversationChangesOnly cuts a newest-page response down to the rows changed
// since cursor. It reports false, leaving the response whole, when the cursor
// was cut from another conversation, branch or edit floor.
func conversationChangesOnly(out *ConversationSnapshotResponse, s chatsvc.Snapshot, cursor conversationChangeCursor) bool {
	if cursor.ConversationID != s.Conversation.ID ||
		cursor.BranchID != s.Conversation.ActiveBranchID ||
		cursor.EditFloorSequence != s.EditFloorSequence {
		return false
	}
	out.PageDigest = conversationPageDigest(out)
	since := cursor.ReadAt - conversationChangeOverlap.Nanoseconds()
	changed := func(sequence int64, updatedAt time.Time) bool {
		return sequence > cursor.LatestSequence || updatedAt.UnixNano() >= since
	}
	messages := out.Messages[:0]
	for i, message := range out.Messages {
		if changed(s.Messages[i].Sequence, s.Messages[i].UpdatedAt) {
			messages = append(messages, message)
		}
	}
	out.Messages = messages
	activities := out.Activities[:0]
	for i, activity := range out.Activities {
		if changed(s.Activities[i].Sequence, s.Activities[i].UpdatedAt) {
			activities = append(activities, activity)
		}
	}
	out.Activities = activities
	out.ChangesOnly = true
	return true
}

func conversationPageDigest(out *ConversationSnapshotResponse) string {
	type row struct {
		sequence int64
		key      string
	}
	rows := make([]row, 0, len(out.Messages)+len(out.Activities))
	for _, message := range out.Messages {
		rows = append(rows, row{message.Sequence, "message:" + message.ID + ":" + strconv.FormatInt(message.Revision, 10)})
	}
	for _, activity := range out.Activities {
		rows = append(rows, row{activity.Sequence, "activity:" + activity.ID + ":" + strconv.FormatInt(activity.Revision, 10)})
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sequence < rows[j].sequence })
	hash := fnv.New32a()
	for _, r := range rows {
		_, _ = hash.Write([]byte(r.key + "\n"))
	}
	return fmt.Sprintf("%08x", hash.Sum32())
}
