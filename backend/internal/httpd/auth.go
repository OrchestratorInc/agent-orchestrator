package httpd

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type authState struct {
	hashes      atomic.Pointer[authHashes]
	devices     *mobilebridge.AccessDevices
	codes       *mobilebridge.PairingCodes
	passwordOff atomic.Bool
}
type authHashes struct {
	current string
	account string
	retired []string
}

const maxRetiredHashes = 8

func (a *authState) setHash(h string) {
	for {
		previous := a.hashes.Load()
		if previous != nil && previous.current == h {
			return
		}
		next := &authHashes{current: h}
		if previous != nil {
			next.account = previous.account
			next.retired = append(next.retired, previous.retired...)
			if previous.current != "" {
				next.retired = append(next.retired, previous.current)
			}
			if len(next.retired) > maxRetiredHashes {
				next.retired = next.retired[len(next.retired)-maxRetiredHashes:]
			}
		}
		if a.hashes.CompareAndSwap(previous, next) {
			return
		}
	}
}
func (a *authState) setAccountHash(h string) {
	for {
		previous := a.hashes.Load()
		next := &authHashes{account: h}
		if previous != nil {
			next.current = previous.current
			next.retired = previous.retired
		}
		if a.hashes.CompareAndSwap(previous, next) {
			return
		}
	}
}
func (a *authState) accountHash() string {
	if hashes := a.hashes.Load(); hashes != nil {
		return hashes.account
	}
	return ""
}
func (a *authState) currentHash() string {
	if hashes := a.hashes.Load(); hashes != nil {
		return hashes.current
	}
	return ""
}

func (a *authState) authenticate(tok string, pairing bool) (credential requestscope.Credential, ok, passwordRefused bool) {
	if mobilebridge.PasswordMatches(a.currentHash(), tok) {
		if a.passwordOff.Load() {
			return requestscope.Credential{}, false, true
		}
		return requestscope.Credential{Kind: requestscope.CredentialPassword}, true, false
	}
	if mobilebridge.PasswordMatches(a.accountHash(), tok) {
		return requestscope.Credential{Kind: requestscope.CredentialAccount}, true, false
	}
	if a.devices != nil {
		if device, found := a.devices.Match(tok); found {
			return requestscope.Credential{Kind: requestscope.CredentialDevice, DeviceID: device.ID}, true, false
		}
	}
	if pairing && a.codes != nil && a.codes.Valid(tok) {
		return requestscope.Credential{Kind: requestscope.CredentialPairingCode}, true, false
	}
	return requestscope.Credential{}, false, false
}

func (a *authState) noteUse(credential requestscope.Credential, now time.Time) {
	if a.devices == nil {
		return
	}
	switch credential.Kind {
	case requestscope.CredentialDevice:
		a.devices.Touch(credential.DeviceID, now)
	case requestscope.CredentialPassword:
		a.devices.TouchShared(mobilebridge.SharedPassword, now)
	case requestscope.CredentialAccount:
		a.devices.TouchShared(mobilebridge.SharedAccount, now)
	case requestscope.CredentialPairingCode:
	}
}

func (a *authState) retiredPasswordMatches(token string) bool {
	if hashes := a.hashes.Load(); hashes != nil {
		for _, hash := range hashes.retired {
			if mobilebridge.PasswordMatches(hash, token) {
				return true
			}
		}
	}
	return false
}

type lockout struct {
	mu        sync.Mutex
	now       func() time.Time
	attempts  map[string]authAttempts
	lastSweep time.Time
}

type authAttempts struct {
	fails   int
	expires time.Time
}

func newLockout(now func() time.Time) *lockout {
	return &lockout{now: now, attempts: map[string]authAttempts{}}
}

func (l *lockout) blocked(src string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	a, ok := l.attempts[src]
	if !ok {
		return false
	}
	if l.now().Before(a.expires) {
		return a.fails >= 5
	}
	delete(l.attempts, src)
	return false
}

func (l *lockout) fail(src string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if now.Sub(l.lastSweep) >= time.Minute {
		for key, a := range l.attempts {
			if !now.Before(a.expires) {
				delete(l.attempts, key)
			}
		}
		l.lastSweep = now
	}
	a := l.attempts[src]
	if !now.Before(a.expires) {
		a.fails = 0
	}
	a.fails++
	a.expires = now.Add(time.Minute)
	l.attempts[src] = a
}

