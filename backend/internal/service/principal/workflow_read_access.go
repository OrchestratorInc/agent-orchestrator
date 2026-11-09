package principal

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// HeaderWorkflowReadAccess 承载仅供指定工作流读取的短期凭据。
const HeaderWorkflowReadAccess = "X-AO-Workflow-Read-Access"

// WorkflowReadAccessTTL 限制人工授权后读取凭据的有效期。
const WorkflowReadAccessTTL = 30 * time.Minute

var workflowReadID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// workflowReadGrant 仅在本次 daemon 内存有效，精确限制 Workflow 和已注册的 GET 路由。
type workflowReadGrant struct {
	workflowID, actor string
	expiresAt         time.Time
}

// GrantWorkflowReadAccess 只能在一次性原生审批已消费后调用；返回凭据仅交给桌面 Main。
func (s *ApprovalStore) GrantWorkflowReadAccess(workflowID, actor string) (string, time.Time, error) {
	if s == nil || !workflowReadID.MatchString(workflowID) || actor == "" {
		return "", time.Time{}, ErrNotAuthorized
	}
	var bytes [32]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", time.Time{}, err
	}
	token := hex.EncodeToString(bytes[:])
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clock().UTC()
	if s.readGrants == nil {
		s.readGrants = make(map[string]workflowReadGrant)
	}
	for key, grant := range s.readGrants {
		if !now.Before(grant.expiresAt) {
			delete(s.readGrants, key)
		}
	}
	if len(s.readGrants) >= s.max {
		return "", time.Time{}, fmt.Errorf("read access limit reached")
	}
	expires := now.Add(WorkflowReadAccessTTL)
	s.readGrants[hex.EncodeToString(sum[:])] = workflowReadGrant{workflowID: workflowID, actor: actor, expiresAt: expires}
	return token, expires, nil
}

// proveWorkflowReadAccess 不允许写入、跨 Workflow、混合审批或非回环请求，失效不触发新授权。
func (s *ApprovalStore) proveWorkflowReadAccess(r *http.Request) (string, error) {
	if s == nil || r == nil || !LoopbackPeer(r) || r.Method != http.MethodGet || hasHeader(r, HeaderOperatorApproval) || r.ContentLength > 0 {
		return "", ErrNotAuthorized
	}
	op, ok := MatchOperatorOperation(r.Method, r.URL.RequestURI())
	if !ok {
		return "", ErrNotAuthorized
	}
	token := r.Header.Get(HeaderWorkflowReadAccess)
	if len(token) != 64 {
		return "", ErrNotAuthorized
	}
	sum := sha256.Sum256([]byte(token))
	s.mu.Lock()
	defer s.mu.Unlock()
	grant, ok := s.readGrants[hex.EncodeToString(sum[:])]
	if !ok || !s.clock().UTC().Before(grant.expiresAt) {
		return "", ErrNotAuthorized
	}
	if op.Path != "/api/v1/workflows/capabilities" && !strings.HasPrefix(r.URL.Path, "/api/v1/workflows/"+grant.workflowID+"/") {
		return "", ErrNotAuthorized
	}
	return grant.actor, nil
}
