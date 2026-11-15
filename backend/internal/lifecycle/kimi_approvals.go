package lifecycle

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// maxKimiApprovals bounds both the map and FIFO per session. Wire keys are
// capped at 256 bytes at ingress (the CLI emits a 78-byte scoped approval hash).
// On insertion, evict the oldest completed identity, never a pending approval.
// If all slots are pending, latch overflow: results cannot clear waiting_input
// until an existing turn boundary settles the unknown outstanding set.
//
// Completion evidence survives an empty pending set, Stop and new prompts;
// boundaries settle observed pending entries and reset overflow. Process exit,
// Kimi spawn, termination and controller replacement delete the whole
// toolFlight. Daemon restart also loses it. Consequently callbacks
// beyond eviction/reset, or requests never observed before a boundary, are not
// ordered by this cache; native hooks supply no durable ordered delivery log.
const maxKimiApprovals = 128

func (f *toolFlight) applyKimiApproval(s ports.ActivitySignal) ports.ActivitySignal {
	suppressed := s
	suppressed.Valid = false
	key := s.ToolUseID
	identified := strings.HasPrefix(key, "kimi-approval:") && len(key) > len("kimi-approval:") && len(key) <= 256
	if identified {
		completed, known := f.kimiApprovals[key]
		if known && completed {
			return suppressed // duplicate result or a request delivered after its result
		}
		if !known {
			if len(f.kimiApprovals) >= maxKimiApprovals {
				for i, old := range f.kimiApprovalOrder {
					if f.kimiApprovals[old] {
						delete(f.kimiApprovals, old)
						f.kimiApprovalOrder = append(f.kimiApprovalOrder[:i], f.kimiApprovalOrder[i+1:]...)
						break
					}
				}
			}
			if len(f.kimiApprovals) >= maxKimiApprovals {
				f.kimiApprovalOverflow = true
			} else {
				if f.kimiApprovals == nil {
					f.kimiApprovals = make(map[string]bool)
					f.kimiApprovalOrder = make([]string, 0, maxKimiApprovals)
				}
				f.kimiApprovalOrder = append(f.kimiApprovalOrder, key)
				f.kimiApprovals[key] = s.Event == "permission-resolved"
			}
		} else if s.Event == "permission-resolved" {
			f.kimiApprovals[key] = true
		}
	}
	if s.Event == "permission-request" {
		return s
	}
	if f.kimiApprovalOverflow {
		return suppressed
	}
	for _, completed := range f.kimiApprovals {
		if !completed {
			return suppressed // includes ID-less results while identified approvals remain
		}
	}
	// ID-less installs preserve their legacy clearing behavior only when no
	// identified pending approval (or overflow uncertainty) contradicts it.
	return s
}

func (f *toolFlight) settleKimiApprovals() {
	for key := range f.kimiApprovals {
		f.kimiApprovals[key] = true
	}
	f.kimiApprovalOverflow = false
}
