package worker

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
)

// scriptLeakCase is output a Setup or Cleanup step may print with a
// credential in it. secrets are the substrings that must never be published.
type scriptLeakCase struct {
	name    string
	text    string
	secrets []string
}

func scriptLeakCases() []scriptLeakCase {
	return append(earlierLeakCases(), stagingLeakCases()...)
}

func stagingLeakCases() []scriptLeakCase {
	var cases []scriptLeakCase
	for index, step := range stagingRetestSteps()[:len(stagingRetestSecrets)] {
		if secrets := stagingRetestSecrets[index]; len(secrets) > 0 {
			cases = append(cases, scriptLeakCase{"staging case " + strconv.Itoa(index+1), step, secrets})
		}
	}
	return cases
}

func earlierLeakCases() []scriptLeakCase {
	privateKey := "-----BEGIN RSA PRIV" + "ATE KEY-----\n" +
		"MIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7Leak14BodyA\n" +
		"VJTUt9Us8cKjMzEfYyjiWA4R4key=Leak14BodyB/M2bS1GB4t7NXp98C3SC6d==\n" +
		"-----END RSA PRIV" + "ATE KEY-----"
	return []scriptLeakCase{
		{"secrets manager", `{"SecretString":"{\"password\":\"hunter2xyz\"}"}`, []string{"hunter2xyz"}},
		{"secrets manager spaced", `"SecretString": "{\"password\": \"Leak01spc\"}",`, []string{"Leak01spc"}},
		{"double escaped", `{\"SecretString\":\"{\\\"password\\\":\\\"Leak02dbl\\\"}\"}`, []string{"Leak02dbl"}},
		{"double escaped, plain wrapper", `{"Data":"{\"Inner\":\"{\\\"password\\\":\\\"Leak02wrap\\\"}\"}"}`, []string{"Leak02wrap"}},
		{"escaped, plain wrapper", `{"Data":"{\"db_password\":\"Leak02esc\",\"user\":\"u\"}"}`, []string{"Leak02esc"}},
		{"single quoted", `{'password': 'Leak03sq'}`, []string{"Leak03sq"}},
		{"colon", `password: Leak04col`, []string{"Leak04col"}},
		{"env", `PASSWORD=Leak05env`, []string{"Leak05env"}},
		{"export", `export DB_PASSWORD="Leak06exp"`, []string{"Leak06exp"}},
		{"flag", `mysql --password Leak07flag -h db`, []string{"Leak07flag"}},
		{"flag quoted", `deploy --api-token "Leak07q" --verbose`, []string{"Leak07q"}},
		{"flag equals", `mysql --password=Leak08eq -h db`, []string{"Leak08eq"}},
		{"url", `DATABASE_URL=postgres://u:Leak09url@h/db`, []string{"Leak09url"}},
		{"bearer", `Authorization: Bearer Leak10tok`, []string{"Leak10tok"}},
		{"bearer curl", `curl -H 'Authorization: Bearer Leak10crl' https://api`, []string{"Leak10crl"}},
		{"bearer pair", `auth_header=Bearer Leak10pr`, []string{"Leak10pr"}},
		{"json token", `{ "token": "Leak11tok" }`, []string{"Leak11tok"}},
		{"json token spaced colon", `{ "token" : "Leak11spc" }`, []string{"Leak11spc"}},
		{"yaml quoted", `  password: "Leak12yml"`, []string{"Leak12yml"}},
		{"secrets manager multiple keys",
			`"SecretString": "{\"username\":\"admin\",\"password\":\"Leak13pw\",\"api_key\":\"Leak13key\",\"host\":\"db\"}",`,
			[]string{"Leak13pw", "Leak13key"}},
		{"secrets manager compact multiple keys",
			`{"SecretString":"{\"api_key\":\"Leak13ck\",\"password\":\"Leak13cp\"}","Name":"prod/db"}`,
			[]string{"Leak13ck", "Leak13cp"}},
		{"private key", privateKey, []string{"Leak14BodyA", "Leak14BodyB"}},
		{"escaped quote in value", `{"password":"Le\"ak15esc"}`, []string{"ak15esc"}},
		{"value ending in a backslash", `{"password":"Leak16bs\\","user":"u"}`, []string{"Leak16bs"}},
		{"single quoted with a blank", `DB_PASSWORD='Leak18 spaced'`, []string{"Leak18", "spaced"}},
		{"unterminated escaped", `{"SecretString":"{\"password\":\"Leak19open`, []string{"Leak19open"}},
		// What a worker from before the fix published: the control plane
		// masks the rest.
		{"already cut short", `{"SecretString":[REDACTED]password\":\"Leak20old\"}"}`, []string{"Leak20old"}},
		// Short flags of commands that take a password.
		{"docker login -p", `docker login -u ci -p Leak30QZX registry.example`, []string{"Leak30QZX"}},
		{"docker login -p first", `+ sudo docker login -p "Leak31QZX" -u ci registry.example`, []string{"Leak31QZX"}},
		{"mysql -p attached", `mysql -uroot -pLeak32QZX -h db`, []string{"Leak32QZX"}},
		{"mariadb -p attached quoted", `mariadb -u root '-pLeak33QZX' app`, []string{"Leak33QZX"}},
		{"mysql -p separate", `mysql -u root -p Leak34QZX -e 'select 1'`, []string{"Leak34QZX"}},
		{"sshpass -p", `sshpass -p Leak35QZX ssh -p 22 deploy@host`, []string{"Leak35QZX"}},
		{"sshpass -p attached", `sshpass -pLeak36QZX scp f host:`, []string{"Leak36QZX"}},
		{"redis-cli -a", `redis-cli -h cache -a Leak37QZX ping`, []string{"Leak37QZX"}},
		{"curl -u", `curl -sSf -u admin:Leak38QZX https://api.example`, []string{"Leak38QZX"}},
		{"curl -u attached", `curl -uadmin:Leak39QZX https://api.example`, []string{"Leak39QZX"}},
		{"curl --user", `curl --user "admin:Leak40QZX" https://api.example`, []string{"Leak40QZX"}},
		{"curl combined -u", `curl -fsSu admin:Leak41QZX https://api.example`, []string{"Leak41QZX"}},
		// URLs.
		{"token as a URL's user", `git clone https://Leak42tok0123456789abcdef@github.com/o/r.git`, []string{"Leak42tok"}},
		{"github token as a URL's user", `remote: https://` + "gh" + "p_" + `Leak43abcdefghijklmnopqrstuvwxyz0123@github.com/o/r`, []string{"Leak43"}},
		{"azure sas", `https://acct.blob.core.windows.net/c/b.tgz?sv=2022-11-02&se=2026-10-20&sp=r&sig=Leak44sig%2Fab%3D`, []string{"Leak44sig"}},
		{"sas sig first", `sig=Leak45sig%3D&sv=2022-11-02&sr=b`, []string{"Leak45sig"}},
		// A Kubernetes Secret, whatever its keys are named, in kubectl's
		// key order (data before kind) and with stringData after kind.
		{"kubernetes secret data", "apiVersion: v1\ndata:\n  dbhost: TGVhazQ2ZGF0YQ==\n  conn: TGVhazQ2Y29ubg==\nkind: Secret\nmetadata:\n  name: db\ntype: Opaque",
			[]string{"TGVhazQ2ZGF0YQ", "TGVhazQ2Y29ubg"}},
		{"kubernetes secret stringData", "---\nkind: Secret\napiVersion: v1\nstringData:\n  url: Leak47strdata\n  config: |\n    host: db\n    Leak47block\nmetadata:\n  name: x",
			[]string{"Leak47strdata", "Leak47block"}},
		{"kubernetes secret list", "items:\n- apiVersion: v1\n  data:\n    a: TGVhazQ4bGlzdA==\n  kind: Secret",
			[]string{"TGVhazQ4bGlzdA"}},
		// Argument lists.
		{"json argument list", `"Args": ["--token","Leak49arr","--verbose"]`, []string{"Leak49arr"}},
		{"json argument list spaced", `["deploy", "--password", "Leak50arr"]`, []string{"Leak50arr"}},
		{"assignment of flags", `ARGS=--password Leak51args`, []string{"Leak51args"}},
		{"bracketed flags", `usage seen: [--password Leak52brk]`, []string{"Leak52brk"}},
		// YAML block scalars.
		{"yaml literal block", "private_key: |\n  Leak53blockA\n  Leak53blockB\nnext: ok", []string{"Leak53blockA", "Leak53blockB"}},
		{"yaml folded block", "db:\n  password: >-\n    Leak54fold\n    Leak54fold2\n  host: db", []string{"Leak54fold"}},
		// Token shapes.
		{"hugging face", `HF said hf_Leak55abcdefghijklmnopqrstuvwxyz12`, []string{"Leak55"}},
		{"stripe restricted", `using rk_live_Leak56abcdefghijklmn`, []string{"Leak56"}},
		{"stripe secret", `using sk_live_Leak57abcdefghijklmn`, []string{"Leak57"}},
		// Values holding a separator or a quote.
		{"value with a semicolon", `DB_PASSWORD=Leak58;B58semi`, []string{"Leak58", "B58semi"}},
		{"value with a comma", `API_TOKEN=Leak59,B59comma`, []string{"Leak59", "B59comma"}},
		{"value with an ampersand", `client_secret=Leak60&B60amp`, []string{"Leak60", "B60amp"}},
		{"value with a quote", `PASSWORD=Leak61"B61quote`, []string{"Leak61", "B61quote"}},
		{"quoted value with separators", `PASSWORD="Leak62;a,b&c" next`, []string{"Leak62", "a,b&c"}},
		// Escapes between the name and the value.
		{"colour codes", "\x1b[1mNPM_AUTH\x1b[0m=\x1b[32mLeak63ansi\x1b[0m", []string{"Leak63ansi"}},
		{"colour code after the separator", "SIGNING_KEY:\x1b[33m Leak64ansi\x1b[0m", []string{"Leak64ansi"}},
		// Python strings.
		{"python unicode string", `{'password': u'Leak65py', 'user': u'x'}`, []string{"Leak65py"}},
		{"python bytes string", `{"api_key": b"Leak66py"}`, []string{"Leak66py"}},
	}
}

