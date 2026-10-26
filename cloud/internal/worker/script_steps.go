package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/bits"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/google/uuid"
)

// Live Setup and Cleanup logs. Each step of a project's Setup or Cleanup run
// is reported on the session's event stream as a ScriptStepEventType event
// carrying the redacted tail of the step's output, so the person can follow a
// run and read it afterwards without opening the sandbox. The workspace.setup
// summary event is unchanged.

// ScriptStepEventType is the session event that reports one step of a Setup
// or Cleanup run.
const ScriptStepEventType = "workspace.script.step"

// Phases of ScriptStepEvent.Phase.
const (
	ScriptPhaseSetup   = "setup"
	ScriptPhaseCleanup = "cleanup"
)

// ScriptStepEvent.Status values beyond a finished step's SetupSucceeded,
// SetupFailed and SetupTimedOut.
const (
	ScriptStepRunning = "running"
	// ScriptStepSkipped is a step that never ran because an earlier one
	// failed, or, with a Hint saying why, one AO did not run (for example in
	// an additional repository's folder that is not AO's,
	// ForeignRepoFolderHint).
	ScriptStepSkipped = "skipped"
)

const (
	// ScriptStepOutputLimit bounds the output tail one event carries.
	ScriptStepOutputLimit = 16 << 10
	// ScriptStepCommandLimit bounds an event's command.
	ScriptStepCommandLimit = 300
	// scriptLiveTailLimit is the raw output kept per step. It is larger than
	// ScriptStepOutputLimit so a secret is redacted whole before the tail is
	// cut from the redacted text.
	scriptLiveTailLimit = 64 << 10
	// scriptStepEncodedLimit bounds an encoded event, so its escaped output
	// still fits the control plane's worker event body limit (24 KiB).
	scriptStepEncodedLimit = 20 << 10
	// scriptStepQueueLimit bounds the final events waiting to be sent.
	scriptStepQueueLimit = 256
	// scriptStepAttempts is how often a step's final event is sent before it
	// is dropped; a live update is sent once.
	scriptStepAttempts = 3
	// scriptStepSendTimeout bounds one send.
	scriptStepSendTimeout = 10 * time.Second
	// scriptStepFinalWait bounds how long the steps' final events still
	// queued are sent once the run is over or the worker is stopping, and
	// how long a finished run waits for them.
	scriptStepFinalWait = 10 * time.Second
)

// ErrScriptStepsUnsupported is what a publish function returns when the
// control plane refuses ScriptStepEventType (one from before live logs). The
// run then sends nothing more.
var ErrScriptStepsUnsupported = errors.New("the control plane does not accept " + ScriptStepEventType + " events")

