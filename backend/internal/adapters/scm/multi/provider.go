package multi

import (
	"context"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"

	scmobserve "github.com/aoagents/agent-orchestrator/backend/internal/observe/scm"
)

type NamedProvider struct {
	Key      string
	Provider scmobserve.Provider
}

type Provider struct {
	ordered []NamedProvider
	byName  map[string]scmobserve.Provider
}

func New(providers ...NamedProvider) *Provider {
	m := &Provider{
		ordered: providers,
		byName:  make(map[string]scmobserve.Provider, len(providers)),
	}
	for _, p := range providers {
		m.byName[p.Key] = p.Provider
	}
	return m
}

func (m *Provider) resolve(key string) (scmobserve.Provider, error) {
	if p, ok := m.byName[key]; ok {
		return p, nil
	}
	return nil, fmt.Errorf("scm multi: unknown provider %q", key)
}

func (m *Provider) ParseRepository(remote string) (ports.SCMRepo, bool) {
	for _, np := range m.ordered {
		if repo, ok := np.Provider.ParseRepository(remote); ok {
			return repo, true
		}
	}
	return ports.SCMRepo{}, false
}

func (m *Provider) RepoPRListGuard(ctx context.Context, repo ports.SCMRepo, etag string) (ports.SCMGuardResult, error) {
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return p.RepoPRListGuard(ctx, repo, etag)
}

func (m *Provider) ListPRsByRepo(ctx context.Context, repo ports.SCMRepo, updatedAfter time.Time) ([]ports.SCMPRObservation, error) {
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return nil, err
	}
	return p.ListPRsByRepo(ctx, repo, updatedAfter)
}

func (m *Provider) ListOpenPRsByBranch(ctx context.Context, repo ports.SCMRepo, branch string) ([]ports.SCMPRObservation, error) {
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return nil, err
	}
	lister, ok := p.(branchPRLister)
	if !ok {
		return nil, fmt.Errorf("%w: branch PR listing for provider %q", ports.ErrSCMUnsupported, repo.Provider)
	}
	return lister.ListOpenPRsByBranch(ctx, repo, branch)
}

func (m *Provider) CommitChecksGuard(ctx context.Context, repo ports.SCMRepo, headSHA, etag string) (ports.SCMGuardResult, error) {
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return ports.SCMGuardResult{}, err
	}
	return p.CommitChecksGuard(ctx, repo, headSHA, etag)
}

func (m *Provider) FetchPullRequests(ctx context.Context, refs []ports.SCMPRRef) ([]ports.SCMObservation, error) {
	groups := make(map[string][]indexedRef)
	for i, r := range refs {
		groups[r.Repo.Provider] = append(groups[r.Repo.Provider], indexedRef{idx: i, ref: r})
	}

	results := make([]ports.SCMObservation, len(refs))
	groupErrs := make(map[string]error)

	for key, irefs := range groups {
		p, err := m.resolve(key)
		if err != nil {
			groupErrs[key] = err
			for _, ir := range irefs {
				results[ir.idx] = ports.SCMObservation{
					Fetched:  false,
					Provider: key,
					Host:     ir.ref.Repo.Host,
					Repo:     repoFullNameFromRef(ir.ref.Repo),
					PR:       ports.SCMPRObservation{Number: ir.ref.Number, URL: ir.ref.URL},
				}
			}
			continue
		}
		batch := make([]ports.SCMPRRef, len(irefs))
		for j, ir := range irefs {
			batch[j] = ir.ref
		}
		obs, err := p.FetchPullRequests(ctx, batch)
		if err != nil {
			groupErrs[key] = err
		}
		for j, ir := range irefs {
			if j < len(obs) {
				results[ir.idx] = obs[j]
			} else {
				results[ir.idx] = ports.SCMObservation{
					Fetched:  false,
					Provider: key,
					Host:     ir.ref.Repo.Host,
					Repo:     repoFullNameFromRef(ir.ref.Repo),
					PR:       ports.SCMPRObservation{Number: ir.ref.Number, URL: ir.ref.URL},
				}
			}
			if err != nil && !results[ir.idx].Fetched {
				results[ir.idx].Error = err
			}
		}
	}

	return results, nil
}

