package mobilebridge

import (
	"sync"
	"time"
)

// PairingCodeTTL is how long an unused pairing code stays valid.
const PairingCodeTTL = 10 * time.Minute

// maxPairingCodes bounds the unused codes kept in memory.
const maxPairingCodes = 8

// PairingCodes holds the unused one-time pairing codes, in memory only.
type PairingCodes struct {
	mu    sync.Mutex
	now   func() time.Time
	codes map[string]time.Time // SHA-256 of the code -> expiry
}

// NewPairingCodes returns an empty set; a nil clock is the wall clock.
func NewPairingCodes(now func() time.Time) *PairingCodes {
	if now == nil {
		now = time.Now
	}
	return &PairingCodes{now: now, codes: map[string]time.Time{}}
}

// Issue makes a new code and returns it with its expiry.
func (p *PairingCodes) Issue() (string, time.Time, error) {
	code, err := GeneratePassword()
	if err != nil {
		return "", time.Time{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	p.pruneLocked(now)
	for len(p.codes) >= maxPairingCodes {
		oldest := ""
		for hash, expiry := range p.codes {
			if oldest == "" || expiry.Before(p.codes[oldest]) {
				oldest = hash
			}
		}
		delete(p.codes, oldest)
	}
	expiry := now.Add(PairingCodeTTL)
	p.codes[HashPassword(code)] = expiry
	return code, expiry, nil
}

// Valid reports whether code is an unused, unexpired pairing code.
func (p *PairingCodes) Valid(code string) bool {
	if code == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(p.now())
	_, ok := p.codes[HashPassword(code)]
	return ok
}

// Consume uses up code and reports whether it was valid.
func (p *PairingCodes) Consume(code string) bool {
	if code == "" {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(p.now())
	hash := HashPassword(code)
	if _, ok := p.codes[hash]; !ok {
		return false
	}
	delete(p.codes, hash)
	return true
}

// Clear drops every unused code.
func (p *PairingCodes) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.codes = map[string]time.Time{}
}

func (p *PairingCodes) pruneLocked(now time.Time) {
	for hash, expiry := range p.codes {
		if !now.Before(expiry) {
			delete(p.codes, hash)
		}
	}
}