// stagingRetestSteps are the Setup (and Cleanup) steps of the staging
// re-test, verbatim: one per case, then all of them in one failing step.
func stagingRetestSteps() []string {
	steps := []string{
		`echo '{"SecretString":"{\"password\":\"FAKEVAL1\"}"}'`,
		`echo "docker login -p FAKEVAL2 registry.example.com || true"`,
		`echo "mysql -pFAKEVAL3"`,
		`echo "sshpass -p FAKEVAL4 ssh -p 22 host"`,
		`echo "curl -u admin:FAKEVAL5 https://x"`,
		`echo "https://` + "gh" + "p_" + `FAKEVAL6aaaaaaaaaaaaaaaaaaaaaaaaaaaa@github.com/o/r"`,
		`echo "https://acct.blob.core.windows.net/c?sv=1&sig=FAKEVAL7"`,
		`printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: demo\ndata:\n  foo: RkFLRVZBTDg=\n'`,
		`echo '["--token","FAKEVAL9"]'`,
		`printf 'private_key: |\n  FAKEVAL10-line-one\n  FAKEVAL10-line-two\nafter_block: visible\n'`,
		`echo hf_FAKEVAL11aaaaaaaaaaaaaaaaaaaaaaaa`,
		`echo "API_KEY=FAKEVAL12;next=1"`,
		`printf '\033[32mPASSWORD\033[0m=FAKEVAL13\n'`,
		`export MY_SERVICE_SECRET=FAKEVAL14; printf '%s\n' "$MY_SERVICE_SECRET"`,
		`set -x; true --password FAKEVAL15`,
		`echo "docker run -p 8080:80 nginx"`,
	}
	var combined []string
	for _, step := range steps {
		if !strings.HasPrefix(step, "set -x") {
			combined = append(combined, step)
		}
	}
	return append(steps, `echo "== summary step =="; `+strings.Join(combined, "; ")+`; set -x; true --password FAKEVAL15; echo "about to fail"; exit 3`)
}