// ScriptStepEvent is the payload of ScriptStepEventType. The newest event for
// a run's step carries everything needed to show it: a live update repeats
// the step's facts with the latest output tail.
type ScriptStepEvent struct {
	Phase string `json:"phase"`
	// RunID is the same for every step of one run of the whole list.
	RunID string `json:"runId"`
	// Step is 1-based, of Total.
	Step  int `json:"step"`
	Total int `json:"total"`
	// Command is the step's command on one line, redacted and bounded, and
	// Repository the repository folder it ran in ("" for the main one).
	Command    string `json:"command"`
	Repository string `json:"repository"`
	Status     string `json:"status"`
	// ExitCode and DurationMS are set once the step finished (not on a
	// running or skipped step). StartedAt (RFC 3339, UTC) is unset for a
	// skipped step and for a Cleanup step whose start was not reported.
	ExitCode   *int   `json:"exitCode,omitempty"`
	StartedAt  string `json:"startedAt,omitempty"`
	DurationMS *int64 `json:"durationMs,omitempty"`
	// Output is the redacted tail of the step's combined output, at most
	// ScriptStepOutputLimit bytes, without terminal escape sequences.
	// OutputTruncated is true when its beginning was dropped.
	Output          string `json:"output"`
	OutputTruncated bool   `json:"outputTruncated"`
	// LogPath is the file in the sandbox with the full output.
	LogPath string `json:"logPath,omitempty"`
	// The rest describe a failed or timed-out step, as SetupEvent does.
	User   string   `json:"user,omitempty"`
	Groups []string `json:"groups,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Hint   string   `json:"hint,omitempty"`
	// TimeoutMinutes is the step's time limit (domain.ScriptStepTimeout); 0
	// when it is not known (a worker or Cleanup hook that predates per-step
	// limits, whose steps had the default).
	TimeoutMinutes int `json:"timeoutMinutes,omitempty"`
	// OutputAgeMS, on a running step's live update from the worker, is how
	// long before the update the step last wrote any byte of output (a
	// duration, so the worker's clock never matters). The control plane turns
	// it into OutputAt with its own clock (StampScriptStepOutputAt) and never
	// keeps it.
	OutputAgeMS *int64 `json:"outputAgeMs,omitempty"`
	// OutputAt, on a running step's live update as the control plane keeps
	// it, is when the step last wrote any byte of output (RFC 3339, UTC), by
	// the control plane's clock. A step that keeps printing the same line, or
	// redraws a progress line with \r, writes output without changing its
	// tail; OutputAt still moves, so a client tells a busy step from a
	// silent one.
	OutputAt string `json:"outputAt,omitempty"`
}

// maxScriptStepOutputAge bounds the output age a live update may report.
const maxScriptStepOutputAge = 24 * time.Hour

// StampScriptStepOutputAt sets a running step's OutputAt from the output age
// its worker reported (OutputAgeMS), by receivedAt, the control plane's
// clock, so a worker whose clock is off never makes a step look silent or
// busy. Any OutputAt the worker sent is replaced, and OutputAgeMS is dropped.
func StampScriptStepOutputAt(event ScriptStepEvent, receivedAt time.Time) ScriptStepEvent {
	age := event.OutputAgeMS
	event.OutputAgeMS, event.OutputAt = nil, ""
	if event.Status != ScriptStepRunning || age == nil || *age < 0 {
		return event
	}
	// Capped in milliseconds first: a huge age would overflow the Duration.
	event.OutputAt = scriptTimestamp(receivedAt.Add(-time.Duration(min(*age, maxScriptStepOutputAge.Milliseconds())) * time.Millisecond))
	return event
}

// finish records a finished step's exit status and duration.
func (e *ScriptStepEvent) finish(status string, exitCode int, duration time.Duration) {
	e.Status = status
	e.ExitCode = &exitCode
	milliseconds := duration.Milliseconds()
	e.DurationMS = &milliseconds
}

// ValidScriptStepEvent reports whether a ScriptStepEvent is well formed:
// a known phase and status, a step within its run, and an exit status and
// duration exactly when the step finished.
func ValidScriptStepEvent(event ScriptStepEvent) bool {
	if event.Phase != ScriptPhaseSetup && event.Phase != ScriptPhaseCleanup {
		return false
	}
	if strings.TrimSpace(event.RunID) == "" || len(event.RunID) > 128 ||
		event.Step < 1 || event.Total < event.Step || event.Total > 1000 {
		return false
	}
	if event.DurationMS != nil && *event.DurationMS < 0 {
		return false
	}
	if event.StartedAt != "" {
		if _, err := time.Parse(time.RFC3339Nano, event.StartedAt); err != nil {
			return false
		}
	}
	switch event.Status {
	case ScriptStepRunning, ScriptStepSkipped:
		return event.ExitCode == nil && event.DurationMS == nil
	case SetupSucceeded, SetupFailed, SetupTimedOut:
		return event.ExitCode != nil
	}
	return false
}

// SanitizeScriptStepEvent bounds and redacts an event a worker sent, on top
// of the worker's own redaction, so the control plane never stores more.
func SanitizeScriptStepEvent(event ScriptStepEvent) ScriptStepEvent {
	redactor := scriptRedactor{}
	// Redacted before activity.Redact runs (in redact), which masks a quoted
	// value only up to an escaped quote and would hide the rest of it.
	event.Output, event.OutputTruncated = scriptOutputTail(redactor.redact(activity.StripControl(event.Output)), event.OutputTruncated)
	event.Command = boundScriptCommand(redactor.redactCommand(event.Command))
	event.Repository = activity.Truncate(event.Repository, 256)
	event.Dir = activity.Truncate(event.Dir, 1024)
	event.User = activity.Truncate(event.User, 128)
	event.LogPath = activity.Truncate(event.LogPath, 1024)
	event.Hint = activity.Truncate(event.Hint, 512)
	if len(event.Groups) > 32 {
		event.Groups = event.Groups[:32]
	}
	for index, group := range event.Groups {
		event.Groups[index] = activity.Truncate(group, 64)
	}
	if !domain.ValidScriptStepTimeoutMinutes(event.TimeoutMinutes) {
		event.TimeoutMinutes = 0
	}
	return fitScriptStepEvent(event)
}

// secretNamePattern matches the names of environment variables whose values
// are masked in a step's output.
var secretNamePattern = regexp.MustCompile(`(?i)(TOKEN|SECRET|KEY|PASSWORD|PASS|CREDENTIAL|AUTH|PRIVATE)`)

// secretPairPattern matches the name and separator of a NAME=value or
// NAME: value pair, or of a JSON "name": member at any depth of escaping
// (\"password\": inside a JSON string, \\\"password\\\": inside one more, as
// an AWS Secrets Manager SecretString can be), whose name looks like a
// secret's, also with colour codes written out around the separator
// (literalEscapes). Its groups are the name and the separator; the value
// after it is read by secretValue.
var secretPairPattern = regexp.MustCompile(`(?i)\\*["']?([a-z0-9_.-]*(?:token|secret|key|pass|auth|credential|private)[a-z0-9_.-]*)` +
	literalEscapes + `\\*["']?(` + literalEscapes + `\s*[:=]\s*` + literalEscapes + `)`)

// literalEscapes matches terminal colour codes written out in a command
// (printf '\033[32mPASSWORD\033[0m=x', \e[...m, \x1b[...m, \u001b[...m),
// which separate a pair's name from its value in what it prints.
const literalEscapes = `(?:\\(?:033|e|x1b|u001b)\[[0-9;]*[a-z])*`

// secretFlagPattern matches a command-line flag whose name looks like a
// secret's and its separating blanks (--password x); --password=x is a
// secretPairPattern pair.
var secretFlagPattern = regexp.MustCompile(`(?i)(--?[a-z0-9_.-]*(?:token|secret|key|pass|auth|credential|private)[a-z0-9_.-]*)([ \t]+)`)

// secretArgumentPattern matches a quoted flag whose name looks like a
// secret's in a JSON or YAML argument list and the separator before its
// value, the next quoted item (["--token","x"], also escaped inside a JSON
// string).
var secretArgumentPattern = regexp.MustCompile(`(?i)\\*["'](--?[a-z0-9_.-]*(?:token|secret|key|pass|auth|credential|private)[a-z0-9_.-]*)(\\*["'][ \t]*,[ \t]*)`)

var (
	// secretNameFragment is what makes a pair's name look like a secret's.
	secretNameFragment = regexp.MustCompile(`(?i)token|secret|key|pass|auth|credential|private`)
	// benignNameWords are common words that contain a fragment but name no
	// secret (GIT_AUTHOR_NAME, "tests passed: 3"); they are ignored when the
	// name is checked.
	benignNameWords = regexp.MustCompile(`(?i)author|passed|passes|passing|bypass|keyboard|monkey`)
	// publicNameSuffix marks a name whose value is a socket, path, directory,
	// file or URL (SSH_AUTH_SOCK, KEY_PATH), or that says the secret is read
	// from standard input (--password-stdin); credentials in a URL are masked
	// on their own.
	publicNameSuffix = regexp.MustCompile(`(?i)[_.-](?:sock|path|dir|file|url|stdin)$`)
)

// isSecretName reports whether a pair's value is masked: its name contains a
// secretNameFragment outside benignNameWords and has no publicNameSuffix.
func isSecretName(name string) bool {
	return !publicNameSuffix.MatchString(name) &&
		secretNameFragment.MatchString(benignNameWords.ReplaceAllString(name, ""))
}

// secretShapes are token shapes masked on top of activity.Redact's: npm,
// Google API, SendGrid and Hugging Face keys, Stripe restricted and secret
// keys (not its publishable pk_ keys), and the body of a private key whose
// BEGIN line is no longer in the output (only its END line is).
var secretShapes = []*regexp.Regexp{
	regexp.MustCompile(`\bnpm_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`\bhf_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\b[rs]k_(?:live|test)_[A-Za-z0-9]{10,}`),
	regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{30,}`),
	regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`(?m)(?:^[A-Za-z0-9+/=]{16,}\r?\n)+-----END [A-Z ]*PRIVATE KEY-----`),
}