func (m *Provider) FetchFailedCheckLogTail(ctx context.Context, repo ports.SCMRepo, check ports.SCMCheckObservation) (string, error) {
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return "", err
	}
	return p.FetchFailedCheckLogTail(ctx, repo, check)
}

func (m *Provider) FetchReviewThreads(ctx context.Context, ref ports.SCMPRRef) (ports.SCMReviewObservation, error) {
	p, err := m.resolve(ref.Repo.Provider)
	if err != nil {
		return ports.SCMReviewObservation{}, err
	}
	return p.FetchReviewThreads(ctx, ref)
}

func (m *Provider) RequestReview(ctx context.Context, request ports.SCMReviewRequest) error {
	if m == nil {
		return fmt.Errorf("%w: review request provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(request.PR.Repo.Provider)
	if err != nil {
		return err
	}
	requester, ok := p.(ports.SCMReviewRequester)
	if !ok {
		return fmt.Errorf("%w: review requests for provider %q", ports.ErrSCMUnsupported, request.PR.Repo.Provider)
	}
	return requester.RequestReview(ctx, request)
}

func (m *Provider) ResolveReviewThread(ctx context.Context, request ports.SCMReviewResolveRequest) error {
	if m == nil {
		return fmt.Errorf("%w: review thread resolver is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(request.PR.Repo.Provider)
	if err != nil {
		return err
	}
	resolver, ok := p.(ports.SCMReviewResolver)
	if !ok {
		return fmt.Errorf("%w: review thread resolve for provider %q", ports.ErrSCMUnsupported, request.PR.Repo.Provider)
	}
	return resolver.ResolveReviewThread(ctx, request)
}

func (m *Provider) ChangePullRequestState(ctx context.Context, request ports.SCMPRStateRequest) error {
	if m == nil {
		return fmt.Errorf("%w: pull request state provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(request.PR.Repo.Provider)
	if err != nil {
		return err
	}
	changer, ok := p.(ports.SCMPRStateChanger)
	if !ok {
		return fmt.Errorf("%w: pull request state changes for provider %q", ports.ErrSCMUnsupported, request.PR.Repo.Provider)
	}
	return changer.ChangePullRequestState(ctx, request)
}

func (m *Provider) stacker(name string) (ports.SCMStacker, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: stack provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(name)
	if err != nil {
		return nil, err
	}
	stacker, ok := p.(ports.SCMStacker)
	if !ok {
		return nil, fmt.Errorf("%w: stacks for provider %q", ports.ErrSCMUnsupported, name)
	}
	return stacker, nil
}

func (m *Provider) PullRequestStack(ctx context.Context, ref ports.SCMPRRef) (ports.SCMStack, bool, error) {
	stacker, err := m.stacker(ref.Repo.Provider)
	if err != nil {
		return ports.SCMStack{}, false, err
	}
	return stacker.PullRequestStack(ctx, ref)
}

func (m *Provider) FindOpenPullRequest(ctx context.Context, repo ports.SCMRepo, branch string) (ports.SCMPRRef, bool, error) {
	stacker, err := m.stacker(repo.Provider)
	if err != nil {
		return ports.SCMPRRef{}, false, err
	}
	return stacker.FindOpenPullRequest(ctx, repo, branch)
}

func (m *Provider) RegisterStacked(ctx context.Context, request ports.SCMStackRegisterRequest) (ports.SCMStack, error) {
	stacker, err := m.stacker(request.Repo.Provider)
	if err != nil {
		return ports.SCMStack{}, err
	}
	return stacker.RegisterStacked(ctx, request)
}

func (m *Provider) MergeStack(ctx context.Context, request ports.SCMStackMergeRequest) (ports.SCMStackMergeStatus, error) {
	stacker, err := m.stacker(request.PR.Repo.Provider)
	if err != nil {
		return "", err
	}
	return stacker.MergeStack(ctx, request)
}

func (m *Provider) HeadChecks(ctx context.Context, repo ports.SCMRepo, sha string) (ports.SCMHeadChecks, error) {
	if m == nil {
		return ports.SCMHeadChecks{}, fmt.Errorf("%w: head checks provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(repo.Provider)
	if err != nil {
		return ports.SCMHeadChecks{}, err
	}
	reader, ok := p.(ports.SCMHeadChecksReader)
	if !ok {
		return ports.SCMHeadChecks{}, fmt.Errorf("%w: head checks for provider %q", ports.ErrSCMUnsupported, repo.Provider)
	}
	return reader.HeadChecks(ctx, repo, sha)
}

func (m *Provider) PostComment(ctx context.Context, request ports.SCMCommentRequest) (ports.SCMCommentResult, error) {
	if m == nil {
		return ports.SCMCommentResult{}, fmt.Errorf("%w: comment provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(request.PR.Repo.Provider)
	if err != nil {
		return ports.SCMCommentResult{}, err
	}
	commenter, ok := p.(ports.SCMCommenter)
	if !ok {
		return ports.SCMCommentResult{}, fmt.Errorf("%w: comments for provider %q", ports.ErrSCMUnsupported, request.PR.Repo.Provider)
	}
	return commenter.PostComment(ctx, request)
}

func (m *Provider) attachmentStore(name string) (ports.SCMAttachmentStore, error) {
	if m == nil {
		return nil, fmt.Errorf("%w: attachment provider is unavailable", ports.ErrSCMUnsupported)
	}
	p, err := m.resolve(name)
	if err != nil {
		return nil, err
	}
	store, ok := p.(ports.SCMAttachmentStore)
	if !ok {
		return nil, fmt.Errorf("%w: attachments for provider %q", ports.ErrSCMUnsupported, name)
	}
	return store, nil
}

func (m *Provider) AttachmentAccess(ctx context.Context, pr ports.SCMPRRef) (ports.SCMAttachmentAccess, error) {
	store, err := m.attachmentStore(pr.Repo.Provider)
	if err != nil {
		return ports.SCMAttachmentAccess{}, err
	}
	return store.AttachmentAccess(ctx, pr)
}

func (m *Provider) StoreAttachments(ctx context.Context, batch ports.SCMAttachmentBatch) (ports.SCMStoredAttachments, error) {
	store, err := m.attachmentStore(batch.PR.Repo.Provider)
	if err != nil {
		return ports.SCMStoredAttachments{}, err
	}
	return store.StoreAttachments(ctx, batch)
}

func (m *Provider) DiscardAttachments(ctx context.Context, pr ports.SCMPRRef, handle string) error {
	store, err := m.attachmentStore(pr.Repo.Provider)
	if err != nil {
		return err
	}
	return store.DiscardAttachments(ctx, pr, handle)
}

type branchPRLister interface {
	ListOpenPRsByBranch(ctx context.Context, repo ports.SCMRepo, branch string) ([]ports.SCMPRObservation, error)
}

type credentialChecker interface {
	SCMCredentialsAvailable(ctx context.Context) (bool, error)
}

type identityResolver interface {
	AuthenticatedIdentity(ctx context.Context) (ports.SCMIdentity, error)
}

type hostScopedIdentityResolver interface {
	AuthenticatedIdentityForHost(ctx context.Context, host string) (ports.SCMIdentity, error)
}

func (m *Provider) SCMCredentialsAvailable(ctx context.Context) (bool, error) {
	var firstErr error
	for _, np := range m.ordered {
		cc, ok := np.Provider.(credentialChecker)
		if !ok {
			continue
		}
		avail, err := cc.SCMCredentialsAvailable(ctx)
		if avail {
			return true, nil
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return false, firstErr
}

type indexedRef struct {
	idx int
	ref ports.SCMPRRef
}

func repoFullNameFromRef(repo ports.SCMRepo) string {
	if repo.Repo != "" {
		return repo.Repo
	}
	return repo.Owner + "/" + repo.Name
}

func (m *Provider) AuthenticatedIdentityForProvider(ctx context.Context, provider, host string) (ports.SCMIdentity, error) {
	p, err := m.resolve(provider)
	if err != nil {
		return ports.SCMIdentity{}, err
	}
	if hs, ok := p.(hostScopedIdentityResolver); ok {
		return hs.AuthenticatedIdentityForHost(ctx, host)
	}
	resolver, ok := p.(identityResolver)
	if !ok {
		return ports.SCMIdentity{}, fmt.Errorf("scm multi: provider %q does not implement AuthenticatedIdentity", provider)
	}
	return resolver.AuthenticatedIdentity(ctx)
}