// stagingRetestSecrets are what each staging re-test step must never publish.
var stagingRetestSecrets = [][]string{
	{"FAKEVAL1"}, {"FAKEVAL2"}, {"FAKEVAL3"}, {"FAKEVAL4"}, {"FAKEVAL5"}, {"FAKEVAL6"}, {"FAKEVAL7"}, {"RkFLRVZBTDg"},
	{"FAKEVAL9"}, {"FAKEVAL10"}, {"FAKEVAL11"}, {"FAKEVAL12"}, {"FAKEVAL13"}, {"FAKEVAL14"}, {"FAKEVAL15"}, nil,
}

// The staging re-test end to end: its steps as a Setup and a Cleanup publish
// none of their secrets on any path (each step's live and final command and
// output, sanitized again by the control plane, the failed run's summary,
// and Cleanup's steps with the failed step's repeated command line), and
// still show what is not secret.
func TestStagingRetestNeverPublishesASecret(t *testing.T) {
	published := publishedByRuns(t, stagingRetestSteps())
	for _, secrets := range stagingRetestSecrets {
		for _, secret := range secrets {
			if strings.Contains(published, secret) {
				t.Errorf("%q published", secret)
			}
		}
	}
	for _, kept := range []string{
		"sshpass -p [REDACTED] ssh -p 22 host", "docker run -p 8080:80 nginx", "API_KEY=[REDACTED];next=1",
		`echo "API_KEY=[REDACTED];next=1"`, "after_block: visible", "cleanup step 17: echo", "about to fail",
	} {
		if !strings.Contains(published, kept) {
			t.Errorf("%q is not shown", kept)
		}
	}
	if t.Failed() {
		t.Logf("published:\n%s", published)
	}
}

// assertNoLeak fails when published holds any of the case's secrets.
func assertNoLeak(t *testing.T, path string, test scriptLeakCase, published string) {
	t.Helper()
	for _, secret := range test.secrets {
		if strings.Contains(published, secret) {
			t.Errorf("%s: %s published %q from %q", test.name, path, published, test.text)
		}
	}
	if !strings.Contains(published, "[REDACTED]") {
		t.Errorf("%s: %s published %q without a redaction marker", test.name, path, published)
	}
}

// No credential in a step's output or command leaves the sandbox or is kept
// by the control plane, through any of the production paths: the worker's
// final and live step output, the control plane's sanitizing of a worker's
// event, the Cleanup hook's output parsed into steps (with and without
// markers), and the script redaction the Setup summary and startup error get.
func TestScriptOutputNeverLeaksASecret(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor(nil, nil)
	for _, test := range scriptLeakCases() {
		paths := map[string]string{"RedactScriptOutput": RedactScriptOutput(test.text)}
		paths["scriptStepOutput"], _ = redactor.scriptStepOutput(test.text+"\n", false)
		paths["scriptStepOutput, unfinished line"], _ = redactor.scriptStepOutput(test.text, false)
		paths["liveStepOutput"], _ = redactor.liveStepOutput("started\n"+test.text+"\n", false)
		paths["SanitizeScriptStepEvent output"] = SanitizeScriptStepEvent(ScriptStepEvent{Output: test.text}).Output
		if !strings.Contains(test.text, "\n") {
			paths["SanitizeScriptStepEvent command"] = SanitizeScriptStepEvent(ScriptStepEvent{Command: "echo " + test.text}).Command
			command, _ := scriptStepCommand("echo "+test.text, redactor)
			paths["scriptStepCommand"] = command
		}
		// The worker's event, sanitized again by the control plane.
		workerOutput, _ := redactor.scriptStepOutput(test.text+"\n", false)
		paths["worker then control plane"] = SanitizeScriptStepEvent(ScriptStepEvent{Output: workerOutput}).Output

		nonce := NewScriptMarkerNonce()
		marked := scriptMarkerPrefix + nonce + " 1 1 start 1700000000000 / echo\n" + test.text + "\n" +
			scriptMarkerPrefix + nonce + " 1 1 end 0 5\n"
		events := CleanupStepEvents(marked, CleanupRun{RunID: "r", Nonce: nonce, StartedAt: time.Now()})
		if len(events) != 1 {
			t.Fatalf("%s: cleanup events = %+v", test.name, events)
		}
		paths["CleanupStepEvents"] = events[0].Output
		events = CleanupStepEvents("cleaning\n"+test.text+"\n", CleanupRun{RunID: "r", ExitCode: 1, StartedAt: time.Now()})
		if len(events) != 1 {
			t.Fatalf("%s: cleanup events without markers = %+v", test.name, events)
		}
		paths["CleanupStepEvents without markers"] = events[0].Output

		for path, published := range paths {
			assertNoLeak(t, path, test, published)
		}
		encoded, _ := json.Marshal(paths)
		for _, secret := range test.secrets {
			if strings.Contains(string(encoded), secret) {
				t.Errorf("%s: a path published %q", test.name, secret)
			}
		}
	}
}