// RedactScriptOutput masks credentials in a Setup or Cleanup run's output
// with the patterns every step event gets (no known secret values): the
// control plane's redaction of what a worker or a Cleanup hook printed. Like a
// step's output (scriptStepOutput), the text is first collapsed to what a
// terminal shows and stripped of escapes and control characters, so a colour
// code between a secret's name and its value never hides the pair.
func RedactScriptOutput(text string) string {
	return scriptRedactor{}.redact(activity.StripControl(collapseCarriageReturns(text)))
}

// redactSecretPairs masks the value of each secretPairPattern pair,
// secretArgumentPattern list item and secretFlagPattern flag with a secret's
// name (isSecretName), keeping its quotes.
func redactSecretPairs(text string) string {
	text = maskSecretPairs(text, secretPairPattern, namedPair)
	text = maskSecretPairs(text, secretArgumentPattern, argumentPair)
	return maskSecretPairs(text, secretFlagPattern, flagPair)
}

// pairKind is the form of the pairs maskSecretPairs masks.
type pairKind int

const (
	// namedPair is NAME=value, NAME: value or a JSON member; its value may
	// be a YAML block scalar.
	namedPair pairKind = iota
	// flagPair is --name value, starting at a flag boundary.
	flagPair
	// argumentPair is "--name","value" in an argument list.
	argumentPair
)

// maskSecretPairs masks the values of pattern's matches (name and separator
// groups) whose name is a secret's. A value is read whole (secretValue), so
// an escaped JSON string holding more JSON is masked as one; a name that is
// not a secret's is skipped alone, so pairs inside its value are still
// found. A value already masked is skipped alone too: what follows it is
// still read as text, so a pass that masked a value short (up to an escaped
// quote) never hides the rest of it.
func maskSecretPairs(text string, pattern *regexp.Regexp, kind pairKind) string {
	var out strings.Builder
	last := 0
	blocks := newBlockScalars(text)
	for position := 0; position < len(text); {
		match := pattern.FindStringSubmatchIndex(text[position:])
		if match == nil {
			break
		}
		nameStart, valueStart := position+match[2], position+match[5]
		next := valueStart
		switch {
		case kind == flagPair && nameStart > 0 && !isFlagBoundary(text[nameStart-1]):
		case !isSecretName(text[nameStart : position+match[3]]):
		case strings.HasPrefix(text[valueStart:], redactedMarker):
			next = valueStart + len(redactedMarker)
		case kind == flagPair && (valueStart == len(text) || text[valueStart] == '-'),
			kind == argumentPair && strings.HasPrefix(strings.TrimLeft(text[valueStart:], `\"'`), "--"):
			// The next flag, not a value.
		case kind == namedPair && strings.Contains(text[position+match[4]:valueStart], ":") &&
			blocks.end(nameStart, valueStart) > valueStart:
			// A YAML block scalar (private_key: |): its indented lines are
			// the value.
			end := blocks.end(nameStart, valueStart)
			out.WriteString(text[last:valueStart])
			out.WriteString(maskedBlockScalar(text[valueStart:end]))
			last, next = end, end
		default:
			if end, masked := secretValue(text, valueStart); end > valueStart {
				out.WriteString(text[last:valueStart])
				out.WriteString(masked)
				last, next = end, end
			}
		}
		position = next
	}
	if last == 0 {
		return text
	}
	out.WriteString(text[last:])
	return out.String()
}

const redactedMarker = "[REDACTED]"

// isFlagBoundary reports whether a flag may start after c: a blank, a quote,
// the start of a command, an assignment (ARGS=--password x) or a list
// ([--password x]).
func isFlagBoundary(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '"', '\'', '(', ';', '=', '[':
		return true
	}
	return false
}

// credentialSchemes are bare values naming the kind of credential that
// follows them ("Authorization: Bearer x"): the word after them is masked too.
var credentialSchemes = map[string]bool{"bearer": true, "basic": true, "token": true, "bot": true, "digest": true}

