package awake

import (
	"context"
	"net/netip"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func validateRule(r domain.AwakeRule) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 120 {
		return invalid("name the automatic rule")
	}
	if r.Match != "all" && r.Match != "any" {
		return invalid("rule must match all or any conditions")
	}
	if len(r.Conditions) == 0 || len(r.Conditions) > 12 {
		return invalid("choose between one and twelve conditions")
	}
	for _, c := range r.Conditions {
		switch c.Kind {
		case "agent_activity":
		case "schedule":
			if _, err := time.Parse("15:04", c.Start); err != nil {
				return invalid("schedule start must be HH:mm")
			}
			if _, err := time.Parse("15:04", c.End); err != nil {
				return invalid("schedule end must be HH:mm")
			}
			if c.Timezone != "" {
				if _, err := time.LoadLocation(c.Timezone); err != nil {
					return invalid("invalid schedule timezone")
				}
			}
		case "power":
			if c.Value != "ac" && c.Value != "battery" {
				return invalid("choose AC or battery power")
			}
		case "battery":
			if c.Threshold < 10 || c.Threshold > 100 {
				return invalid("battery condition must be 10–100 percent")
			}
		case "network":
			if _, err := netip.ParsePrefix(c.Value); err != nil {
				return invalid("enter a network CIDR, such as 192.168.1.0/24")
			}
		case "device", "app":
			if c.Value == "" || len(c.Value) > 256 {
				return invalid("choose a connected volume or application")
			}
		case "display":
			if c.Threshold < 1 || c.Threshold > 16 {
				return invalid("choose one to sixteen external displays")
			}
		case "uptime":
			if c.Threshold < 0 {
				return invalid("uptime cannot be negative")
			}
		case "download":
		default:
			return invalid("unknown automatic condition")
		}
	}
	return nil
}

// Three-valued conditions: unknown cannot activate a rule or clear suppression.
func truth(value bool) int {
	if value {
		return 1
	}
	return 0
}
func (s *Service) matchRule(ctx context.Context, r domain.AwakeRule) int {
	return s.matchRuleActive(ctx, r, false)
}
func (s *Service) matchRuleActive(ctx context.Context, r domain.AwakeRule, active bool) int {
	unknown := false
	for _, c := range r.Conditions {
		// Three percentage points of hysteresis apply only to an already
		// active rule. Critical/battery safety stops remain independent.
		if c.Kind == "battery" && active {
			c.Threshold = max(10, c.Threshold-3)
		}
		value := s.condition(ctx, c)
		if r.Match == "all" && value == 0 {
			return 0
		}
		if r.Match == "any" && value == 1 {
			return 1
		}
		unknown = unknown || value < 0
	}
	if unknown {
		return -1
	}
	return truth(r.Match == "all")
}
func (s *Service) condition(ctx context.Context, c domain.AwakeCondition) int {
	o := s.observation
	switch c.Kind {
	case "agent_activity":
		if o.AgentActive == nil {
			return -1
		}
		return truth(*o.AgentActive)
	case "schedule":
		zone := time.Local
		if c.Timezone != "" {
			var err error
			zone, err = time.LoadLocation(c.Timezone)
			if err != nil {
				return -1
			}
		}
		current := s.now().In(zone).Format("15:04")
		if c.Start < c.End {
			return truth(current >= c.Start && current < c.End)
		}
		return truth(current >= c.Start || current < c.End)
	case "power":
		if o.OnBattery == nil {
			return -1
		}
		return truth(*o.OnBattery == (c.Value == "battery"))
	case "battery":
		if o.BatteryPercent == nil {
			return -1
		}
		return truth(*o.BatteryPercent >= c.Threshold)
	case "network":
		if o.NetworkAddresses == nil {
			return -1
		}
		prefix, err := netip.ParsePrefix(c.Value)
		if err != nil {
			return -1
		}
		for _, address := range o.NetworkAddresses {
			ip, err := netip.ParseAddr(address)
			if err == nil && prefix.Contains(ip) {
				return 1
			}
		}
		return 0
	case "device":
		if o.Devices == nil {
			return -1
		}
		for _, device := range o.Devices {
			if device == c.Value {
				return 1
			}
		}
		return 0
	case "display":
		if o.ObservedAt.IsZero() || o.Thermal == "" {
			return -1
		}
		return truth(o.ExternalDisplays >= c.Threshold)
	case "app":
		alive, _, err := s.bindings.Alive(ctx, "app", c.Value, "")
		if err != nil {
			return -1
		}
		return truth(alive)
	case "uptime":
		if o.UptimeSeconds <= 0 {
			return -1
		}
		return truth(o.UptimeSeconds >= float64(c.Threshold))
	case "download":
		if s.downloadsSeen.IsZero() || s.now().Sub(s.downloadsSeen) > 20*time.Second {
			return -1
		}
		id := c.Value
		if id == "" {
			id = "any"
		}
		return truth(s.downloadActive(id, s.now()))
	}
	return -1
}

// Require matching observations across a five-second interval. Missing or stale
// observations withdraw ownership immediately and restart the settling window.
type ruleMatch struct{ since, last time.Time }

func (s *Service) stableRule(id string, match int, active bool, now time.Time) bool {
	previous, exists := s.ruleMatches[id]
	if match != 1 {
		delete(s.ruleMatches, id)
		return false
	}
	if !exists || now.Sub(previous.last) > 10*time.Second || now.Before(previous.last) {
		previous.since = now
		active = false
	}
	previous.last = now
	s.ruleMatches[id] = previous
	return active || now.Sub(previous.since) >= 5*time.Second
}
