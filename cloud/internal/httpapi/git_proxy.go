package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
)

// The git proxy carries a session's clone, fetch and push to GitHub so the
// sandbox never holds a GitHub credential (see worker.GitProxyRoutePrefix).
// Git in the sandbox talks smart HTTP to
//
//	<public URL>/api/cloud/v1/worker/git/<sessionId>/<owner>/<repo>.git/...
//
// authenticated with the worker token. The proxy accepts only the smart-HTTP
// endpoints, checks that the worker is the session's current one and that the
// repository is one of the session's, and streams the request to GitHub with
// an installation token it mints and keeps itself. The worker's credential is
// never forwarded, and GitHub's credential never comes back.

const (
	// gitProxyMaxDuration bounds one git request end to end: a clone of a large
	// repository, or a large push, streams for a while but not forever.
	gitProxyMaxDuration = time.Hour
	// gitProxyResponseHeaderTimeout is how long GitHub may take to start
	// answering once the request is sent. A push is answered only after GitHub
	// has received and checked the whole pack.
	gitProxyResponseHeaderTimeout = 10 * time.Minute
	// gitProxyMaxPush matches GitHub's own limit on one push.
	gitProxyMaxPush = 2 << 30
	// gitProxyMaxFetchRequest bounds a fetch's negotiation body (the haves).
	gitProxyMaxFetchRequest = 256 << 20
	// gitProxyMaxCommands bounds how much of a push the proxy reads before its
	// pack to check the ref updates.
	gitProxyMaxCommands = 1 << 20
)

// gitProxyRequest is one smart-HTTP request the proxy accepts.
type gitProxyRequest struct {
	owner, repo string
	// suffix is the smart-HTTP path after <repo>.git/.
	suffix string
	// push is a receive-pack request (or its ref advertisement).
	push bool
}

// parseGitProxyPath accepts exactly the smart-HTTP endpoints: the ref
// advertisement for either service, and the two RPCs. Everything else (the
// dumb protocol, LFS, arbitrary paths) is refused.
func parseGitProxyPath(method, path, service string) (gitProxyRequest, bool) {
	segments := strings.Split(path, "/")
	if len(segments) < 3 {
		return gitProxyRequest{}, false
	}
	owner, repo := segments[0], strings.TrimSuffix(segments[1], ".git")
	if !validGitHubName(owner) || !validGitHubName(repo) {
		return gitProxyRequest{}, false
	}
	request := gitProxyRequest{owner: owner, repo: repo, suffix: strings.Join(segments[2:], "/")}
	switch {
	case method == http.MethodGet && request.suffix == "info/refs":
		switch service {
		case "git-upload-pack":
		case "git-receive-pack":
			request.push = true
		default:
			return gitProxyRequest{}, false
		}
	case method == http.MethodPost && request.suffix == "git-upload-pack":
	case method == http.MethodPost && request.suffix == "git-receive-pack":
		request.push = true
	default:
		return gitProxyRequest{}, false
	}
	return request, true
}