// secretValue reads the value of a pair that starts at text[start:] and
// returns where it ends and its masked form:
//
//   - a double-quoted string at any depth of JSON escaping ("x", \"x\",
//     \\\"x\\\"), read up to the quote that closes it at that depth, so the
//     escaped quotes of the JSON it may hold do not end it; one that is not
//     closed on its line is masked to the end of the line;
//   - a single-quoted string, up to its closing quote or the end of the line;
//   - a Python string with its prefix (u'x', b"x"), as the quoted string;
//   - a bare word, up to a blank (a separator or quote inside it is part of
//     it: PASSWORD=ab;cd), with the word after it when it names a credential
//     scheme ("Bearer x").
func secretValue(text string, start int) (int, string) {
	rest := text[start:]
	if prefix := stringPrefixLength(rest); prefix > 0 {
		end, masked := secretValue(text, start+prefix)
		return end, rest[:prefix] + masked
	}
	slashes := len(rest) - len(strings.TrimLeft(rest, `\`))
	switch {
	case slashes < len(rest) && rest[slashes] == '"':
		quote := rest[:slashes+1]
		depth := escapeDepth(slashes)
		for index := slashes + 1; index < len(rest); index++ {
			switch rest[index] {
			case '\n':
				return start + index, quote + redactedMarker
			case '"':
				run := 0
				for run < index-slashes-1 && rest[index-1-run] == '\\' {
					run++
				}
				// A quote escaped deeper belongs to the value; one at this
				// depth (or shallower, in malformed text) closes it.
				if escapeDepth(run) <= depth {
					return start + index + 1, quote + redactedMarker + quote
				}
			}
		}
		return len(text), quote + redactedMarker
	case slashes == 0 && rest != "" && rest[0] == '\'':
		if end := strings.IndexAny(rest[1:], "'\n"); end >= 0 {
			if rest[1+end] == '\'' {
				return start + end + 2, "'" + redactedMarker + "'"
			}
			return start + 1 + end, "'" + redactedMarker
		}
		return len(text), "'" + redactedMarker
	}
	end := bareWordEnd(rest)
	if credentialSchemes[strings.ToLower(rest[:end])] {
		blanks := len(rest[end:]) - len(strings.TrimLeft(rest[end:], " \t"))
		if blanks > 0 {
			if word := bareWordEnd(rest[end+blanks:]); word > 0 {
				end += blanks + word
			}
		}
	}
	return start + end, redactedMarker
}

// bareWordEnd is the length of the bare value text starts with: up to a
// blank. A separator or quote inside it is part of it, as a secret may hold
// one (PASSWORD=ab;cd, TOKEN=ab"cd), except where it plainly ends the value:
// a ; or & before a blank, the end, or the next name=value (API_KEY=x;next=1,
// ?token=x&page=2), and a quote before a blank, the end or a separator, which
// closes a string the pair is written in (echo "API_KEY=x").
func bareWordEnd(text string) int {
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case ' ', '\t', '\n', '\r':
			return index
		case ';', '&':
			if index+1 == len(text) || isBlank(text[index+1]) || startsWithAssignment(text[index+1:]) {
				return index
			}
		case '"', '\'':
			if index+1 == len(text) || isBlank(text[index+1]) || strings.IndexByte(";,)]}&|<>", text[index+1]) >= 0 {
				return index
			}
		}
	}
	return len(text)
}

func isBlank(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// startsWithAssignment reports whether text starts with name= (a letter or
// underscore, then letters, digits, _, . or -).
func startsWithAssignment(text string) bool {
	for index := 0; index < len(text); index++ {
		c := text[index]
		switch {
		case c == '_' || (c|0x20 >= 'a' && c|0x20 <= 'z'):
		case index > 0 && ((c >= '0' && c <= '9') || c == '.' || c == '-'):
		case index > 0 && c == '=':
			return true
		default:
			return false
		}
	}
	return false
}

// stringPrefixLength is the length of a Python string prefix (u, b, r, f,
// br, rb, fr, rf) that text starts with, followed by its quote, or 0.
func stringPrefixLength(text string) int {
	for _, prefix := range []string{"u", "b", "r", "f", "br", "rb", "fr", "rf"} {
		if len(text) > len(prefix) && strings.EqualFold(text[:len(prefix)], prefix) &&
			(text[len(prefix)] == '\'' || text[len(prefix)] == '"') {
			return len(prefix)
		}
	}
	return 0
}

// escapeDepth is how deep in JSON strings a quote preceded by run backslashes
// is: a quote at depth d is preceded by 2^d - 1 backslashes, plus any number
// of escaped literal backslashes (2^(d+1) each), so the depth is the count of
// trailing one bits of run.
func escapeDepth(run int) int {
	return bits.TrailingZeros(^uint(run))
}

// workerTokenPattern matches an AO worker token (see TokenManager.Issue).
var workerTokenPattern = regexp.MustCompile(`\b` + tokenPrefix + `\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`)

// minSecretValueLength skips values too short to mask without blanking
// ordinary words ("1", "true").
const minSecretValueLength = 6

// scriptRedactor masks credentials in a step's output and command before they
// leave the sandbox: the values of secret-named environment variables and any
// extra secrets (the worker's own token) verbatim, then the values of
// secret-named pairs, flags and argument lists (a YAML block scalar's lines
// too), the passwords known commands take in short flags, a Kubernetes
// Secret's data, tokens used as a URL's user and signed URLs' signatures,
// activity.Redact's patterns, secretShapes and AO worker tokens. The control
// plane redacts what it receives and a Cleanup hook's
// output with the same patterns (scriptRedactor{}, without values).
type scriptRedactor struct {
	values []string
}

func newScriptRedactor(environment []string, extra []string) scriptRedactor {
	seen := map[string]bool{}
	var values []string
	add := func(value string) {
		value = strings.TrimSpace(value)
		if len(value) >= minSecretValueLength && !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	for _, entry := range environment {
		if name, value, ok := strings.Cut(entry, "="); ok && secretNamePattern.MatchString(name) {
			add(value)
		}
	}
	for _, value := range extra {
		add(value)
	}
	// The longest first, so a secret that contains another is masked whole.
	sort.SliceStable(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	return scriptRedactor{values: values}
}

func (r scriptRedactor) redact(text string) string {
	for _, value := range r.values {
		text = strings.ReplaceAll(text, value, redactedMarker)
	}
	// A printf's literal \n (in a command, or in a trace of it that set -x
	// printed) is read as the line break it prints, so the line-based passes
	// (a YAML block scalar, a Kubernetes Secret's data) read what it writes:
	// as a carriage return and newline, which they take for a line's end, put
	// back afterwards. Callers strip carriage returns first, so each one left
	// is one of these.
	expanded := strings.Contains(text, `\n`) && !strings.Contains(text, "\r")
	if expanded {
		text = strings.ReplaceAll(text, `\n`, "\r\n")
	}
	// Pairs first: activity.Redact masks a quoted value up to its first
	// quote, which would cut an escaped JSON string (a SecretString) short.
	text = redactSecretPairs(text)
	text = maskCommandPasswords(text)
	text = maskKubernetesSecretData(text)
	text = maskURLTokens(text)
	text = activity.Redact(text)
	for _, shape := range secretShapes {
		text = shape.ReplaceAllString(text, "[REDACTED]")
	}
	text = workerTokenPattern.ReplaceAllString(text, "[REDACTED]")
	if expanded {
		text = strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", `\n`), "\r", `\n`)
	}
	return text
}

// redactCommand is a step's command as its events and summaries show it:
// redacted, then on one line. It is redacted while it still has its lines, so
// the line-based passes (a YAML block scalar, a Kubernetes Secret's data) read
// a command that writes one (a printf's literal \n is read as a line break
// too, see redact).
func (r scriptRedactor) redactCommand(command string) string {
	return strings.Join(strings.Fields(r.redact(activity.StripControl(command))), " ")
}

// scriptStepOutput turns a finished step's raw output tail into what an event
// carries: carriage-return overwrites (progress bars) collapsed to their last
// state, terminal escapes and control characters removed, credentials masked,
// and the tail of at most ScriptStepOutputLimit bytes. headDropped says the
// raw output already lost its beginning; its partial first line (or word, in
// one long line) is dropped (dropPartialHead), so a secret cut in half there
// is never published. An unfinished last line (the
// step ended mid-line) is published, but its end is masked when it may be the
// start of a credential (maskTrailingFragment).
func (r scriptRedactor) scriptStepOutput(raw string, headDropped bool) (string, bool) {
	if headDropped {
		raw = dropPartialHead(raw)
	}
	text := collapseCarriageReturns(raw)
	// Only stripped here: redact masks secret pairs before activity.Redact's
	// patterns, which mask a quoted value only up to an escaped quote and
	// would hide the rest of it (a SecretString's password) from the pairs.
	text = r.redact(activity.StripControl(text))
	if raw != "" && !strings.HasSuffix(raw, "\n") {
		text = r.maskTrailingFragment(text)
	}
	return scriptOutputTail(text, headDropped)
}

// dropPartialHead drops the start of an output tail that lost its beginning,
// which may be the end of a credential the redaction no longer recognizes:
// its first line when it has more than one, and otherwise (one long line, of
// which only the end was kept) its first word. A tail that is all one word is
// kept whole rather than dropped, so a long line's end is still published.
func dropPartialHead(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		return text[index+1:]
	}
	if index := strings.IndexAny(text, " \t"); index >= 0 {
		return text[index+1:]
	}
	return text
}

// liveStepOutput is scriptStepOutput for a step still running: only its
// finished lines are published. The line being written may be a credential
// printed part way ("DB_PASSWORD=supersecr"), which no pattern recognizes
// until it is whole, so it waits for its newline or the step's final event.
func (r scriptRedactor) liveStepOutput(raw string, headDropped bool) (string, bool) {
	return r.scriptStepOutput(raw[:strings.LastIndexByte(raw, '\n')+1], headDropped)
}

// minSecretFragmentLength is the shortest end of an output that is masked as
// the start of a known secret value.
const minSecretFragmentLength = 4

// tokenFragmentPattern matches the start of a well-known token shape at the
// end of an output, however short: what a step printed before it ended mid-way
// through a token the full patterns would mask.
var tokenFragmentPattern = regexp.MustCompile(`\b(?:gh[pousr]_|github_pat_|sk-|[rs]k_(?:live|test)_|hf_|xox[abprs]?-?|AKIA|eyJ|npm_|AIza|glpat-|SG\.)[A-Za-z0-9_.-]*$`)

// maskTrailingFragment masks the end of an output that may be a credential
// cut short: the beginning of a known secret value (at least
// minSecretFragmentLength bytes of it), or of a well-known token shape.
func (r scriptRedactor) maskTrailingFragment(text string) string {
	for _, value := range r.values {
		for length := min(len(value)-1, len(text)); length >= minSecretFragmentLength; length-- {
			if strings.HasSuffix(text, value[:length]) {
				return text[:len(text)-length] + "[REDACTED]"
			}
		}
	}
	if location := tokenFragmentPattern.FindStringIndex(text); location != nil {
		return text[:location[0]] + "[REDACTED]"
	}
	return text
}

// scriptOutputTail keeps the last ScriptStepOutputLimit bytes of already
// redacted text on a line start when it can, on a rune start otherwise.
func scriptOutputTail(text string, truncated bool) (string, bool) {
	text = strings.TrimRight(strings.ToValidUTF8(text, ""), "\n")
	if len(text) <= ScriptStepOutputLimit {
		return text, truncated
	}
	tail := text[len(text)-ScriptStepOutputLimit:]
	if index := strings.IndexByte(tail, '\n'); index >= 0 && index < len(tail)-1 {
		tail = tail[index+1:]
	}
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return tail, true
}

// collapseCarriageReturns keeps what a terminal would show of each line that
// redraws itself with carriage returns: the text after the last one.
func collapseCarriageReturns(text string) string {
	if !strings.Contains(text, "\r") {
		return text
	}
	lines := strings.Split(text, "\n")
	for index, line := range lines {
		line = strings.TrimRight(line, "\r")
		if cut := strings.LastIndexByte(line, '\r'); cut >= 0 {
			line = line[cut+1:]
		}
		lines[index] = line
	}
	return strings.Join(lines, "\n")
}

// scriptStepCommand is a step's command for its events: as the person wrote
// it (without the cd AO wraps a repository step in), on one line, redacted
// before it is cut to ScriptStepCommandLimit bytes, and its repository folder.
func scriptStepCommand(stored string, redactor scriptRedactor) (command, repository string) {
	repository, command, _ = domain.ParseRepoScriptCommand(stored)
	return boundScriptCommand(redactor.redactCommand(command)), repository
}

func boundScriptCommand(command string) string {
	if len(command) <= ScriptStepCommandLimit {
		return command
	}
	return strings.ToValidUTF8(command[:ScriptStepCommandLimit-3], "") + "..."
}

// fitScriptStepEvent drops more of the output's beginning while the encoded
// event is over scriptStepEncodedLimit (JSON escapes can grow it severalfold).
func fitScriptStepEvent(event ScriptStepEvent) ScriptStepEvent {
	bare := event
	bare.Output = ""
	overhead, err := json.Marshal(bare)
	if err != nil || len(overhead) >= scriptStepEncodedLimit {
		return event
	}
	for event.Output != "" {
		encoded, err := json.Marshal(event)
		if err != nil || len(encoded) <= scriptStepEncodedLimit {
			return event
		}
		// Keep the share of the output that fits at its current escaping
		// rate, and at least a little less each time.
		keep := len(event.Output) * (scriptStepEncodedLimit - len(overhead)) / (len(encoded) - len(overhead))
		keep = min(keep, len(event.Output)-1)
		output := event.Output[len(event.Output)-max(keep, 0):]
		for len(output) > 0 && !utf8.RuneStart(output[0]) {
			output = output[1:]
		}
		event.Output, event.OutputTruncated = output, true
	}
	return event
}

// liveTail keeps the last limit bytes of a running step's output for its live
// updates. It is written by the step's output copier and read by the watcher.
type liveTail struct {
	mu      sync.Mutex
	limit   int
	data    bytes.Buffer
	dropped bool
	version uint64
	// written is when any byte was last written (zero before the first).
	written time.Time
}

func (t *liveTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data.Write(p)
	if over := t.data.Len() - t.limit; over > 0 {
		t.data.Next(over)
		t.dropped = true
	}
	if len(p) > 0 {
		t.version++
		t.written = time.Now()
	}
	return len(p), nil
}

// lastWrite is when any byte was last written (zero before the first).
func (t *liveTail) lastWrite() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.written
}

// snapshot returns the kept output, whether its beginning was dropped, and a
// version that changes with every write.
func (t *liveTail) snapshot() (string, bool, uint64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.data.String(), t.dropped, t.version
}

// scriptStepPublisher sends one run's step events, in order, from its own
// goroutine, so a step never waits on the network. Live updates coalesce:
// only the newest waits to be sent, and one that fails is dropped. A step's
// final event is sent up to scriptStepAttempts times, each on a context
// detached from the worker's, so a worker that is stopping still reports how
// its steps ended: once the run is closed or the worker's context ends, live
// updates are dropped and the queued final events are sent for up to
// finalWait more. Once the control plane refuses the event type, the run
// sends nothing more.
type scriptStepPublisher struct {
	ctx        context.Context
	publish    func(context.Context, ScriptStepEvent) error
	retryDelay time.Duration
	finalWait  time.Duration

	mu          sync.Mutex
	live        *ScriptStepEvent
	finals      []ScriptStepEvent
	closed      bool
	unsupported bool
	// flushBy is when the run stopped (closed, or the worker's context
	// ended) plus finalWait: no final event is sent after it.
	flushBy time.Time
	wake    chan struct{}
	done    chan struct{}
}

func newScriptStepPublisher(
	ctx context.Context,
	publish func(context.Context, ScriptStepEvent) error,
	retryDelay, finalWait time.Duration,
) *scriptStepPublisher {
	publisher := &scriptStepPublisher{
		ctx: ctx, publish: publish, retryDelay: retryDelay, finalWait: finalWait,
		wake: make(chan struct{}, 1), done: make(chan struct{}),
	}
	go publisher.run()
	return publisher
}

// update replaces any live update still waiting to be sent.
func (p *scriptStepPublisher) update(event ScriptStepEvent) {
	p.mu.Lock()
	if !p.closed && !p.unsupported {
		p.live = &event
	}
	p.mu.Unlock()
	p.signal()
}

// final queues a step's final event; a live update for the same step still
// waiting is dropped, as the final one supersedes it.
func (p *scriptStepPublisher) final(event ScriptStepEvent) {
	p.mu.Lock()
	if !p.closed && !p.unsupported {
		if p.live != nil && p.live.Step == event.Step {
			p.live = nil
		}
		if len(p.finals) < scriptStepQueueLimit {
			p.finals = append(p.finals, event)
		}
	}
	p.mu.Unlock()
	p.signal()
}

// close stops taking events and waits for the queued final events to be
// sent, up to finalWait.
func (p *scriptStepPublisher) close() {
	p.mu.Lock()
	p.closed = true
	p.stoppingLocked()
	p.mu.Unlock()
	p.signal()
	// A send in flight ends by flushBy too; a moment more lets it return.
	timer := time.NewTimer(p.finalWait + 100*time.Millisecond)
	defer timer.Stop()
	select {
	case <-p.done:
	case <-timer.C:
	}
}

func (p *scriptStepPublisher) signal() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// stoppingLocked reports whether the run is closed or the worker's context
// ended, and when it is, the time final events are sent until. p.mu is held.
func (p *scriptStepPublisher) stoppingLocked() (time.Time, bool) {
	if !p.closed && p.ctx.Err() == nil {
		return time.Time{}, false
	}
	if p.flushBy.IsZero() {
		p.flushBy = time.Now().Add(p.finalWait)
	}
	return p.flushBy, true
}

func (p *scriptStepPublisher) run() {
	defer close(p.done)
	for {
		p.mu.Lock()
		flushBy, stopping := p.stoppingLocked()
		if stopping {
			// Live updates no longer matter; final events do, until flushBy.
			p.live = nil
		}
		if p.unsupported || (stopping && !time.Now().Before(flushBy)) {
			p.live, p.finals = nil, nil
			p.mu.Unlock()
			return
		}
		// Final events are older than the pending live update (which is
		// always for the step running now), so they go first.
		if len(p.finals) > 0 {
			event := p.finals[0]
			p.finals = p.finals[1:]
			p.mu.Unlock()
			p.send(event, true)
			continue
		}
		if p.live != nil {
			event := *p.live
			p.live = nil
			p.mu.Unlock()
			p.send(event, false)
			continue
		}
		if stopping {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		select {
		case <-p.wake:
		case <-p.ctx.Done():
		}
	}
}

// send sends a live update once, on the worker's context, or a final event
// up to scriptStepAttempts times, on a context detached from the worker's and
// bounded by flushBy once the run is stopping.
func (p *scriptStepPublisher) send(event ScriptStepEvent, final bool) {
	attempts := 1
	if final {
		attempts = scriptStepAttempts
	}
	for attempt := 1; attempt <= attempts; attempt++ {
		parent, timeout := p.ctx, scriptStepSendTimeout
		if final {
			parent = context.WithoutCancel(p.ctx)
			p.mu.Lock()
			flushBy, stopping := p.stoppingLocked()
			p.mu.Unlock()
			if stopping {
				timeout = min(timeout, time.Until(flushBy))
				if timeout <= 0 {
					return
				}
			}
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		err := p.publish(ctx, event)
		cancel()
		if err == nil {
			return
		}
		if errors.Is(err, ErrScriptStepsUnsupported) {
			p.mu.Lock()
			p.unsupported = true
			p.mu.Unlock()
			return
		}
		if attempt == attempts {
			return
		}
		delay := p.retryDelay * time.Duration(attempt)
		if !final {
			return
		}
		p.mu.Lock()
		flushBy, stopping := p.stoppingLocked()
		p.mu.Unlock()
		if stopping && time.Until(flushBy) < delay {
			return
		}
		time.Sleep(delay)
	}
}

// liveUpdateInterval paces a running step's updates: every two seconds, and
// every five seconds once the step has run two minutes, so a long, chatty
// step does not fill the session's event stream (the control plane also
// drops a step's live updates once its final event arrives).
func liveUpdateInterval(elapsed time.Duration) time.Duration {
	if elapsed >= 2*time.Minute {
		return 5 * time.Second
	}
	return 2 * time.Second
}

// liveHeartbeat is how often a running step that keeps writing output sends
// a live update even when its tail looks the same (the same line again, or a
// progress line redrawn with \r), so its OutputAt keeps moving and a client
// never takes it for a silent step.
const liveHeartbeat = 60 * time.Second

// watchStepOutput sends a live update whenever the step finished another line
// of output, paced by interval, and at least every heartbeat while it keeps
// writing any output, until the returned stop is called. Each update carries
// how long ago the step last wrote a byte.
func watchStepOutput(
	tail *liveTail,
	interval func(elapsed time.Duration) time.Duration,
	heartbeat time.Duration,
	emit func(output string, truncated bool, outputAge time.Duration),
	redactor scriptRedactor,
) (stop func()) {
	quit, finished := make(chan struct{}), make(chan struct{})
	started := time.Now()
	go func() {
		defer close(finished)
		var sent uint64
		lastOutput, lastTruncated := "", false
		lastEmit := started
		for {
			timer := time.NewTimer(interval(time.Since(started)))
			select {
			case <-quit:
				timer.Stop()
				return
			case <-timer.C:
			}
			raw, dropped, version := tail.snapshot()
			if version == sent {
				continue
			}
			output, truncated := redactor.liveStepOutput(raw, dropped)
			if (output == "" || (output == lastOutput && truncated == lastTruncated)) && time.Since(lastEmit) < heartbeat {
				// Nothing new is finished (the step is still writing a line,
				// or wrote the same one again): wait, until the heartbeat says
				// it is still writing.
				continue
			}
			sent = version
			if output != "" {
				lastOutput, lastTruncated = output, truncated
			}
			lastEmit = time.Now()
			emit(output, truncated, max(0, time.Since(tail.lastWrite())))
		}
	}()
	return func() {
		close(quit)
		<-finished
	}
}

// scriptTimestamp formats a step's start time for its events.
func scriptTimestamp(at time.Time) string {
	return at.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}

// setupStepEvents reports one Setup run's steps through a SetupRunner's
// Publish. Without Publish every method does nothing.
type setupStepEvents struct {
	publisher *scriptStepPublisher
	redactor  scriptRedactor
	interval  func(time.Duration) time.Duration
	heartbeat time.Duration
	runID     string
	total     int

	current   ScriptStepEvent
	started   time.Time
	live      *liveTail
	stopWatch func()
}

// redactor masks what a run of commands publishes: the values of the
// secret-named variables of the environment its steps run in, the values its
// commands assign to secret-named variables (assignedSecretValues: a step
// that sets one prints it with nothing naming it, and later steps inherit it)
// and Secrets, then the patterns.
func (r SetupRunner) redactor(commands []string) scriptRedactor {
	var secrets []string
	if r.Secrets != nil {
		secrets = r.Secrets()
	}
	return newScriptRedactor(os.Environ(), append(secrets, assignedSecretValues(commands)...))
}

func (r SetupRunner) startStepEvents(ctx context.Context, total int, redactor scriptRedactor) *setupStepEvents {
	if r.Publish == nil {
		return &setupStepEvents{}
	}
	interval := liveUpdateInterval
	if r.UpdateInterval > 0 {
		interval = func(time.Duration) time.Duration { return r.UpdateInterval }
	}
	finalWait := scriptStepFinalWait
	if r.FinalWait > 0 {
		finalWait = r.FinalWait
	}
	heartbeat := liveHeartbeat
	if r.LiveHeartbeat > 0 {
		heartbeat = r.LiveHeartbeat
	}
	return &setupStepEvents{
		publisher: newScriptStepPublisher(ctx, r.Publish, time.Second, finalWait),
		redactor:  redactor,
		interval:  interval,
		heartbeat: heartbeat,
		runID:     uuid.NewString(),
		total:     total,
	}
}

// start reports a step as running and returns the writer that feeds its live
// updates (nil without Publish).
func (s *setupStepEvents) start(step int, command, logPath string, timeout time.Duration) io.Writer {
	if s.publisher == nil {
		return nil
	}
	s.stopWatching()
	s.started = time.Now()
	s.current = ScriptStepEvent{
		Phase: ScriptPhaseSetup, RunID: s.runID, Step: step, Total: s.total,
		Status: ScriptStepRunning, StartedAt: scriptTimestamp(s.started), LogPath: logPath,
		TimeoutMinutes: timeoutMinutes(timeout),
	}
	s.current.Command, s.current.Repository = scriptStepCommand(command, s.redactor)
	s.publisher.update(fitScriptStepEvent(s.current))
	s.live = &liveTail{limit: scriptLiveTailLimit}
	running := s.current
	s.stopWatch = watchStepOutput(s.live, s.interval, s.heartbeat, func(output string, truncated bool, outputAge time.Duration) {
		update := running
		update.Output, update.OutputTruncated = output, truncated
		age := outputAge.Milliseconds()
		update.OutputAgeMS = &age
		s.publisher.update(fitScriptStepEvent(update))
	}, s.redactor)
	return s.live
}

// finish reports the step start began with its outcome and final output.
func (s *setupStepEvents) finish(outcome SetupEvent) {
	if s.publisher == nil {
		return
	}
	s.stopWatching()
	event := s.current
	event.finish(outcome.Status, outcome.ExitCode, time.Since(s.started))
	raw, dropped, _ := s.live.snapshot()
	event.Output, event.OutputTruncated = s.redactor.scriptStepOutput(raw, dropped)
	if event.Output == "" && outcome.Status != SetupSucceeded {
		// A step that could not start says why instead.
		event.Output, _ = s.redactor.scriptStepOutput(outcome.Output, false)
	}
	if event.LogPath != "" {
		if _, err := os.Stat(event.LogPath); err != nil {
			event.LogPath = ""
		}
	}
	if outcome.Status != SetupSucceeded {
		event.User, event.Groups, event.Dir, event.Hint = outcome.User, outcome.Groups, outcome.Dir, outcome.Hint
	}
	s.publisher.final(fitScriptStepEvent(event))
}

// skip reports the steps after a failure, which never ran, with their time
// limits.
func (s *setupStepEvents) skip(commands []string, timeouts []time.Duration) {
	if s.publisher == nil {
		return
	}
	for index, command := range commands {
		event := ScriptStepEvent{
			Phase: ScriptPhaseSetup, RunID: s.runID, Step: s.current.Step + 1 + index, Total: s.total,
			Status: ScriptStepSkipped, TimeoutMinutes: timeoutMinutes(timeouts[index]),
		}
		event.Command, event.Repository = scriptStepCommand(command, s.redactor)
		s.publisher.final(event)
	}
}

// skipOne reports step, which did not run, with why (hint).
func (s *setupStepEvents) skipOne(step int, command string, timeout time.Duration, hint string) {
	if s.publisher == nil {
		return
	}
	event := ScriptStepEvent{
		Phase: ScriptPhaseSetup, RunID: s.runID, Step: step, Total: s.total,
		Status: ScriptStepSkipped, TimeoutMinutes: timeoutMinutes(timeout), Hint: hint,
	}
	event.Command, event.Repository = scriptStepCommand(command, s.redactor)
	s.publisher.final(event)
}

func (s *setupStepEvents) close() {
	if s.publisher == nil {
		return
	}
	s.stopWatching()
	s.publisher.close()
}

func (s *setupStepEvents) stopWatching() {
	if s.stopWatch != nil {
		s.stopWatch()
		s.stopWatch = nil
	}
}
