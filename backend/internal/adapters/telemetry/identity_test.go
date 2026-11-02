package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func newTestSink(t *testing.T, dir string, requests chan map[string]any, hits *atomic.Int32) *PostHogSink {
	t.Helper()
	sink, err := NewPostHogSink(dir, "phc_test", "https://us.i.posthog.com", "1.2.3", "", roundTripClient(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		if hits != nil {
			hits.Add(1)
		}
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if requests != nil {
			requests <- body
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"status":1}`))}, nil
	}), nil)
	if err != nil {
		t.Fatalf("NewPostHogSink: %v", err)
	}
	return sink
}

func TestIdentityStampsEveryEventAndSetsPersonOnce(t *testing.T) {
	sink := newTestSink(t, t.TempDir(), nil, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	stableInstallID := sink.Identity().InstallID()

	anon := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	for _, k := range []string{"github_actor", "ao_cloud_user_id", "$set"} {
		if _, ok := anon[k]; ok {
			t.Fatalf("anonymous event carries %q", k)
		}
	}
	if anon["$process_person_profile"] != false {
		t.Fatalf("anonymous event is identified: %v", anon["$process_person_profile"])
	}
	if anon["install_id"] != stableInstallID {
		t.Fatal("anonymous event missing installation ID")
	}

	sink.Identity().SetGitHubLogin("octocat")
	if err := sink.Identity().SetCloudUserID(context.Background(), "user_01HABC"); err != nil {
		t.Fatalf("valid WorkOS id rejected: %v", err)
	}
	first := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	if first["github_actor"] != "octocat" || first["ao_cloud_user_id"] != "user_01HABC" {
		t.Fatalf("identity not on event: %#v", first)
	}
	if first["install_id"] != stableInstallID {
		t.Fatal("signed-in event changed installation ID")
	}
	if first["$process_person_profile"] != true {
		t.Fatal("signed-in event must be linked to the person")
	}
	set, _ := first["$set"].(map[string]any)
	if set["github_login"] != "octocat" || set["ao_cloud_user_id"] != "user_01HABC" {
		t.Fatalf("$set = %#v", set)
	}
	if _, ok := set["email"]; ok {
		t.Fatal("daemon must never $set email")
	}
	// The key advances only after the sender receives a successful response.
	sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})

	second := sink.properties(ports.TelemetryEvent{Name: "ao.http.5xx", Source: "httpd"})
	if second["github_actor"] != "octocat" || second["ao_cloud_user_id"] != "user_01HABC" || second["$process_person_profile"] != true {
		t.Fatalf("identity missing on later event: %#v", second)
	}
	if _, ok := second["$set"]; ok {
		t.Fatal("$set resent for unchanged identity")
	}
	for _, key := range []string{"app_version", "ao_version"} {
		if second[key] != "1.2.3" {
			t.Fatalf("%s = %v, want 1.2.3", key, second[key])
		}
	}

	// Sign-out drops the user but the person set is re-evaluated, not stale.
	_ = sink.Identity().SetCloudUserID(context.Background(), "")
	out := sink.properties(ports.TelemetryEvent{Name: "ao.http.5xx", Source: "httpd"})
	if _, ok := out["ao_cloud_user_id"]; ok {
		t.Fatal("ao_cloud_user_id survived sign-out")
	}
	if out["install_id"] != stableInstallID || sink.Identity().InstallID() != stableInstallID {
		t.Fatal("sign-out changed installation ID")
	}
}

func TestPersonPropertiesAreRetriedAfterARejectedSend(t *testing.T) {
	var sent []map[string]any
	sink, err := NewPostHogSink(t.TempDir(), "phc_test", "https://us.i.posthog.com", "", "", roundTripClient(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		var body map[string]any
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			return nil, err
		}
		sent = append(sent, body)
		status := http.StatusOK
		if len(sent) == 1 {
			status = http.StatusBadRequest
		}
		return &http.Response{StatusCode: status, Body: http.NoBody}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close(context.Background()) }()
	if err := sink.Identity().SetCloudUserID(context.Background(), "user_A"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	}
	if len(sent) != 3 {
		t.Fatalf("sends = %d, want 3", len(sent))
	}
	for index, wantSet := range []bool{true, true, false} {
		props := sent[index]["properties"].(map[string]any)
		_, hasSet := props["$set"]
		if hasSet != wantSet {
			t.Fatalf("send %d has $set = %t, want %t", index, hasSet, wantSet)
		}
	}
}

func TestIdentityRejectsMalformedValues(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if ident.SetCloudUserID(context.Background(), "user@example.com") == nil || ident.SetCloudUserID(context.Background(), strings.Repeat("a", 65)) == nil {
		t.Fatal("email-shaped or oversized cloud user id accepted")
	}
	ident.SetGitHubLogin("not a login!")
	if got := ident.Snapshot(); got.GitHubLogin != "" || got.CloudUserID != "" {
		t.Fatalf("malformed values stored: %#v", got)
	}
}

func TestOptOutStopsExportAndHidesIdentity(t *testing.T) {
	dir := t.TempDir()
	var hits atomic.Int32
	sink := newTestSink(t, dir, nil, &hits)
	sink.Identity().SetGitHubLogin("octocat")
	_ = sink.Identity().SetCloudUserID(context.Background(), "user_01HABC")

	if err := os.WriteFile(filepath.Join(dir, OptOutFile), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	snap := sink.Identity().Snapshot()
	if !snap.OptedOut || snap.CloudUserID != "" || snap.GitHubLogin != "" {
		t.Fatalf("opted-out snapshot leaks identity: %#v", snap)
	}
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon", OccurredAt: time.Now()})
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 0 {
		t.Fatalf("opted-out install exported %d events", hits.Load())
	}

	// Opting back in resumes export.
	if err := os.Remove(filepath.Join(dir, OptOutFile)); err != nil {
		t.Fatal(err)
	}
	sink = newTestSink(t, dir, nil, &hits)
	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon", OccurredAt: time.Now()})
	_ = sink.Close(context.Background())
	if hits.Load() != 1 {
		t.Fatalf("export did not resume after opt-in: %d", hits.Load())
	}
}

func TestFreshInstallOnlyBeforeFirstExportedEvent(t *testing.T) {
	dir := t.TempDir()
	ident, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !ident.FreshInstall() {
		t.Fatal("new data dir should be a fresh install")
	}
	// An exported event writes the tenure file; the next start is not fresh.
	sink := newTestSink(t, dir, nil, nil)
	sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started"})
	_ = sink.Close(context.Background())
	again, _ := NewIdentity(dir)
	if again.FreshInstall() {
		t.Fatal("second start reported a fresh install")
	}

	// An old install ID with no tenure file (upgrade) is not a new install.
	old := t.TempDir()
	path := filepath.Join(old, "telemetry_install_id")
	if err := os.WriteFile(path, []byte("ins_x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-72 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatal(err)
	}
	upgraded, _ := NewIdentity(old)
	if upgraded.FreshInstall() {
		t.Fatal("upgrade counted as a new install")
	}
}

func TestInstallIDCreationRecoversEmptyFileAndConcurrentReaders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, installIDFile)
	if err := os.WriteFile(path, []byte(" \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want, err := loadOrCreateInstallID(dir)
	if err != nil || !strings.HasPrefix(want, "ins_") {
		t.Fatalf("recovered install id = %q, %v", want, err)
	}
	if stored, err := readTrimmed(path); err != nil || stored != want {
		t.Fatalf("stored install id = %q, %v, want %q", stored, err, want)
	}

	type result struct {
		id  string
		err error
	}
	for _, legacyEmpty := range []bool{false, true} {
		concurrentDir := t.TempDir()
		if legacyEmpty {
			if err := os.WriteFile(filepath.Join(concurrentDir, installIDFile), []byte("\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		results := make(chan result, 16)
		for range cap(results) {
			go func() {
				id, err := loadOrCreateInstallID(concurrentDir)
				results <- result{id, err}
			}()
		}
		for range cap(results) {
			got := <-results
			if got.err != nil || !strings.HasPrefix(got.id, "ins_") {
				t.Fatalf("concurrent install id = %q, %v", got.id, got.err)
			}
			if disk, err := readTrimmed(filepath.Join(concurrentDir, installIDFile)); err != nil || disk != got.id {
				t.Fatalf("published install id = %q, %v, caller saw %q", disk, err, got.id)
			}
		}
	}
}

func TestInstalledEventIsReservedOnceEvenWithoutAnExport(t *testing.T) {
	dir := t.TempDir()
	first, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := first.MarkInstalledEvent(context.Background())
	if err != nil || !marked {
		t.Fatalf("first install mark = %t, %v", marked, err)
	}
	// No sender processed the queued event before this restart.
	second, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.FreshInstall() {
		t.Fatal("restart must not emit the install event again")
	}
	marked, err = second.MarkInstalledEvent(context.Background())
	if err != nil || marked {
		t.Fatalf("second install mark = %t, %v", marked, err)
	}
}

func TestCanceledIdentityWritesLeaveStateUnchanged(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ident.SetCloudUserID(ctx, "user_A"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetCloudUserID error = %v, want canceled", err)
	}
	if got := ident.Snapshot().CloudUserID; got != "" {
		t.Fatalf("cloud user after cancellation = %q", got)
	}
	if marked, err := ident.MarkInstalledEvent(ctx); marked || !errors.Is(err, context.Canceled) {
		t.Fatalf("MarkInstalledEvent = %t, %v, want canceled", marked, err)
	}
	if marked, err := ident.MarkInstalledEvent(context.Background()); !marked || err != nil {
		t.Fatalf("MarkInstalledEvent after cancellation = %t, %v", marked, err)
	}
}

// Email is a person property set once by the signed-in desktop. No daemon event
// may carry it, so it must not be an allowlisted payload key anywhere.
func TestNoEmailInAnyPayloadAllowlist(t *testing.T) {
	for name, allowed := range remotePayloadAllowlist {
		for key := range allowed {
			if strings.Contains(strings.ToLower(key), "email") {
				t.Errorf("%s allowlists %q", name, key)
			}
		}
	}
}

// drainDistinct returns the distinct_id of each captured request, in order.
func drainDistinct(t *testing.T, requests chan map[string]any, n int) []string {
	t.Helper()
	out := make([]string, 0, n)
	for len(out) < n {
		select {
		case body := <-requests:
			id, _ := body["distinct_id"].(string)
			out = append(out, id)
		case <-time.After(2 * time.Second):
			t.Fatalf("got %d requests, want %d", len(out), n)
		}
	}
	return out
}

func TestAccountSwitchNeverReusesTheFirstAccountsInstallID(t *testing.T) {
	dir := t.TempDir()
	requests := make(chan map[string]any, 8)
	sink := newTestSink(t, dir, requests, nil)
	defer func() { _ = sink.Close(context.Background()) }()
	ident := sink.Identity()
	install0 := ident.InstallID()
	emit := func() {
		sink.send(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	}

	emit() // anonymous: the install ID
	_ = ident.SetCloudUserID(context.Background(), "user_A")
	emit() // signed in: the account, not the install ID that identify() merges into A
	_ = ident.SetCloudUserID(context.Background(), "")
	install1 := ident.AnonymousID()
	emit() // signed out: must not resolve to A
	_ = ident.SetCloudUserID(context.Background(), "user_B")
	emit()

	got := drainDistinct(t, requests, 4)
	if got[0] != install0 || got[1] != "user_A" || got[3] != "user_B" {
		t.Fatalf("distinct ids = %v", got)
	}
	if install1 == install0 || got[2] != install1 {
		t.Fatalf("signed-out events reused the install ID bound to A: %v (install0=%s install1=%s)", got, install0, install1)
	}
	// The desktop bootstrap and the phone read the same ID from disk.
	if disk, _ := loadOrCreateInstallID(dir); disk != install0 {
		t.Fatalf("install id on disk = %s, want %s", disk, install0)
	}
}

func TestInstallIDRetiredAcrossRestartAndDirectSwitch(t *testing.T) {
	dir := t.TempDir()
	first, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := first.InstallID()
	_ = first.SetCloudUserID(context.Background(), "user_A")
	if first.InstallID() != original {
		t.Fatal("first sign-in must keep the install ID so identify() aliases it")
	}

	// Daemon restarts, A signed out while the app was closed, then the app pushes
	// a signed-out state.
	second, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.SetCloudUserID(context.Background(), "user_A")
	if second.InstallID() != original {
		t.Fatal("same account replayed after restart must not rotate")
	}
	summary := filepath.Join(dir, "telemetry", "cli-usage-daily.json")
	if err := os.MkdirAll(filepath.Dir(summary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(summary, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = second.SetCloudUserID(context.Background(), "")
	rotated := second.AnonymousID()
	if rotated == original || second.InstallID() != original {
		t.Fatal("sign-out did not retire the anonymous ID")
	}
	if _, err := os.Stat(summary); !os.IsNotExist(err) {
		t.Fatal("install reset left the CLI usage summary on disk")
	}

	third, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	_ = third.SetCloudUserID(context.Background(), "")
	if third.AnonymousID() != rotated {
		t.Fatal("repeated signed-out push must not keep rotating")
	}
	_ = third.SetCloudUserID(context.Background(), "user_B")
	_ = third.SetCloudUserID(context.Background(), "user_C") // account switch without a signed-out step
	if third.AnonymousID() == rotated || third.InstallID() != original {
		t.Fatal("direct account switch did not retire the install ID")
	}
}

func TestBoundInstallKeepsAnonymousDistinctIDAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	first, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetCloudUserID(context.Background(), "user_A"); err != nil {
		t.Fatal(err)
	}

	// Startup events can export before the desktop restores the Cloud user.
	second, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := second.Snapshot().DistinctID()
	if want == first.InstallID() {
		t.Fatal("bound install reused the install ID before Cloud identity was restored")
	}
	third, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := third.Snapshot().DistinctID(); got != want {
		t.Fatalf("anonymous distinct ID changed across restart: got %q, want %q", got, want)
	}
}

func TestOptOutSinkKeepsOptedOutEventsOutOfAggregateBuckets(t *testing.T) {
	rec := &syncRecordingSink{}
	agg := NewAggregatingSink(rec, []string{"ao.http.5xx"}, time.Hour)
	optedOut := true
	gated := NewOptOutSink(agg, func() bool { return optedOut })

	gated.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.http.5xx", Payload: map[string]any{"path": "/x"}})
	optedOut = false // opt back in before the minute flush
	agg.flush(context.Background())

	if got := len(rec.snapshot()); got != 0 {
		t.Fatalf("opted-out event was exported after opt-in: %d event(s)", got)
	}
}

func TestSendRechecksOptOutBeforeRetry(t *testing.T) {
	dir := t.TempDir()
	attempts := 0
	sink, err := NewPostHogSink(dir, "phc_test", "https://example.test", "", "", roundTripClient(func(_ *http.Request) (*http.Response, error) {
		attempts++
		// The user opts out while the first request is in flight; it then fails
		// transiently, so the retry would otherwise go out.
		if err := os.WriteFile(filepath.Join(dir, OptOutFile), []byte("1\n"), 0o600); err != nil {
			t.Error(err)
		}
		return &http.Response{StatusCode: http.StatusInternalServerError, Body: http.NoBody}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close(context.Background()) }()

	sink.send(context.Background(), activeEvent())
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1 (retry after opt-out must not be sent)", attempts)
	}
}

func TestQueuedEventStopsAtSendGateAfterOptOut(t *testing.T) {
	dir := t.TempDir()
	started := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int32
	sink, err := NewPostHogSink(dir, "phc_test", "https://example.test", "", "", roundTripClient(func(_ *http.Request) (*http.Response, error) {
		if attempts.Add(1) == 1 {
			close(started)
			<-release
		}
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		_ = sink.Close(context.Background())
	}()
	sink.Emit(context.Background(), activeEvent())
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first send did not start")
	}
	sink.Emit(context.Background(), activeEvent())
	if err := os.WriteFile(filepath.Join(dir, OptOutFile), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := sink.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("HTTP attempts = %d, want 1 after opt-out", got)
	}
}

func TestRotationFailureKeepsTheBindingAndSuppressesExport(t *testing.T) {
	dir := t.TempDir()
	ident, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	original := ident.InstallID()
	if err := ident.SetCloudUserID(context.Background(), "user_A"); err != nil {
		t.Fatal(err)
	}

	// Sign-out while the install ID cannot be replaced.
	realWrite := ident.writeFile
	ident.writeFile = func(path string, data []byte) error {
		if filepath.Base(path) == anonymousIDFile {
			return os.ErrPermission
		}
		return realWrite(path, data)
	}
	if err := ident.SetCloudUserID(context.Background(), ""); err == nil {
		t.Fatal("sign-out reported success although the install ID was not retired")
	}
	if ident.InstallID() != original {
		t.Fatal("install ID changed despite the failed write")
	}
	if got, _ := readTrimmed(filepath.Join(dir, boundUserFile)); got != "user_A" {
		t.Fatalf("binding = %q, want user_A kept so the retirement can be retried", got)
	}
	if !ident.ExportSuppressed() {
		t.Fatal("export must stay suppressed while the retired ID is still in use")
	}

	if snap := ident.Snapshot(); !snap.OptedOut || snap.CloudUserID != "" || snap.GitHubLogin != "" {
		t.Fatalf("snapshot must suppress the phone after a failed rotation: %#v", snap)
	}

	// The retry succeeds once the filesystem recovers.
	ident.writeFile = realWrite
	if err := ident.SetCloudUserID(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if ident.Snapshot().OptedOut {
		t.Fatal("snapshot still suppressed after recovery")
	}
	if ident.InstallID() != original || ident.AnonymousID() == original || ident.ExportSuppressed() {
		t.Fatalf("retry did not retire the anonymous ID or lift suppression (id=%s suppressed=%v)", ident.AnonymousID(), ident.ExportSuppressed())
	}
	if _, err := os.Stat(filepath.Join(dir, boundUserFile)); !os.IsNotExist(err) {
		t.Fatalf("binding survived a completed sign-out: %v", err)
	}
}

func TestBindingWriteAndClearFailuresFailClosed(t *testing.T) {
	ident, err := NewIdentity(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	realWrite, realRemove := ident.writeFile, ident.removeFile

	// First sign-in: the binding cannot be recorded, so nothing may export as A.
	ident.writeFile = func(string, []byte) error { return os.ErrPermission }
	if err := ident.SetCloudUserID(context.Background(), "user_A"); err == nil || !ident.ExportSuppressed() {
		t.Fatalf("unrecorded binding must fail closed (err=%v suppressed=%v)", err, ident.ExportSuppressed())
	}
	ident.writeFile = realWrite
	if err := ident.SetCloudUserID(context.Background(), "user_A"); err != nil || ident.ExportSuppressed() {
		t.Fatalf("retry should record the binding (err=%v suppressed=%v)", err, ident.ExportSuppressed())
	}

	// Sign-out where the ID is replaced but the binding cannot be cleared.
	ident.removeFile = func(string) error { return os.ErrPermission }
	if err := ident.SetCloudUserID(context.Background(), ""); err == nil || !ident.ExportSuppressed() {
		t.Fatalf("uncleared binding must fail closed (err=%v suppressed=%v)", err, ident.ExportSuppressed())
	}
	ident.removeFile = realRemove
	if err := ident.SetCloudUserID(context.Background(), ""); err != nil || ident.ExportSuppressed() {
		t.Fatalf("retry should clear the binding (err=%v suppressed=%v)", err, ident.ExportSuppressed())
	}
}

func TestWriteFileAtomicLeavesOldFileWhenRenameFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	// A non-empty directory in the way makes the final rename fail.
	if err := os.MkdirAll(filepath.Join(target, "x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(target, []byte("new\n")); err == nil {
		t.Fatal("rename over a directory should fail")
	}
	if _, err := os.Stat(target + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file left behind: %v", err)
	}
}

func TestSinkExportsNothingWhileIdentityIsUnpersisted(t *testing.T) {
	var hits atomic.Int32
	sink := newTestSink(t, t.TempDir(), nil, &hits)
	defer func() { _ = sink.Close(context.Background()) }()
	sink.Identity().writeFile = func(string, []byte) error { return os.ErrPermission }
	_ = sink.Identity().SetCloudUserID(context.Background(), "user_A")

	sink.Emit(context.Background(), ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	sink.send(context.Background(), activeEvent())
	if hits.Load() != 0 {
		t.Fatalf("exported %d request(s) with an unpersisted identity", hits.Load())
	}
}