// End to end: a Setup step that prints every case publishes none of them in
// its live or final events, and the failed run's summary (what the startup
// screen shows) holds none, as the run returns it and as the worker redacts it
// again before sending it. A secret environment variable's value is masked
// even where nothing names it: printed bare, traced by set -x, and as the
// unfinished last line.
func TestSetupRunNeverPublishesASecret(t *testing.T) {
	t.Setenv("MY_SERVICE_SECRET", "LeakEnvSvc01x")
	t.Setenv("REGISTRY_PASSWORD", "LeakEnvReg02x")
	cases := append(scriptLeakCases(),
		scriptLeakCase{name: "env value, bare", secrets: []string{"LeakEnvSvc01x"}},
		scriptLeakCase{name: "env value, set -x", secrets: []string{"LeakEnvReg02x"}},
	)
	var printed strings.Builder
	for _, test := range cases {
		printed.WriteString(test.text + "\n")
	}
	file := filepath.Join(t.TempDir(), "output.txt")
	if err := os.WriteFile(file, []byte(printed.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	summary, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 10 * time.Millisecond},
		[]string{"cat " + file + `; printf '%s\n' "$MY_SERVICE_SECRET"; ` +
			`(set -x; : docker login -u ci -p "$REGISTRY_PASSWORD" registry.example) 2>&1; ` +
			`sleep 0.1; printf 'last %s' "$MY_SERVICE_SECRET"; exit 1`})
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	// JSON escapes the published text's quotes and backslashes, not the
	// secrets, which are letters and digits.
	published := string(encoded) + "\n" + summary.Output + "\n" + summary.Command + "\n" +
		RedactScriptOutput(summary.Output) + "\n" + RedactScriptOutput(summary.Message())
	for _, test := range cases {
		for _, secret := range test.secrets {
			if strings.Contains(published, secret) {
				t.Errorf("%s: %q published", test.name, secret)
			}
		}
	}
}

// leaksPart reports whether published holds any 4-byte piece of secret.
func leaksPart(published, secret string) bool {
	for start := 0; start+4 <= len(secret); start++ {
		if strings.Contains(published, secret[start:start+4]) {
			return true
		}
	}
	return false
}

// A secret where the output is cut is never published in part: at the 16 KiB
// tail an event carries (redacted before it is cut), where the raw tail kept
// for a step begins, and where the Setup summary's and the Cleanup hook's
// tails begin.
func TestScriptOutputCutNeverSplitsASecret(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor(nil, nil)
	const secret = "Leak21CutBoundary"
	line := "DB_PASSWORD=" + secret
	for shift := 0; shift <= len(line); shift++ {
		// The secret's line straddles the 16 KiB tail an event carries, on
		// its own line and inside one long line.
		after := ScriptStepOutputLimit - len(line) + shift
		for _, text := range []string{
			strings.Repeat("filler line\n", ScriptStepOutputLimit/12+2) + line + "\n" + strings.Repeat("z", after) + "\n",
			strings.Repeat("x", ScriptStepOutputLimit) + " " + line + " " + strings.Repeat("y", after) + "\n",
		} {
			output, _ := redactor.scriptStepOutput(text, false)
			sanitized := SanitizeScriptStepEvent(ScriptStepEvent{Output: text}).Output
			fitted := fitScriptStepEvent(ScriptStepEvent{Phase: ScriptPhaseSetup, RunID: "r", Step: 1, Total: 1, Output: output}).Output
			for _, published := range []string{output, sanitized, fitted} {
				if leaksPart(published, secret) {
					t.Fatalf("shift %d: 16 KiB tail published part of the secret", shift)
				}
			}
		}
		// The raw tail kept for a running step starts shift bytes into the
		// secret's line.
		tail := &liveTail{limit: scriptLiveTailLimit}
		_, _ = tail.Write([]byte("start\n" + line + "\n"))
		_, _ = tail.Write([]byte(strings.Repeat("b", scriptLiveTailLimit-len(line)-2+shift) + "\n"))
		raw, dropped, _ := tail.snapshot()
		if shift > 0 && (!dropped || !strings.HasPrefix(raw, line[shift:])) {
			t.Fatalf("shift %d: raw tail starts %q (dropped %v)", shift, raw[:min(len(raw), 30)], dropped)
		}
		final, _ := redactor.scriptStepOutput(raw, dropped)
		live, _ := redactor.liveStepOutput(raw, dropped)
		if leaksPart(final, secret) || leaksPart(live, secret) {
			t.Fatalf("shift %d: raw tail cut published part of the secret", shift)
		}
		// The Setup summary's tail (worker side, before the script redaction).
		buffer := &tailBuffer{limit: len(line) + 66 - shift}
		_, _ = buffer.Write([]byte(line + "\n" + strings.Repeat("d", 64) + "\n"))
		if summary := RedactScriptOutput(buffer.String()); leaksPart(summary, secret) {
			t.Fatalf("shift %d: setup summary published %q", shift, summary)
		}
		// A Cleanup hook's tail without markers, cut shift bytes into the line.
		hookOutput := line[shift:] + "\n" + strings.Repeat("e\n", CleanupHookOutputLimit/2)
		events := CleanupStepEvents(hookOutput, CleanupRun{RunID: "r", ExitCode: 1, StartedAt: time.Now()})
		if len(events) != 1 || leaksPart(events[0].Output, secret) {
			t.Fatalf("shift %d: cleanup hook tail published part of the secret", shift)
		}
	}
}

