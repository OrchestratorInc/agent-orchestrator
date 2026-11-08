package chat

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrFindCursor fences pagination against a different query or transcript.
var ErrFindCursor = errors.New("search changed; restart from the first page")

// ErrFindUnavailable reports a transport without complete retained history.
var ErrFindUnavailable = errors.New("complete conversation search is unavailable")

const maxFindCacheBytes = 32 << 20

var findMessageBytes = int(reflect.TypeFor[domain.ConversationMessage]().Size())

type cachedFind struct {
	conversation    domain.ConversationRecord
	messages        []domain.ConversationMessage
	durableRevision string
	bytes           int
	at              time.Time
}

// FindOccurrence identifies a literal match in one displayed message block.
type FindOccurrence struct {
	MessageID string `json:"messageId"`
	Revision  int64  `json:"revision"`
	Sequence  int64  `json:"sequence"`
	Block     int    `json:"block"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
}

// FindResult is a bounded occurrence page with an exact transcript-wide count.
type FindResult struct {
	ConversationID     string           `json:"conversationId"`
	TranscriptRevision string           `json:"transcriptRevision"`
	Occurrences        []FindOccurrence `json:"occurrences"`
	Total              int              `json:"total"`
	NextCursor         string           `json:"nextCursor,omitempty"`
	PreviousCursor     string           `json:"previousCursor,omitempty"`
	LastCursor         string           `json:"lastCursor,omitempty"`
	Offset             int              `json:"offset"`
	InitialIndex       int              `json:"initialIndex"`
}

// FindAnchor starts a new search at the reader's visible text position.
type FindAnchor struct {
	MessageID      string
	Block, Offset  int
	Sequence       int64
	PreferPrevious bool
}

type findReader interface {
	ConversationFindSource(context.Context, string) (domain.ConversationRecord, []domain.ConversationMessage, error)
	ConversationFindWindow(context.Context, string, int64) (domain.ConversationFindWindow, error)
}

// FindConversation is a read-only search of the visible durable branch. Its
// revision binds every result page to the same query and transcript contents.
func (s *Service) FindConversation(ctx context.Context, id, query, cursor string, anchors ...FindAnchor) (FindResult, error) {
	if err := ctx.Err(); err != nil {
		return FindResult{}, err
	}
	reader, ok := s.store.(findReader)
	if !ok {
		return FindResult{}, ErrFindUnavailable
	}
	conversation, messages, err := reader.ConversationFindSource(ctx, id)
	if errors.Is(err, domain.ErrNoConversation) {
		side, sideErr := s.findSide(ctx, id)
		if sideErr != nil {
			return FindResult{}, sideErr
		}
		conversation, messages, err = side.Conversation, side.Messages, nil
	}
	if err != nil {
		return FindResult{}, err
	}
	var anchor FindAnchor
	if len(anchors) > 0 {
		anchor = anchors[0]
	}
	anchorSequence := anchor.Sequence
	for _, message := range messages {
		if message.ID == anchor.MessageID {
			anchorSequence = message.Sequence
			break
		}
	}
	// Cursor pages refer to one immutable read snapshot. Streaming can change
	// between requests without stranding navigation; durable edits and branch
	// changes revoke the snapshot. Keep at most 32 MiB in memory, never on disk.
	revision, err := findRevision(conversation, messages, query)
	if err != nil {
		return FindResult{}, err
	}
	// Without a streaming message the two hashes cover the same input.
	durableRevision := revision
	if slices.ContainsFunc(messages, func(message domain.ConversationMessage) bool { return findableMessage(message) && message.Streaming }) {
		durableRevision, err = findRevisionFiltered(conversation, messages, query, true)
		if err != nil {
			return FindResult{}, err
		}
	}
	key := revision
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 {
			return FindResult{}, ErrFindCursor
		}
		key = parts[0]
	}
	s.findMu.Lock()
	for key, entry := range s.findCache {
		if time.Since(entry.at) > 30*time.Second {
			delete(s.findCache, key)
		}
	}
	cached, found := s.findCache[key]
	s.findMu.Unlock()
	if found && cursor != "" {
		if cached.durableRevision != durableRevision {
			return FindResult{}, ErrFindCursor
		}
		return findMessages(ctx, cached.conversation, cached.messages, query, cursor)
	}
	if _, err := findCursorOffset(cursor, revision); err != nil {
		return FindResult{}, err
	}
	result, err := findMessagesAt(ctx, conversation, messages, query, revision, cursor, 200, anchorSequence, anchor)
	if err != nil {
		return FindResult{}, err
	}
	// Only retain facts needed by matching, not delivery payloads/attachments.
	size, count := 0, 0
	for _, message := range messages {
		if findableMessage(message) {
			size += findMessageBytes + len(message.Text) + len(message.ID)
			count++
		}
	}
	if size <= maxFindCacheBytes {
		compact := make([]domain.ConversationMessage, 0, count)
		for _, message := range messages {
			if findableMessage(message) {
				compact = append(compact, domain.ConversationMessage{ID: message.ID, Text: message.Text, Role: message.Role, Origin: message.Origin, Sequence: message.Sequence, Revision: message.Revision, Continuation: message.Continuation, Streaming: message.Streaming})
			}
		}
		s.findMu.Lock()
		if s.findCache == nil {
			s.findCache = make(map[string]cachedFind)
		}
		total := size
		for key, entry := range s.findCache {
			if key != revision {
				total += entry.bytes
			}
		}
		for len(s.findCache) >= 8 || total > maxFindCacheBytes {
			var oldest string
			var at time.Time
			for key, entry := range s.findCache {
				if key != revision && (oldest == "" || entry.at.Before(at)) {
					oldest, at = key, entry.at
				}
			}
			if oldest == "" {
				break
			}
			total -= s.findCache[oldest].bytes
			delete(s.findCache, oldest)
		}
		s.findCache[revision] = cachedFind{conversation: domain.ConversationRecord{ID: conversation.ID, ActiveBranchID: conversation.ActiveBranchID, SessionID: conversation.SessionID, ReviewID: conversation.ReviewID}, messages: compact, durableRevision: durableRevision, bytes: size, at: time.Now()}
		s.findMu.Unlock()
	}
	return result, nil
}

func findMessages(ctx context.Context, conversation domain.ConversationRecord, messages []domain.ConversationMessage, query, cursor string) (FindResult, error) {
	return findMessagesLimit(ctx, conversation, messages, query, cursor, 200, 0, FindAnchor{})
}

func findRevision(conversation domain.ConversationRecord, messages []domain.ConversationMessage, query string) (string, error) {
	return findRevisionFiltered(conversation, messages, query, false)
}

func findRevisionFiltered(conversation domain.ConversationRecord, messages []domain.ConversationMessage, query string, settledOnly bool) (string, error) {
	needle := strings.TrimSpace(foldFindText(query).text)
	hash := sha256.New()
	// JSON supplies unambiguous boundaries; text is hashed, never logged.
	encoder := json.NewEncoder(hash)
	if err := encoder.Encode([]string{conversation.ID, conversation.ActiveBranchID, string(conversation.SessionID), conversation.ReviewID, needle}); err != nil {
		return "", err
	}
	for _, message := range messages {
		if !findableMessage(message) || settledOnly && message.Streaming {
			continue
		}
		value := struct {
			ID, Text                string
			Role                    domain.MessageRole
			Origin                  domain.MessageOrigin
			Sequence, Revision      int64
			Continuation, Streaming bool
		}{message.ID, message.Text, message.Role, message.Origin, message.Sequence, message.Revision, message.Continuation, message.Streaming}
		if err := encoder.Encode(value); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func findAtOrAfter(hit FindOccurrence, sequence int64, anchor FindAnchor) bool {
	return hit.Sequence > sequence || hit.Sequence == sequence && (hit.MessageID != anchor.MessageID || hit.Block > anchor.Block || hit.Block == anchor.Block && hit.Start >= anchor.Offset)
}

func findPageCursors(result *FindResult) {
	result.NextCursor, result.PreviousCursor = "", ""
	if end := result.Offset + len(result.Occurrences); end < result.Total {
		result.NextCursor = fmt.Sprintf("%s:%d", result.TranscriptRevision, end)
	}
	if result.Offset > 0 {
		result.PreviousCursor = fmt.Sprintf("%s:%d", result.TranscriptRevision, max(0, result.Offset-200))
	}
	if result.Total > 0 {
		result.LastCursor = fmt.Sprintf("%s:%d", result.TranscriptRevision, (result.Total-1)/200*200)
	}
}

func findCursorOffset(cursor, revision string) (int, error) {
	offset := 0
	if cursor != "" {
		parts := strings.Split(cursor, ":")
		if len(parts) != 2 || parts[0] != revision {
			return 0, ErrFindCursor
		}
		var err error
		offset, err = strconv.Atoi(parts[1])
		if err != nil || offset < 0 {
			return 0, ErrFindCursor
		}
	}
	return offset, nil
}

func findMessagesLimit(ctx context.Context, conversation domain.ConversationRecord, messages []domain.ConversationMessage, query, cursor string, limit int, anchorSequence int64, anchor FindAnchor) (FindResult, error) {
	revision, err := findRevision(conversation, messages, query)
	if err != nil {
		return FindResult{}, err
	}
	return findMessagesAt(ctx, conversation, messages, query, revision, cursor, limit, anchorSequence, anchor)
}

func findMessagesAt(ctx context.Context, conversation domain.ConversationRecord, messages []domain.ConversationMessage, query, revision, cursor string, limit int, anchorSequence int64, anchor FindAnchor) (FindResult, error) {
	needle := strings.TrimSpace(foldFindText(query).text)
	offset, err := findCursorOffset(cursor, revision)
	if err != nil {
		return FindResult{}, err
	}
	result := FindResult{ConversationID: conversation.ID, TranscriptRevision: revision, Occurrences: []FindOccurrence{}}
	result.Offset, result.InitialIndex = offset, -1
	anchored := cursor == "" && anchor.MessageID != ""
	var recent []FindOccurrence
	if anchored {
		recent = make([]FindOccurrence, limit)
	}
	if needle == "" {
		result.InitialIndex = 0
		return result, nil
	}
	for _, message := range messages {
		if err := ctx.Err(); err != nil {
			return FindResult{}, err
		}
		if !findableMessage(message) {
			continue
		}
		for block, value := range findMessageBlocks(message) {
			folded := foldFindText(value)
			for from := 0; from < len(folded.text); {
				index := strings.Index(folded.text[from:], needle)
				if index < 0 {
					break
				}
				start := from + index
				end := start + len(needle)
				if end < len(folded.text) {
					next, _ := utf8.DecodeRuneInString(folded.text[end:])
					if unicode.IsMark(next) {
						from = end
						continue
					}
				}
				hit := FindOccurrence{MessageID: message.ID, Revision: message.Revision, Sequence: message.Sequence, Block: block, Start: folded.startAt(start), End: folded.endAt(end - 1)}
				if anchored && result.InitialIndex < 0 {
					recent[result.Total%limit] = hit
				}
				if result.InitialIndex < 0 && findAtOrAfter(hit, anchorSequence, anchor) {
					result.InitialIndex = result.Total
					if anchored {
						offset = result.Total / limit * limit
						result.Offset = offset
						result.Occurrences = result.Occurrences[:0]
						for i := offset; i < result.Total; i++ {
							result.Occurrences = append(result.Occurrences, recent[i%limit])
						}
					}
				}
				if result.Total >= offset && len(result.Occurrences) < limit {
					result.Occurrences = append(result.Occurrences, hit)
				}
				result.Total++
				if result.Total%1024 == 0 {
					if err := ctx.Err(); err != nil {
						return FindResult{}, err
					}
				}
				from = end
				// A folded expansion (ß→ss) is one displayed glyph. Do not
				// count the same glyph twice for a one-letter query.
				for from < len(folded.text) && folded.startAt(from) < folded.endAt(end-1) {
					from++
				}
			}
		}
	}
	if offset > result.Total {
		return FindResult{}, ErrFindCursor
	}
	if result.InitialIndex < 0 {
		result.InitialIndex = 0
		if anchor.PreferPrevious && result.Total > 0 {
			result.InitialIndex = result.Total - 1
			if anchored {
				result.Offset = (result.Total - 1) / limit * limit
				result.Occurrences = result.Occurrences[:0]
				for i := result.Offset; i < result.Total; i++ {
					result.Occurrences = append(result.Occurrences, recent[i%limit])
				}
			}
		}
	}
	if cursor != "" {
		result.InitialIndex = offset
	}
	findPageCursors(&result)
	return result, nil
}

func findableMessage(message domain.ConversationMessage) bool {
	return !message.Continuation && message.Origin != domain.MessageOriginAutomation && message.Origin != domain.MessageOriginDaemon && (message.Role == domain.MessageRoleUser || message.Role == domain.MessageRoleAssistant)
}

// FindConversationWindow reveals a result without loading intervening history.
func (s *Service) FindConversationWindow(ctx context.Context, id string, sequence int64) (Snapshot, error) {
	reader, ok := s.store.(findReader)
	if !ok {
		return Snapshot{}, ErrFindUnavailable
	}
	rows, err := reader.ConversationFindWindow(ctx, id, sequence)
	if errors.Is(err, domain.ErrNoConversation) {
		side, sideErr := s.findSide(ctx, id)
		if sideErr != nil {
			return Snapshot{}, sideErr
		}
		// Messages near the target and their turns are sufficient to reveal it;
		// the normal side history remains the authority for live interactions.
		from, to := -1, 0
		for i, message := range side.Messages {
			if message.Sequence == sequence {
				from, to = max(0, i-20), min(len(side.Messages), i+21)
				break
			}
		}
		if from < 0 {
			return Snapshot{}, ErrFindCursor
		}
		side.Messages = side.Messages[from:to]
		visibleTurns := map[string]bool{}
		for _, message := range side.Messages {
			visibleTurns[message.TurnID] = true
		}
		turns := side.Turns[:0]
		for _, turn := range side.Turns {
			if visibleTurns[turn.ID] {
				turns = append(turns, turn)
			}
		}
		side.Turns = turns
		activities := side.Activities[:0]
		for _, activity := range side.Activities {
			if visibleTurns[activity.TurnID] {
				activities = append(activities, activity)
			}
		}
		side.Activities = activities
		return side, nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Conversation: rows.Conversation, SessionID: rows.Conversation.SessionID, Mode: domain.SessionModeChat, ActiveBranch: rows.ActiveBranch, Turns: rows.Turns, Messages: rows.Messages, Activities: rows.Activities, OldestSequence: rows.OldestSequence, HasMoreBefore: rows.HasMoreBefore, BranchPoints: rows.BranchPoints}, nil
}
