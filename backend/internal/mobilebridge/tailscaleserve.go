package mobilebridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type Serve struct {
	Run TailscaleRunner
}

func NewServe() *Serve { return &Serve{} }

func (s *Serve) run(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, tailscaleTimeout)
	defer cancel()
	if s.Run != nil {
		return s.Run(ctx, args...)
	}
	return execTailscale(ctx, args...)
}

func (s *Serve) Apply(ctx context.Context, bridgePort int) error {
	target := fmt.Sprintf("http://127.0.0.1:%d", bridgePort)
	if _, err := s.run(ctx, "serve", "--bg", "--https=443", target); err != nil {
		return fmt.Errorf("tailscale serve --https=443 %s: %w", target, err)
	}
	return nil
}

func (s *Serve) Clear(ctx context.Context) error {
	if _, err := s.run(ctx, "serve", "--https=443", "off"); err != nil {
		return fmt.Errorf("tailscale serve --https=443 off: %w", err)
	}
	return nil
}

func (s *Serve) Target(ctx context.Context) int {
	out, err := s.run(ctx, "serve", "status", "--json")
	if err != nil {
		return 0
	}
	var parsed struct {
		Web map[string]struct {
			Handlers map[string]struct{ Proxy string } `json:"Handlers"`
		} `json:"Web"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return 0
	}
	for key, site := range parsed.Web {
		if !strings.HasSuffix(key, ":443") {
			continue
		}
		for _, h := range site.Handlers {
			u, err := url.Parse(h.Proxy)
			if err != nil {
				continue
			}
			if port, err := strconv.Atoi(u.Port()); err == nil {
				return port
			}
		}
	}
	return 0
}
