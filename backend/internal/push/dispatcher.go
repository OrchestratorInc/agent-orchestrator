package push

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type Subscriber interface {
	Subscribe(projectID domain.ProjectID) (<-chan domain.NotificationEvent, func())
}

type DeviceStore interface {
	List() []mobilebridge.PushDevice
	UnregisterToken(token string) error
}

type Sender interface {
	Send(ctx context.Context, messages []Message) ([]Ticket, error)
	GetReceipts(ctx context.Context, ids []string) (map[string]Receipt, error)
}

const androidChannelID = "default"

const (
	receiptDelay         = 15 * time.Minute
	receiptSweepInterval = 5 * time.Minute
	receiptMaxAge        = time.Hour
	maxPendingReceipts   = 10000
)

type sentTicket struct {
	id             string
	token          string
	sentAt         time.Time
	pruneAmbiguous bool
}

type Dispatcher struct {
	sub     Subscriber
	devices DeviceStore
	sender  Sender
	hostID  string
	log     *slog.Logger
	clock   func() time.Time

	mu      sync.Mutex
	pending []sentTicket
}

func NewDispatcher(sub Subscriber, devices DeviceStore, sender Sender, hostID string, log *slog.Logger) *Dispatcher {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Dispatcher{sub: sub, devices: devices, sender: sender, hostID: hostID, log: log, clock: time.Now}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ch, unsubscribe := d.sub.Subscribe("")
	defer unsubscribe()
	ticker := time.NewTicker(receiptSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-ch:
			if !ok {
				return
			}
			if event.Kind != domain.NotificationCreated {
				continue
			}
			d.dispatch(ctx, event.Record)
		case <-ticker.C:
			d.sweepReceipts(ctx)
		}
	}
}

func (d *Dispatcher) dispatch(ctx context.Context, rec domain.NotificationRecord) {
	if d.hostID == "" {
		return
	}
	devices := d.devices.List()
	if len(devices) == 0 {
		return
	}
	messages := make([]Message, 0, len(devices))
	for _, dev := range devices {
		if dev.Token == "" {
			continue
		}
		messages = append(messages, messageFor(rec, dev.Token, d.hostID, dev.HostName))
	}
	if len(messages) == 0 {
		return
	}
	tickets, err := d.sender.Send(ctx, messages)
	if err != nil {
		d.log.Warn("push send failed", "err", err, "notification", rec.ID, "devices", len(messages))
	}
	now := d.clock()
	var accepted []sentTicket
	for i, t := range tickets {
		if i >= len(messages) {
			break
		}
		token := messages[i].To
		if t.IsDeviceNotRegistered() {
			d.prune(token)
			continue
		}
		if t.Status == "ok" && t.ID != "" {
			accepted = append(accepted, sentTicket{id: t.ID, token: token, sentAt: now})
		}
	}
	d.trackAccepted(accepted)
}

func (d *Dispatcher) prune(token string) {
	if err := d.devices.UnregisterToken(token); err != nil {
		d.log.Warn("prune dead push token failed", "err", err)
	} else {
		d.log.Info("pruned dead push token")
	}
}

func (d *Dispatcher) trackAccepted(tickets []sentTicket) {
	if len(tickets) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = append(d.pending, tickets...)
	tokenOf := make(map[string]string, len(d.pending))
	for _, t := range d.pending {
		token, exists := tokenOf[t.id]
		if !exists {
			tokenOf[t.id] = t.token
		}
		if t.pruneAmbiguous || (exists && token != t.token) {
			tokenOf[t.id] = ""
		}
	}
	for i := range d.pending {
		if tokenOf[d.pending[i].id] == "" {
			d.pending[i].pruneAmbiguous = true
		}
	}
	if over := len(d.pending) - maxPendingReceipts; over > 0 {
		d.pending = d.pending[over:]
	}
}

func (d *Dispatcher) sweepReceipts(ctx context.Context) {
	now := d.clock()

	d.mu.Lock()
	due := make(map[sentTicket]struct{})
	var ids []string
	seen := make(map[string]struct{})
	for _, t := range d.pending {
		age := now.Sub(t.sentAt)
		if t.id == "" || age < receiptDelay || age >= receiptMaxAge {
			continue
		}
		due[t] = struct{}{}
		if _, exists := seen[t.id]; !exists {
			ids = append(ids, t.id)
			seen[t.id] = struct{}{}
		}
	}
	d.mu.Unlock()

	var receipts map[string]Receipt
	if len(ids) > 0 {
		var err error
		receipts, err = d.sender.GetReceipts(ctx, ids)
		if err != nil {
			d.log.Warn("fetch push receipts failed", "err", err, "tickets", len(ids))
		}
	}

	now = d.clock()
	d.mu.Lock()
	keep := d.pending[:0]
	var deadTokens []string
	pruned := make(map[string]struct{})
	for _, t := range d.pending {
		if now.Sub(t.sentAt) >= receiptMaxAge {
			continue
		}
		r, found := receipts[t.id]
		_, queried := due[t]
		if !queried || !found || (r.Status != "ok" && r.Status != "error") {
			keep = append(keep, t)
			continue
		}
		if r.IsDeviceNotRegistered() {
			token := t.token
			if _, seen := pruned[token]; !t.pruneAmbiguous && token != "" && !seen {
				deadTokens = append(deadTokens, token)
				pruned[token] = struct{}{}
			}
		} else if r.Status == "error" {
			d.log.Warn("push delivery error", "err", r.Details.Error, "message", r.Message)
		}
	}
	clear(d.pending[len(keep):])
	d.pending = keep
	d.mu.Unlock()
	for _, token := range deadTokens {
		d.prune(token)
	}
}

func messageFor(rec domain.NotificationRecord, token, hostID, hostName string) Message {
	label := strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, hostName)), " ")
	if label == "" {
		label = hostID
		if len(label) > 10 {
			label = label[:10]
		}
	}
	if runes := []rune(label); len(runes) > 40 {
		label = string(runes[:40]) + "…"
	}
	return Message{
		To:        token,
		Title:     fmt.Sprintf("%s · %s", label, rec.Title),
		Body:      rec.Body,
		Sound:     "default",
		Priority:  "high",
		ChannelID: androidChannelID,
		Data: map[string]any{
			"type":           string(rec.Type),
			"hostId":         hostID,
			"sessionId":      string(rec.SessionID),
			"projectId":      string(rec.ProjectID),
			"prUrl":          rec.PRURL,
			"notificationId": rec.ID,
		},
	}
}