func (l *lockout) reset(src string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, src)
}

func (l *lockout) resetAll() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts = map[string]authAttempts{}
}

func sourceKey(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func clientSource(r *http.Request) string {
	src := sourceKey(r)
	if ip := net.ParseIP(src); ip != nil && ip.IsLoopback() {
		if forwarded := r.Header.Values("CF-Connecting-IP"); len(forwarded) == 1 {
			if visitor := net.ParseIP(forwarded[0]); visitor != nil {
				return visitor.String()
			}
		}
	}
	return src
}

func lockoutKey(src string) string {
	ip := net.ParseIP(src)
	if ip == nil || ip.To4() != nil {
		return src
	}
	return ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimPrefix(h, "Bearer ")
	}
	return ""
}

const authCookieName = "ao_conn"

const previewFilesMarker = "/preview/files/"

func previewFilesCookiePath(urlPath string) string {
	i := strings.Index(urlPath, previewFilesMarker)
	if i < 0 {
		return ""
	}
	return urlPath[:i+len(previewFilesMarker)]
}

func connectionToken(r *http.Request) string {
	if t := bearerToken(r); t != "" {
		return t
	}
	if previewFilesCookiePath(r.URL.Path) != "" {
		if c, err := r.Cookie(authCookieName); err == nil {
			return c.Value
		}
	}
	return ""
}

func maybeSetPreviewAuthCookie(w http.ResponseWriter, r *http.Request, tok string) {
	path := previewFilesCookiePath(r.URL.Path)
	if path == "" {
		return
	}
	if c, err := r.Cookie(authCookieName); err == nil && c.Value == tok {
		return
	}
	//nolint:gosec // Secure is intentionally omitted: the LAN bridge is plaintext
	http.SetCookie(w, &http.Cookie{
		Name:     authCookieName,
		Value:    tok,
		Path:     path,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

const identityProbePath = "/api/v1/identity"

func isIdentityProbe(r *http.Request) bool {
	return r.Method == http.MethodGet && r.URL.Path == identityProbePath
}

const devicePairPath = "/api/v1/remote-host/devices"

func isDevicePairing(r *http.Request) bool {
	return r.Method == http.MethodPost && r.URL.Path == devicePairPath
}

type lanBearerContextKey struct{}

type lanPreviewCookieContextKey struct{}

func authenticatedLANBearer(r *http.Request) bool {
	authenticated, _ := r.Context().Value(lanBearerContextKey{}).(bool)
	return authenticated
}

func authMiddleware(state *authState, lock *lockout, connected *mobileConnectReporter) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if isIdentityProbe(r) {
				next.ServeHTTP(w, r)
				return
			}
			remoteSrc := sourceKey(r)
			src := lockoutKey(clientSource(r))
			if lock.blocked(src) {
				envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "LOCKED_OUT",
					"too many failed attempts; try again shortly", nil)
				return
			}
			tok := connectionToken(r)
			conn, _ := r.Context().Value(lanConnContextKey{}).(*lanConn)
			if conn != nil {
				conn.owner.mu.Lock()
			}
			credential, authenticated, passwordRefused := state.authenticate(tok, isDevicePairing(r))
			if conn != nil {
				if authenticated {
					conn.tokenHash = mobilebridge.HashPassword(tok)
				}
				conn.owner.mu.Unlock()
			}
			if authenticated {
				lock.reset(src)
				connected.report(remoteSrc)
				state.noteUse(credential, lock.now())
				maybeSetPreviewAuthCookie(w, r, tok)
				ctx := requestscope.WithCredential(r.Context(), credential)
				if bearerToken(r) != "" {
					ctx = context.WithValue(ctx, lanBearerContextKey{}, true)
				} else {
					ctx = context.WithValue(ctx, lanPreviewCookieContextKey{}, true)
				}
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
			if passwordRefused {
				envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DEVICE_CREDENTIAL_REQUIRED",
					"this host no longer accepts the pairing password; pair this device again", nil)
				return
			}
			if strings.HasPrefix(tok, mobilebridge.DeviceTokenPrefix) {
				envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "DEVICE_REVOKED",
					"this device was removed from the host; pair it again", nil)
				return
			}
			if tok != "" && !state.retiredPasswordMatches(tok) {
				lock.fail(src)
			}
			envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "BAD_PASSWORD",
				"missing or invalid connection password", nil)
		})
	}
}