// validGitHubName accepts a GitHub owner or repository name: letters, digits,
// '-', '_' and '.', never a path component like "." or "..".
func validGitHubName(name string) bool {
	if name == "" || len(name) > 100 || name == "." || name == ".." {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' ||
			r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// workerGitAuth authenticates git's HTTP Basic credential (any username, the
// worker token as password) the way workerAuth authenticates the worker
// header. Without one it asks git for credentials, which makes git run the
// session credential helper.
func (s *Server) workerGitAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, token, ok := r.BasicAuth()
		if !ok || strings.TrimSpace(token) == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="AO git", charset="UTF-8"`)
			http.Error(w, "AO: a worker credential is required", http.StatusUnauthorized)
			return
		}
		claims, status, message := s.verifyWorkerToken(r.Context(), token)
		if status != 0 {
			http.Error(w, "AO: "+message, status)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), workerContextKey{}, claims)))
	})
}

func (s *Server) workerGitProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		http.Error(w, "AO: the worker:git scope is required", http.StatusForbidden)
		return
	}
	if chi.URLParam(r, "sessionId") != claims.SessionID {
		http.Error(w, "AO: this worker cannot reach another session's repositories", http.StatusForbidden)
		return
	}
	request, ok := parseGitProxyPath(r.Method, chi.URLParam(r, "*"), r.URL.Query().Get("service"))
	if !ok {
		http.Error(w, "AO: only git smart-HTTP fetch and push are supported", http.StatusNotFound)
		return
	}
	fullName := request.owner + "/" + request.repo
	token, err := s.gitProxyToken(r.Context(), claims, fullName, request.push)
	if err != nil {
		status, message := gitProxyAuthorizationError(err, request.push)
		if status >= http.StatusInternalServerError {
			s.logger.Error("git proxy credential", "error", err, "repository", fullName,
				"push", request.push, "request_id", requestID(r))
		}
		http.Error(w, "AO: "+message, status)
		return
	}
	limit := int64(gitProxyMaxFetchRequest)
	if request.push {
		limit = gitProxyMaxPush
	}
	// The server's read timeout is sized for API calls; a large push or a
	// long fetch negotiation streams for longer.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(gitProxyMaxDuration))
	_ = controller.SetWriteDeadline(time.Now().Add(gitProxyMaxDuration))
	body := io.Reader(http.MaxBytesReader(w, r.Body, limit))
	if r.Method == http.MethodPost && request.push {
		checked, refusal, err := checkGitPushCommands(r.Header.Get("Content-Encoding"), body)
		if err != nil {
			http.Error(w, "AO: the push could not be read", http.StatusBadRequest)
			return
		}
		if refusal != "" {
			s.logger.Info("git proxy refused a push", "repository", fullName, "reason", refusal,
				"session_id", claims.SessionID, "request_id", requestID(r))
			writeGitPushRefusal(w, refusal)
			return
		}
		body = checked
	}
	s.forwardGitRequest(w, r, request, body, token)
}

// gitProxyToken is the installation token the proxy forwards a request with,
// or "" for an anonymous read of a public repository where that is allowed.
func (s *Server) gitProxyToken(ctx context.Context, claims worker.Claims, fullName string, push bool) (string, error) {
	if s.checkoutBroker == nil {
		if !push && s.allowAnonymousGitHubCheckout {
			return "", nil
		}
		return "", errGitProxyUnavailable
	}
	grant, err := s.checkoutBroker.IssueGitProxyGrant(ctx, claims.OrgID, claims.SessionID, fullName, push)
	if err == nil && grant.Token == "" {
		err = errors.New("the repository broker returned an empty credential")
	}
	if err != nil {
		// A session without a repository grant may still read a public
		// repository anonymously where the deployment allows it; GitHub then
		// answers for a private one exactly as it would without the proxy.
		if !push && s.allowAnonymousGitHubCheckout &&
			(errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound)) {
			return "", nil
		}
		return "", err
	}
	return grant.Token, nil
}

var errGitProxyUnavailable = errors.New("git proxy has no repository broker")

func gitProxyAuthorizationError(err error, push bool) (int, string) {
	switch {
	case errors.Is(err, postgres.ErrForbidden) || errors.Is(err, postgres.ErrNotFound):
		if push {
			return http.StatusForbidden, "this session cannot push to that repository"
		}
		return http.StatusForbidden, "this session cannot read that repository"
	case errors.Is(err, errGitProxyUnavailable):
		return http.StatusServiceUnavailable, "repository access is not available"
	default:
		return http.StatusBadGateway, "a repository credential could not be issued"
	}
}

// forwardGitRequest streams the request to GitHub and GitHub's answer back,
// never buffering a pack.
func (s *Server) forwardGitRequest(
	w http.ResponseWriter,
	r *http.Request,
	request gitProxyRequest,
	body io.Reader,
	token string,
) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(r.Context(), gitProxyMaxDuration)
	defer cancel()
	target := s.gitUpstreamURL + "/" + request.owner + "/" + request.repo + ".git/" + request.suffix
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	var upstreamBody io.Reader
	if r.Method == http.MethodPost {
		upstreamBody = body
	}
	upstream, err := http.NewRequestWithContext(ctx, r.Method, target, upstreamBody)
	if err != nil {
		http.Error(w, "AO: the request could not be forwarded", http.StatusBadGateway)
		return
	}
	// Only the headers smart HTTP needs are forwarded: never the worker's
	// Authorization, cookies, or anything else the sandbox sent.
	for _, header := range []string{"Content-Type", "Accept", "Accept-Encoding", "Content-Encoding", "Git-Protocol", "User-Agent"} {
		if value := r.Header.Get(header); value != "" {
			upstream.Header.Set(header, value)
		}
	}
	if r.Method == http.MethodPost && r.ContentLength > 0 {
		// The body is the sandbox's, byte for byte (a checked push replays its
		// commands), so its length is unchanged.
		upstream.ContentLength = r.ContentLength
	}
	if token != "" {
		upstream.SetBasicAuth("x-access-token", token)
	}
	response, err := s.gitProxyClient.Do(upstream)
	// GitHub can refuse an installation token it minted moments ago on its git
	// frontend for a while. The ref advertisement (a GET with no body) is safe
	// to ask again, and once it succeeds the token works for the RPC after it.
	for attempt := 0; err == nil && token != "" && r.Method == http.MethodGet &&
		response.StatusCode == http.StatusForbidden && attempt < len(s.gitProxyRetryDelays); attempt++ {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		timer := time.NewTimer(s.gitProxyRetryDelays[attempt])
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		s.logger.Info("git proxy retrying a refused ref advertisement",
			"repository", request.owner+"/"+request.repo, "push", request.push, "attempt", attempt+1)
		response, err = s.gitProxyClient.Do(upstream.Clone(ctx))
	}
	if err != nil {
		var maxBytes *http.MaxBytesError
		if errors.As(err, &maxBytes) {
			http.Error(w, "AO: the request is larger than GitHub accepts", http.StatusRequestEntityTooLarge)
			return
		}
		s.logger.Warn("git proxy upstream request failed", "error", err, "repository", request.owner+"/"+request.repo,
			"push", request.push, "request_id", requestID(r))
		http.Error(w, "AO: GitHub could not be reached", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	for _, header := range []string{"Content-Type", "Content-Encoding", "Cache-Control", "Expires", "Pragma"} {
		if value := response.Header.Get(header); value != "" {
			w.Header().Set(header, value)
		}
	}
	status := response.StatusCode
	if status == http.StatusUnauthorized {
		// GitHub refused the proxy's credential (or, anonymously, a private
		// repository). Asking git for another credential would only resend the
		// worker token, so it is an authorization failure.
		status = http.StatusForbidden
	}
	w.WriteHeader(status)
	written, copyErr := io.Copy(flushingWriter{w: w, controller: http.NewResponseController(w)}, response.Body)
	attrs := []any{
		"repository", request.owner + "/" + request.repo,
		"endpoint", request.suffix,
		"push", request.push,
		"status", response.StatusCode,
		"response_bytes", written,
		"duration_ms", time.Since(started).Milliseconds(),
		"request_id", requestID(r),
	}
	if copyErr != nil && !errors.Is(copyErr, context.Canceled) {
		s.logger.Warn("git proxy stream ended early", append(attrs, "error", copyErr)...)
		return
	}
	s.logger.Info("git proxy request complete", attrs...)
}

// defaultGitProxyRetryDelays space the retries of a refused ref advertisement
// (see forwardGitRequest): about 20 seconds in all, well inside git's own
// low-speed limit.
var defaultGitProxyRetryDelays = []time.Duration{2 * time.Second, 4 * time.Second, 6 * time.Second, 8 * time.Second}

// flushingWriter flushes after every write so git sees progress and the
// pack as GitHub sends them.
type flushingWriter struct {
	w          io.Writer
	controller *http.ResponseController
}

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		_ = f.controller.Flush()
	}
	return n, err
}

// newGitProxyClient is the proxy's client to GitHub: no overall timeout (a
// clone streams for as long as it needs, bounded by gitProxyMaxDuration), but
// bounded connection setup and time to first response byte. It never follows
// redirects, so a credential is only ever sent to the configured origin.
func newGitProxyClient() *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: gitProxyResponseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		MaxIdleConnsPerHost:   32,
		IdleConnTimeout:       90 * time.Second,
		// Git compresses its own payloads; letting the transport decompress
		// would drop Content-Encoding from what git receives.
		DisableCompression: true,
	}
	return &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// checkGitPushCommands reads a receive-pack request's ref update commands,
// which precede its pack, and refuses the updates a session may not make:
// deleting a ref, or updating anything other than a branch (a tag, a note, a
// pull request ref). It returns a reader that replays the commands followed
// by the rest of the request, so the pack still streams.
func checkGitPushCommands(contentEncoding string, body io.Reader) (io.Reader, string, error) {
	if encoding := strings.TrimSpace(contentEncoding); encoding != "" && !strings.EqualFold(encoding, "identity") {
		// Git never compresses a push request; a compressed one cannot be
		// checked without decompressing the pack.
		return nil, "AO accepts only uncompressed pushes", nil
	}
	reader := bufio.NewReaderSize(body, 64<<10)
	var commands bytes.Buffer
	for {
		line, flush, err := readPktLine(reader, &commands)
		if err != nil {
			return nil, "", err
		}
		if commands.Len() > gitProxyMaxCommands {
			return nil, "", errors.New("push commands are too large")
		}
		if flush {
			break
		}
		line, _, _ = strings.Cut(line, "\x00")
		line = strings.TrimSuffix(line, "\n")
		if strings.HasPrefix(line, "shallow ") {
			continue
		}
		if strings.HasPrefix(line, "push-cert") {
			return nil, "AO does not accept signed pushes", nil
		}
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return nil, "", fmt.Errorf("malformed push command %q", line)
		}
		newID, ref := fields[1], fields[2]
		if strings.Trim(newID, "0") == "" {
			return nil, "AO does not let a session delete " + ref, nil
		}
		if !strings.HasPrefix(ref, "refs/heads/") {
			return nil, "AO lets a session push branches only, not " + ref, nil
		}
	}
	return io.MultiReader(bytes.NewReader(commands.Bytes()), reader), "", nil
}

// readPktLine reads one pkt-line, copying its raw bytes to raw. flush is true
// for a flush packet ("0000").
func readPktLine(reader *bufio.Reader, raw *bytes.Buffer) (line string, flush bool, err error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", false, err
	}
	raw.Write(header)
	length, err := strconv.ParseUint(string(header), 16, 16)
	if err != nil {
		return "", false, fmt.Errorf("malformed pkt-line length %q", header)
	}
	if length == 0 {
		return "", true, nil
	}
	if length < 4 {
		return "", false, fmt.Errorf("unexpected pkt-line length %d", length)
	}
	payload := make([]byte, length-4)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", false, err
	}
	raw.Write(payload)
	return string(payload), false, nil
}

// writeGitPushRefusal answers a refused push the way receive-pack reports a
// fatal error, so git prints the reason ("remote: AO ...") instead of a bare
// HTTP status.
func writeGitPushRefusal(w http.ResponseWriter, reason string) {
	w.Header().Set("Content-Type", "application/x-git-receive-pack-result")
	w.WriteHeader(http.StatusOK)
	message := append([]byte{3}, []byte(reason+"\n")...)
	_, _ = fmt.Fprintf(w, "%04x", len(message)+4)
	_, _ = w.Write(message)
	_, _ = io.WriteString(w, "0000")
}