// The Cleanup hook's tail that began inside a multi-byte character (its first
// bytes dropped as invalid UTF-8, so it is a few bytes short of the hook's
// limit) is still read as cut: its partial first line, the end of a secret,
// is not published, even when what follows it is mostly escapes the
// redaction strips, so that line would be within the event's tail.
func TestCleanupHookTailCutInsideACharacter(t *testing.T) {
	t.Parallel()
	const secret = "Leak72QZX"
	for split := 1; split < utf8.UTFMax; split++ {
		// The hook printed the last CleanupHookOutputLimit bytes, starting
		// split bytes into a 4-byte character just before the secret's end.
		head := "\U0001F600"[split:] + secret + "\n"
		end := "\nthe end"
		escapes := strings.Repeat("\x1b[0m", (CleanupHookOutputLimit-len(head)-len(end))/4)
		output := head + escapes + strings.Repeat("f", CleanupHookOutputLimit-len(head)-len(escapes)-len(end)) + end
		if len(output) != CleanupHookOutputLimit {
			t.Fatalf("output is %d bytes", len(output))
		}
		events := CleanupStepEvents(output, CleanupRun{RunID: "r", ExitCode: 1, StartedAt: time.Now()})
		if len(events) != 1 || leaksPart(events[0].Output, secret) || !events[0].OutputTruncated ||
			!strings.HasSuffix(events[0].Output, "the end") {
			t.Fatalf("split %d: events = %+v", split, events)
		}
	}
}

// A tail that lost its beginning and holds no newline (one long line) still
// publishes its end, without the word it starts with, which may be the end of
// a secret: in the Setup summary's tail and in a step's events.
func TestOutputTailWithoutANewlineKeepsItsEnd(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor(nil, nil)
	const secret = "Leak70QZX"
	line := "x DB_PASSWORD=" + secret + " " + strings.Repeat("z", ProjectScriptOutputLimit) + " the end"
	for shift := 1; shift < len("DB_PASSWORD="+secret); shift++ {
		// The kept tail starts shift bytes into the pair.
		buffer := &tailBuffer{limit: len(line) - len("x ") - shift}
		_, _ = buffer.Write([]byte(line))
		summary := buffer.String()
		if !strings.HasSuffix(summary, "zzz the end") || leaksPart(summary, secret) {
			t.Fatalf("shift %d: setup summary tail = %q...%q", shift, summary[:min(len(summary), 20)], summary[max(0, len(summary)-20):])
		}
		output, truncated := redactor.scriptStepOutput(line[len("x ")+shift:], true)
		if !truncated || !strings.HasSuffix(output, "zzz the end") || leaksPart(output, secret) {
			t.Fatalf("shift %d: event output = %q...", shift, output[:min(len(output), 20)])
		}
	}
	// A tail that is all one word is kept: only its end is published.
	buffer := &tailBuffer{limit: 64}
	_, _ = buffer.Write([]byte(strings.Repeat("w", 100)))
	if buffer.String() != strings.Repeat("w", 64) {
		t.Fatalf("one word tail = %q", buffer.String())
	}
}

// A secret printed across two live snapshots (the step wrote part of the line,
// then the rest) is never published in part: an unfinished line waits for its
// newline, and the whole line is then masked.
func TestScriptOutputSecretSplitAcrossLiveSnapshots(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor(nil, nil)
	for index, test := range scriptLeakCases() {
		if strings.Contains(test.text, "\n") {
			continue
		}
		heading := "step " + strconv.Itoa(index)
		tail := &liveTail{limit: scriptLiveTailLimit}
		half := len(test.text) / 2
		_, _ = tail.Write([]byte(heading + "\n" + test.text[:half]))
		raw, dropped, _ := tail.snapshot()
		if first, _ := redactor.liveStepOutput(raw, dropped); first != heading {
			t.Errorf("%s: first snapshot = %q; the unfinished line must wait", test.name, first)
		}
		_, _ = tail.Write([]byte(test.text[half:] + "\n"))
		raw, dropped, _ = tail.snapshot()
		second, _ := redactor.liveStepOutput(raw, dropped)
		for _, secret := range test.secrets {
			if leaksPart(second, secret) {
				t.Errorf("%s: second snapshot published %q", test.name, second)
			}
		}
	}
}

