package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type attachmentTestStore struct {
	Store
	a           domain.Attachment
	prepares    int
	finalizeErr error
}

type materializeTestStore struct {
	attachmentTestStore
	request                     domain.WorkerRequest
	createErr, retainErr        error
	leased, retained, cancelled bool
	acknowledged                bool
}

func (s *materializeTestStore) LeaseAttachments(context.Context, domain.Principal, string, string, []string) ([]attachments.Metadata, error) {
	s.leased = true
	return []attachments.Metadata{s.a.Metadata}, nil
}
func (s *materializeTestStore) RetainAttachments(_ context.Context, _ domain.Principal, _, _ string, _ []string, workerID string, epoch int64) error {
	if !s.acknowledged || workerID != "worker" || epoch != 7 {
		panic("promotion before valid acknowledgement")
	}
	if s.retainErr != nil {
		return s.retainErr
	}
	s.retained = true
	return nil
}
func (s *materializeTestStore) ResumeSession(context.Context, domain.Principal, string, string) (domain.SandboxLifecycle, error) {
	return domain.SandboxLifecycle{}, nil
}
func (s *materializeTestStore) CreateWorkspaceRequest(_ context.Context, _ domain.Principal, _, _, kind string, _ json.RawMessage, _ time.Duration) (domain.WorkerRequest, error) {
	if !s.leased || s.retained || kind != "attachments.materialize" {
		panic("invalid attachment preparation order")
	}
	return domain.WorkerRequest{ID: uuid.NewString()}, s.createErr
}
func (s *materializeTestStore) GetWorkspaceRequest(context.Context, domain.Principal, string, string, string) (domain.WorkerRequest, error) {
	s.acknowledged = s.request.Status == "succeeded"
	return s.request, nil
}
func (s *materializeTestStore) CancelWorkspaceRequest(context.Context, domain.Principal, string, string, string) error {
	s.cancelled = true
	return nil
}