// The redaction stays linear in its input: each worst case below, 64 KiB of
// what makes one of its passes look back or ahead (a value, block, command or
// document that never ends, pairs packed onto one line), goes through the
// passes that read the text with their own loops (the regular expressions
// alone, activity.Redact's and the token shapes, are linear by construction)
// within a bound, 30 times what plain text of that size takes and at least
// 1 s, that a pass reading the text again per match (quadratic: billions of
// steps at this size) far exceeds. It runs alone, not in parallel, so the
// timings compare.
func TestScriptRedactionIsLinear(t *testing.T) {
	const size = scriptLiveTailLimit
	fill := func(unit string) string { return strings.Repeat(unit, size/len(unit)+1)[:size] }
	cases := map[string]string{
		"names without separators": fill("token"),
		"pairs":                    fill("password="),
		"pairs to the blank":       fill("password=x;"),
		"unclosed quotes":          fill(`password: "`),
		"block headers on a line":  fill("password: | #") + "\n" + fill(" ")[:size/2] + "\nnext: 1",
		"block headers":            fill("password: |\n"),
		"nested blocks":            fill("password: |\n  "),
		"escaped quotes":           `"token":"` + fill(`\\\"`),
		"backslashes":              `"token":"` + fill(`\`),
		"escaped members":          fill(`\"password\":\"`),
		"single quotes":            fill(`password='`),
		"flags":                    fill("--token "),
		"flags to flags":           fill("-token -"),
		"argument lists":           fill(`"--token",`),
		"python strings":           fill(`password: u'`),
		"commands":                 fill("mysql -p "),
		"logins":                   fill("docker login -p x "),
		"command words":            fill("docker "),
		"sshpass words":            fill("sshpass "),
		"quoted arguments":         "mysql " + fill(`"-p`),
		"quoted separators":        fill(`mysql -e "a;b" -p x; `),
		"unclosed argument quotes": fill(`mysql "a; `),
		"enclosed commands":        fill(`echo "mysql -px"; `),
		"curl users":               fill("curl -u a:b "),
		"secret documents":         fill("kind: Secret\ndata:\n  a: b\n"),
		"secret data":              "kind: Secret\ndata:\n" + fill("  a: |\n"),
		"secret documents split":   fill("kind: Secret\n---\ndata:\n"),
		"url users":                fill("https://abcdefghij0123456789@"),
		"url schemes":              fill("a://"),
		"signatures":               fill("&sig="),
		"escapes":                  fill("\x1b["),
		"carriage returns":         fill("password=x\r"),
		"bearer":                   fill("token: Bearer "),
	}
	redactor := newScriptRedactor([]string{"MY_SERVICE_SECRET=value-of-a-secret"}, []string{"ao_worker.token.value"})
	redactAll := func(text string) time.Duration {
		started := time.Now()
		text = activity.StripControl(collapseCarriageReturns(text))
		text = maskURLTokens(maskKubernetesSecretData(maskCommandPasswords(redactSecretPairs(text))))
		_ = redactor.maskTrailingFragment(text)
		return time.Since(started)
	}
	bound := max(time.Second, 30*redactAll(fill("plain text ")))
	for name, text := range cases {
		if elapsed := redactAll(text); elapsed > bound {
			t.Errorf("%s: redacting 64 KiB took %v (bound %v)", name, elapsed, bound)
		}
	}
}

// publishedByRuns runs commands as a project's Setup and as its Cleanup (the
// commands script with its markers, parsed as the control plane parses it,
// with the commands it knows) and returns everything they publish: each
// step's live and final events (command and output) as the worker sends them
// and as the control plane sanitizes them, the failed Setup's summary (its
// output, command and message, what the startup error's Detail is made of),
// and the Cleanup steps, whose failed step's output repeats its command line.
func publishedByRuns(t *testing.T, commands []string) string {
	t.Helper()
	summary, events := runSetupWithSteps(t, SetupRunner{UpdateInterval: 10 * time.Millisecond}, commands)
	nonce := NewScriptMarkerNonce()
	output, code := runCleanupScript(t, CleanupCommandsScript(commands, nonce), t.TempDir())
	events = append(events, CleanupStepEvents(output, CleanupRun{
		RunID: "r", Nonce: nonce, Commands: nonEmptyCommands(commands), ExitCode: code, StartedAt: time.Now(),
	})...)
	for _, event := range append([]ScriptStepEvent(nil), events...) {
		events = append(events, SanitizeScriptStepEvent(event))
	}
	encoded, err := json.Marshal(events)
	if err != nil {
		t.Fatal(err)
	}
	var published strings.Builder
	for _, event := range events {
		published.WriteString(event.Command + "\n" + event.Output + "\n")
	}
	published.WriteString(string(encoded) + "\n" + summary.Output + "\n" + summary.Command + "\n" + summary.Message() + "\n")
	published.WriteString(RedactScriptOutput(summary.Output) + "\n" + RedactScriptOutput(summary.Command) + "\n")
	return published.String()
}

// The values commands assign to secret-named variables, in every shell form,
// literally; not those of other names or that expand something.
func TestAssignedSecretValues(t *testing.T) {
	t.Parallel()
	got := assignedSecretValues([]string{
		`MY_TOKEN=value01; export MY_SERVICE_SECRET=value02 && DB_PASSWORD="value 03" API_KEY='value04'`,
		"declare -x DEPLOY_SECRET=value05; local -r SIGNING_KEY=value06; readonly AUTH_TOKEN=value07",
		"typeset NPM_AUTH=value08; set -a; CI_TOKEN=value09 make deploy; (OTHER_PASS=value10)",
		`HOST=db.example.com PORT=5432 KEY_PATH=/etc/key GIT_AUTHOR_NAME=jane TOKEN=$(cat f) SECRET="$X" PASSWORD=` + "`cat p`",
	})
	want := []string{"value01", "value02", "value 03", "value04", "value05", "value06", "value07", "value08", "value09", "value10"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("assignedSecretValues = %q, want %q", got, want)
	}
}

// A value a step assigns to a secret-named variable and prints with nothing
// naming it (the staging re-test's case 14) is masked on every path: in the
// step that sets it, in a later step that inherits it through a file, in the
// failed run's summary, and in Cleanup's steps and its failed step's line.
func TestAssignedSecretNeverPublished(t *testing.T) {
	published := publishedByRuns(t, []string{
		`export MY_SERVICE_SECRET=FAKEVAL14; printf '%s\n' "$MY_SERVICE_SECRET"`,
		`echo 'export DEPLOY_TOKEN=Leak80QZXa' > "${TMPDIR:-/tmp}/ao-assigned-$$.sh"; echo saved`,
		`echo 'DEPLOY_TOKEN=Leak81QZXb' >/dev/null; printf 'inherited %s\n' Leak81QZXb; exit 3`,
	})
	for _, secret := range []string{"FAKEVAL14", "Leak80QZXa", "Leak81QZXb"} {
		if strings.Contains(published, secret) {
			t.Errorf("%q published:\n%s", secret, published)
		}
	}
	if !strings.Contains(published, "inherited [REDACTED]") {
		t.Errorf("the later step's output is not masked:\n%s", published)
	}
}

// The staging re-test's commands that print a Kubernetes Secret, a YAML block
// scalar and a coloured pair (its cases 8, 10 and 13): masked in the command
// fields too, which are redacted before they are put on one line, with a
// printf's \n read as the line break it prints and colour codes written out
// (\033[0m) read as escapes between a name and its value.
func TestCommandsThatPrintSecretsAreMasked(t *testing.T) {
	commands := []string{
		`printf 'apiVersion: v1\nkind: Secret\nmetadata:\n  name: demo\ndata:\n  foo: RkFLRVZBTDg=\n'`,
		`printf 'private_key: |\n  FAKEVAL10-line-one\n  FAKEVAL10-line-two\nafter_block: visible\n'`,
		`printf '\033[32mPASSWORD\033[0m=FAKEVAL13\n'`,
	}
	commands = append(commands, strings.Join(commands, "; ")+"; exit 3")
	published := publishedByRuns(t, commands)
	for _, secret := range []string{"RkFLRVZBTDg", "FAKEVAL10", "FAKEVAL13"} {
		if strings.Contains(published, secret) {
			t.Errorf("%q published:\n%s", secret, published)
		}
	}
	if !strings.Contains(published, `after_block: visible\n`) || !strings.Contains(published, `name: demo\n`) {
		t.Errorf("the commands lost what is not secret:\n%s", published)
	}

	// Each command path, and a command written on several lines.
	redactor := newScriptRedactor(nil, nil)
	multiline := "cat <<EOF > secret.yaml\nkind: Secret\ndata:\n  token: TGVhazgy\nEOF\nvault_key: |\n  Leak83block\nnext"
	for _, command := range append(commands, multiline, `echo -e "\e[1mAPI_TOKEN\e[0m: \x1b[2mLeak84lit" "\u001b[1mNPM_AUTH\u001b[0m=Leak85lit"`) {
		stepCommand, _ := scriptStepCommand(command, redactor)
		summary, _ := scriptCommandSummary(command, redactor)
		for path, shown := range map[string]string{
			"scriptStepCommand":         stepCommand,
			"scriptCommandSummary":      summary,
			"SanitizeScriptStepEvent":   SanitizeScriptStepEvent(ScriptStepEvent{Command: command}).Command,
			"worker then control plane": SanitizeScriptStepEvent(ScriptStepEvent{Command: stepCommand}).Command,
		} {
			for _, secret := range []string{"RkFLRVZBTDg", "FAKEVAL10", "FAKEVAL13", "TGVhazgy", "Leak83", "Leak84", "Leak85"} {
				if strings.Contains(shown, secret) {
					t.Errorf("%s published %q in %q", path, secret, shown)
				}
			}
			if strings.Contains(shown, "\n") {
				t.Errorf("%s is not on one line: %q", path, shown)
			}
		}
	}
}

// Commands written inside quoted strings, one after another on a line (the
// staging re-test's failing step): each is read up to the end of its string
// or shell segment, so mysql's -p never reads on into sshpass's ssh -p 22,
// and the curl -u after them is still masked, in the step's command and
// output, the summary and Cleanup's failed step line alike.
func TestPasswordCommandsAreReadPerSegment(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ text, want string }{
		{
			`echo "mysql -pFAKEVAL3"; echo "sshpass -p FAKEVAL4 ssh -p 22 host"; echo "curl -u admin:FAKEVAL5 https://x"`,
			`echo "mysql -p[REDACTED]"; echo "sshpass -p [REDACTED] ssh -p 22 host"; echo "curl -u admin:[REDACTED] https://x"`,
		},
		{`echo 'run mysql -p FAKEVAL3' && ssh -p 22 host`, `echo 'run mysql -p [REDACTED]' && ssh -p 22 host`},
		{`mysql -pFAKEVAL3 || ssh -p 22 host | curl --user admin:FAKEVAL5 x`, `mysql -p[REDACTED] || ssh -p 22 host | curl --user admin:[REDACTED] x`},
		{`x=$(mysql -pFAKEVAL3)$(ssh -p 22 h) curl -sS -u "admin:FAKEVAL5" x`, `x=$(mysql -p[REDACTED])$(ssh -p 22 h) curl -sS -u "admin:[REDACTED]" x`},
		{`mysql -h db -e "select 1" -pFAKEVAL3`, `mysql -h db -e "select 1" -p[REDACTED]`},
		// A separator inside a quoted argument is part of it.
		{`mysql -e "select 1; select 2" -pFAKEVAL3`, `mysql -e "select 1; select 2" -p[REDACTED]`},
		{`mysql -e 'a;b|c&&d' -p FAKEVAL3 db; ssh -p 22 h`, `mysql -e 'a;b|c&&d' -p [REDACTED] db; ssh -p 22 h`},
		{`sh -c "mysql -e 'a; b' -pFAKEVAL3"; ssh -p 22 h`, `sh -c "mysql -e 'a; b' -p[REDACTED]"; ssh -p 22 h`},
		{`curl -d "a=1;b=2" -u "admin:FAKEVAL5" x && ssh -p 22 h`, `curl -d "a=1;b=2" -u "admin:[REDACTED]" x && ssh -p 22 h`},
		{`echo "mysql -e"; ssh -p 22 host`, `echo "mysql -e"; ssh -p 22 host`},
		{`don't run mysql -pFAKEVAL3; then ssh -p 22 h`, `don't run mysql -p[REDACTED]; then ssh -p 22 h`},
	} {
		if got := RedactScriptOutput(test.text); got != test.want {
			t.Errorf("RedactScriptOutput(%q)\n got %q\nwant %q", test.text, got, test.want)
		}
	}
}