func TestAttachmentMaterializationRetainsOnlyAfterCurrentWorkerAck(t *testing.T) {
	a := attachments.Metadata{ID: uuid.NewString(), MIMEType: "image/png", Status: "ready"}
	valid, err := json.Marshal(map[string]any{"paths": []string{worker.AttachmentPath(a)}, "workerId": "worker", "epoch": 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name                 string
		request              domain.WorkerRequest
		createErr, retainErr error
		cancel               bool
		timeout              time.Duration
		code                 int
		retained             bool
	}{
		{name: "acknowledged", request: domain.WorkerRequest{Status: "succeeded", Response: valid}, code: 200, retained: true},
		{name: "request rejected", createErr: postgres.ErrWorkerUnavailable, code: 409},
		{name: "worker failure", request: domain.WorkerRequest{Status: "failed", ErrorCode: "DOWNLOAD_FAILED"}, code: 422},
		{name: "worker timeout", timeout: time.Millisecond, code: 504},
		{name: "cancelled client", cancel: true},
		{name: "missing paths", request: domain.WorkerRequest{Status: "succeeded", Response: json.RawMessage(`{"paths":[]}`)}, code: 502},
		{name: "invalid path", request: domain.WorkerRequest{Status: "succeeded", Response: json.RawMessage(`{"paths":["/wrong"],"workerId":"worker","epoch":7}`)}, code: 502},
		{name: "stale worker", request: domain.WorkerRequest{Status: "succeeded", Response: valid}, retainErr: postgres.ErrStaleWorker, code: 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &materializeTestStore{attachmentTestStore: attachmentTestStore{a: domain.Attachment{Metadata: a}}, request: tc.request, createErr: tc.createErr, retainErr: tc.retainErr}
			server := workspaceHandlerServer(store)
			server.attachmentStorage = &attachmentCountingStorage{}
			if tc.timeout != 0 {
				server.workerRequestTimeout = tc.timeout
			}
			request := workspaceHandlerRequest(t, "")
			body, err := json.Marshal(map[string]any{"attachmentIds": []string{a.ID}})
			if err != nil {
				t.Fatal(err)
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
			if tc.cancel {
				ctx, cancel := context.WithCancel(request.Context())
				cancel()
				request = request.WithContext(ctx)
			}
			recorder := httptest.NewRecorder()
			server.materializeAttachments(recorder, request)
			if tc.code != 0 && recorder.Code != tc.code {
				t.Fatalf("status=%d, body=%s", recorder.Code, recorder.Body.String())
			}
			if !store.leased || store.retained != tc.retained {
				t.Fatalf("leased=%v retained=%v", store.leased, store.retained)
			}
			if (tc.cancel || tc.timeout != 0) && !store.cancelled {
				t.Fatal("request not cancelled")
			}
		})
	}
}

type attachmentCountingStorage struct {
	attachments.Storage
	writes int
}

func (s *attachmentCountingStorage) PutVerified(context.Context, string, attachments.Metadata, []byte) error {
	s.writes++
	return nil
}

type deletedAttachmentTestStore struct{ attachmentTestStore }

func (s *deletedAttachmentTestStore) FinalizeAttachment(context.Context, domain.Principal, string, string, func(context.Context, domain.Attachment) error) (domain.Attachment, error) {
	// The earlier metadata read succeeded, then cleanup won the row lock.
	return domain.Attachment{}, postgres.ErrNotFound
}
func TestAttachmentCompletionDoesNotWriteAfterMetadataDeletion(t *testing.T) {
	store := &deletedAttachmentTestStore{attachmentTestStore: attachmentTestStore{a: domain.Attachment{Metadata: attachments.Metadata{ID: uuid.NewString(), Status: "pending"}, OrgID: workspaceTestOrgID, CreatorID: "user-1"}}}
	storage := &attachmentCountingStorage{}
	server := workspaceHandlerServer(store)
	server.attachmentStorage = storage
	request := workspaceHandlerRequest(t, "")
	chi.RouteContext(request.Context()).URLParams.Add("attachmentId", store.a.ID)
	recorder := httptest.NewRecorder()
	server.completeAttachment(recorder, request)
	if recorder.Code != 404 || storage.writes != 0 {
		t.Fatalf("status=%d writes=%d body=%s", recorder.Code, storage.writes, recorder.Body.String())
	}
}

func (s *attachmentTestStore) PrepareAttachment(ctx context.Context, p domain.Principal, org, key string, input domain.PrepareAttachment, issueUpload func(context.Context, domain.Attachment) (time.Time, error)) (domain.Attachment, error) {
	s.prepares++
	if s.a.ID == "" {
		s.a = domain.Attachment{Metadata: input.Metadata, OrgID: org, ProjectID: input.ProjectID, CreatorID: p.UserID}
		s.a.ID = uuid.NewString()
		s.a.Status = "pending"
	}
	if s.a.Status != "expired" {
		if _, err := issueUpload(ctx, s.a); err != nil {
			return s.a, err
		}
	}
	return s.a, nil
}
func (s *attachmentTestStore) GetAttachment(_ context.Context, p domain.Principal, org, id string) (domain.Attachment, error) {
	if s.a.OrgID != org || s.a.ID != id {
		return domain.Attachment{}, postgres.ErrNotFound
	}
	return s.a, nil
}
func (s *attachmentTestStore) FinalizeAttachment(ctx context.Context, p domain.Principal, org, id string, finalize func(context.Context, domain.Attachment) error) (domain.Attachment, error) {
	a, err := s.GetAttachment(ctx, p, org, id)
	if err != nil {
		return a, err
	}
	if a.CreatorID != p.UserID {
		return a, postgres.ErrForbidden
	}
	if a.Status != "ready" {
		if err := finalize(ctx, a); err != nil {
			return a, err
		}
		if s.finalizeErr != nil {
			return a, s.finalizeErr
		}
		s.a.Status = "ready"
	}
	return s.a, nil
}
func (s *attachmentTestStore) WorkerAttachments(context.Context, string, string, string, int64) ([]attachments.Metadata, error) {
	return []attachments.Metadata{s.a.Metadata}, nil
}
func (s *attachmentTestStore) LeaseAttachments(context.Context, domain.Principal, string, string, []string) ([]attachments.Metadata, error) {
	return []attachments.Metadata{s.a.Metadata}, nil
}
func (s *attachmentTestStore) RetainAttachments(context.Context, domain.Principal, string, string, []string, string, int64) error {
	return nil
}
func TestAttachmentUploadHTTPCompletionRetryAndPreview(t *testing.T) {
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	data := b.Bytes()
	sum := sha256.Sum256(data)
	storage, err := attachments.NewFilesystem("test", t.TempDir(), bytes.Repeat([]byte{8}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer storage.Close()
	store := &attachmentTestStore{}
	s := &Server{store: store, attachmentStorage: storage}
	org, project, user := uuid.NewString(), uuid.NewString(), uuid.NewString()
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: user})))
		})
	})
	router.Handle(attachments.StorageRoute+"*", storage)
	router.Post("/orgs/{orgId}/attachments", s.prepareAttachment)
	router.Post("/orgs/{orgId}/attachments/{attachmentId}/complete", s.completeAttachment)
	router.Post("/orgs/{orgId}/attachments/{attachmentId}/read-grant", s.attachmentReadGrant)
	server := httptest.NewServer(router)
	defer server.Close()
	post := func(path string, input any, out any) int {
		t.Helper()
		body, _ := json.Marshal(input)
		r, err := http.NewRequest("POST", server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Idempotency-Key", "selection")
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		if out != nil {
			if err := json.NewDecoder(response.Body).Decode(out); err != nil {
				t.Fatal(err)
			}
		}
		return response.StatusCode
	}
	var prepared struct {
		Attachment domain.Attachment       `json:"attachment"`
		Upload     attachments.UploadGrant `json:"upload"`
	}
	input := domain.PrepareAttachment{ProjectID: project, Metadata: attachments.Metadata{Filename: "image.png", Size: int64(len(data)), MIMEType: "image/png", SHA256: hex.EncodeToString(sum[:])}}
	if code := post("/orgs/"+org+"/attachments", input, &prepared); code != 201 {
		t.Fatal("prepare", code)
	}
	complete := "/orgs/" + org + "/attachments/" + prepared.Attachment.ID + "/complete"
	if code := post(complete, nil, nil); code != 409 {
		t.Fatal("completed missing bytes", code)
	}
	var upload bytes.Buffer
	form := multipart.NewWriter(&upload)
	part, _ := form.CreateFormFile("file", "image.png")
	part.Write(data)
	form.Close()
	response, err := http.Post(server.URL+prepared.Upload.URL, form.FormDataContentType(), &upload)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 204 {
		t.Fatal("direct upload", response.StatusCode)
	}
	store.finalizeErr = postgres.ErrConflict
	if code := post(complete, nil, nil); code != 409 {
		t.Fatal("failed readiness commit", code)
	}
	object, err := storage.Open(context.Background(), uploadKey(prepared.Attachment.ID))
	if err != nil {
		t.Fatal("readiness failure removed retry upload", err)
	}
	object.Close()
	store.finalizeErr = nil
	if code := post(complete, nil, nil); code != 200 {
		t.Fatal("complete", code)
	}
	if code := post(complete, nil, nil); code != 200 {
		t.Fatal("lost acknowledgement retry", code)
	}
	var grant attachments.ReadGrant
	if code := post("/orgs/"+org+"/attachments/"+prepared.Attachment.ID+"/read-grant", nil, &grant); code != 200 {
		t.Fatal("read", code)
	}
	response, err = http.Get(server.URL + grant.URL)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if !bytes.Equal(data, got) {
		t.Fatal("preview bytes differ")
	}
	if code := post("/orgs/"+uuid.NewString()+"/attachments/"+prepared.Attachment.ID+"/read-grant", nil, nil); code != 404 {
		t.Fatal("foreign org", code)
	}
	input.MIMEType = "image/svg+xml"
	if code := post("/orgs/"+org+"/attachments", input, nil); code != 422 {
		t.Fatal("SVG accepted", code)
	}
}