func TestStagingFailedStepCommandsAreMasked(t *testing.T) {
	combined := `echo "== summary step =="; echo "docker login -p FAKEVAL2 registry.example.com || true"; echo "mysql -pFAKEVAL3"; ` +
		`echo "sshpass -p FAKEVAL4 ssh -p 22 host"; echo "curl -u admin:FAKEVAL5 https://x"; echo "docker run -p 8080:80 nginx"; exit 3`
	published := publishedByRuns(t, []string{combined})
	for _, secret := range []string{"FAKEVAL2", "FAKEVAL3", "FAKEVAL4", "FAKEVAL5"} {
		if strings.Contains(published, secret) {
			t.Errorf("%q published:\n%s", secret, published)
		}
	}
	for _, kept := range []string{"ssh -p 22 host", "docker run -p 8080:80 nginx", "cleanup step 1: echo"} {
		if !strings.Contains(published, kept) {
			t.Errorf("%q is not shown:\n%s", kept, published)
		}
	}
	if strings.Contains(published, "ssh -p [REDACTED]") {
		t.Errorf("ssh's port is masked:\n%s", published)
	}
}

// A bare secret value is masked up to where it plainly ends, not past it: a ;
// or & before the next name=value or a blank, and a quote that closes the
// string the pair is written in (the staging re-test's case 12 lost ;next=1
// and its closing quote). A separator or quote inside the value is still part
// of it.
func TestBareSecretValueEnds(t *testing.T) {
	for _, test := range []struct{ text, want string }{
		{`API_KEY=FAKEVAL12;next=1`, `API_KEY=[REDACTED];next=1`},
		{`echo "API_KEY=FAKEVAL12;next=1"`, `echo "API_KEY=[REDACTED];next=1"`},
		{`export MY_SERVICE_SECRET=FAKEVAL14; printf '%s\n' "$MY_SERVICE_SECRET"`, `export MY_SERVICE_SECRET=[REDACTED]; printf '%s\n' "$MY_SERVICE_SECRET"`},
		{`echo 'DEPLOY_TOKEN=abc123' >/dev/null`, `echo 'DEPLOY_TOKEN=[REDACTED]' >/dev/null`},
		{`curl "https://x/?token=abc123&page=2"`, `curl "https://x/?token=[REDACTED]&page=2"`},
		{`["PASSWORD=abc123","HOME=/root"]`, `["PASSWORD=[REDACTED]","HOME=/root"]`},
		// Still part of the value.
		{`PASSWORD=ab;cd next`, `PASSWORD=[REDACTED] next`},
		{`PASSWORD=ab"cd next`, `PASSWORD=[REDACTED] next`},
		{`client_secret=ab&cd`, `client_secret=[REDACTED]`},
	} {
		if got := RedactScriptOutput(test.text); got != test.want {
			t.Errorf("RedactScriptOutput(%q)\n got %q\nwant %q", test.text, got, test.want)
		}
	}
	published := publishedByRuns(t, []string{`echo "API_KEY=FAKEVAL12;next=1"; exit 3`})
	if strings.Contains(published, "FAKEVAL12") || !strings.Contains(published, `echo "API_KEY=[REDACTED];next=1"; exit 3`) ||
		!strings.Contains(published, "API_KEY=[REDACTED];next=1\n") {
		t.Errorf("case 12 published:\n%s", published)
	}
}

// A trace of a printf that writes a Kubernetes Secret or a YAML block scalar
// (as set -x prints it) is masked in a step's output as in its command: its
// literal \n is read as the line break it prints, then put back.
func TestTracedPrintfIsReadByLine(t *testing.T) {
	t.Parallel()
	redactor := newScriptRedactor(nil, nil)
	for _, test := range []struct{ text, want string }{
		{`+ printf 'apiVersion: v1\nkind: Secret\ndata:\n  foo: TGVhazkw\n'`, `+ printf 'apiVersion: v1\nkind: Secret\ndata:\n  foo: [REDACTED]\n'`},
		{`+ printf 'vault_key: |\n  Leak91block\nnext: 1\n'`, `+ printf 'vault_key: |\n  [REDACTED]\nnext: 1\n'`},
	} {
		if got, _ := redactor.scriptStepOutput(test.text+"\n", false); got != test.want {
			t.Errorf("scriptStepOutput(%q)\n got %q\nwant %q", test.text, got, test.want)
		}
	}
}
