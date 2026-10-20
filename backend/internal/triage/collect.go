package triage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

const (
	collectTimeout  = 10 * time.Second
	collectMaxBytes = 64 << 10
)

// Collect builds one bounded, read-only snapshot. Daemon health is taken from
// the initiating desktop; the run-file is evidence only and is never probed.
func Collect(parent context.Context, dataDir string, origin Origin) ([]Evidence, error) {
	ctx, cancel := context.WithTimeout(parent, collectTimeout)
	defer cancel()
	at := time.Now().UTC()
	var evidence []Evidence
	add := func(id, source, status, text string, values map[string]any) {
		evidence = append(evidence, Evidence{ID: id, Kind: id, Source: source, CollectedAt: at, Status: status, Text: Redact(text), Values: values})
	}
	add("collector", "triage host", "collected", "", buildFacts(at))

	if executable, err := os.Executable(); err != nil {
		add("collector-executable", "os.Executable", "unavailable", "The desktop executable path could not be read.", nil)
	} else {
		add("collector-executable", "os.Executable", "collected", "", map[string]any{"path": executable, "name": filepath.Base(executable)})
	}
	if executable, err := exec.LookPath("ao"); err != nil {
		add("path-ao", "PATH lookup without execution", "unavailable", "No ao executable was found on PATH.", nil)
	} else {
		add("path-ao", "PATH lookup without execution", "collected", "This lookup did not execute ao and may differ from the desktop executable.", map[string]any{"path": executable, "name": filepath.Base(executable)})
	}

	paths, pathErr := config.ResolvePaths()
	pathValues := map[string]any{"triageDataDir": dataDir, "dataDir": paths.DataDir, "dataDirSource": paths.DataDirSource,
		"runFile": paths.RunFilePath, "runFileSource": paths.RunFileSource,
		"triageDataDirMatchesResolved": filepath.Clean(dataDir) == filepath.Clean(paths.DataDir)}
	if pathErr != nil {
		add("paths", "independent AO path resolution", "unavailable", "AO paths could not all be resolved.", pathValues)
	} else {
		add("paths", "independent AO path resolution", "collected", "Resolved independently from the daemon and retained with their configuration sources.", pathValues)
	}
	evidence = append(evidence, readRunFile(ctx, paths.RunFilePath, at))

	originValues := map[string]any{"appVersion": origin.AppVersion, "buildMode": origin.BuildMode,
		"surface": origin.Surface, "sessionId": origin.SessionID, "daemonState": origin.DaemonState, "daemonCode": origin.DaemonCode}
	add("origin", "initiating desktop action", "collected", "Daemon state and code were supplied by the desktop supervisor.", originValues)
	if origin.DaemonOutput != "" {
		output := origin.DaemonOutput
		status := "collected"
		if len(output) > 24<<10 {
			output = strings.ToValidUTF8(output[:24<<10], "")
			status = "truncated"
		}
		add("daemon-output", "desktop supervisor's retained startup and daemon output", status, output, nil)
	}
	if len(origin.Machine) != 0 {
		if len(origin.Machine) > collectMaxBytes {
			add("machine", "cached desktop machine snapshot", "truncated", "The cached snapshot exceeded its size limit and was omitted.", nil)
		} else {
			var snapshot any
			if err := decodeJSON(origin.Machine, &snapshot, false); err != nil {
				add("machine", "cached desktop machine snapshot", "unavailable", "The cached machine snapshot was invalid.", nil)
			} else {
				add("machine", "cached desktop machine snapshot", "collected", "Measurements and sample time are cached desktop values.", map[string]any{"snapshot": unknownCPU(snapshot)})
			}
		}
	}

	evidence = append(evidence, readRetainedLog(ctx, dataDir, "daemon.log", "daemon-log", 24<<10, ""))
	if localContextAllowed(origin) {
		if validRunID(origin.SessionID) {
			evidence = append(evidence,
				readRetainedLog(ctx, dataDir, "chat-hosts/"+origin.SessionID+"/acp-prompt.journal", "provider-journal", 12<<10, ""),
				readRetainedLog(ctx, dataDir, "hooks.log", "session-hook-errors", 12<<10, origin.SessionID))
		}
		retained, err := readRetainedContext(ctx, dataDir, origin)
		evidence = append(evidence, retained...)
		if err != nil {
			add("retained-context", "existing local session database", "unavailable", "Some retained session context could not be read. No database was created or migrated.", nil)
		}
	} else if origin.SessionID != "" {
		add("retained-context", "local session database", "unavailable", "Local retained data was skipped because the selected session is remote or its identity did not match.", nil)
	}
	if ctx.Err() != nil {
		add("snapshot-limit", "triage host", "unavailable", "The bounded collection deadline expired; remaining sources were skipped.", nil)
	}

	raw, err := sanitizeJSON(evidence)
	if err != nil {
		return nil, errors.New("diagnostic evidence could not be serialized safely")
	}
	evidence = nil // Decode fresh maps so renamed sensitive keys cannot survive.
	if err := decodeJSON(raw, &evidence, false); err != nil {
		return nil, err
	}
	return evidence, parent.Err()
}

func buildFacts(at time.Time) map[string]any {
	facts := map[string]any{"os": runtime.GOOS, "arch": runtime.GOARCH, "goVersion": runtime.Version(), "collectedAt": at.Format(time.RFC3339Nano)}
	if info, ok := debug.ReadBuildInfo(); ok {
		facts["moduleVersion"] = info.Main.Version
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				facts["vcsRevision"] = setting.Value
			case "vcs.time":
				facts["vcsTime"] = setting.Value
			case "vcs.modified":
				facts["vcsModified"] = setting.Value == "true"
			}
		}
	}
	return facts
}

func readRunFile(ctx context.Context, path string, at time.Time) Evidence {
	item := Evidence{ID: "run-file", Kind: "run-file", Source: "bounded existing AO run-file", CollectedAt: at, Status: "unavailable"}
	if path == "" || ctx.Err() != nil {
		item.Text = "No readable run-file path was available."
		return item
	}
	raw, err := readPrivateBytes(path, collectMaxBytes)
	if errors.Is(err, errSnapshotTooLarge) {
		item.Status, item.Text = "truncated", "The run-file exceeded its size limit."
		return item
	}
	if err != nil {
		item.Text = "The existing run-file could not be read."
		return item
	}
	var info runfile.Info
	if err := decodeJSON(raw, &info, false); err != nil || info.PID <= 0 || info.Port < 1 || info.Port > 65535 || info.StartedAt.IsZero() || info.StartedAt.After(at.Add(time.Minute)) {
		item.Text = "The existing run-file was invalid."
		return item
	}
	item.Status = "collected"
	item.Text = "Validated run-file metadata only. It was not used to contact or control the daemon."
	item.Values = map[string]any{"pid": info.PID, "port": info.Port, "startedAt": info.StartedAt.UTC(), "owner": info.Owner}
	return item
}

var errSnapshotTooLarge = errors.New("snapshot exceeded limit")

func readPrivateBytes(path string, limit int64) ([]byte, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("file is not regular")
	}
	if before.Size() > limit {
		return nil, errSnapshotTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, errors.New("file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > limit {
		return nil, errSnapshotTooLarge
	}
	return raw, nil
}

func decodeJSON(data []byte, value any, strict bool) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("trailing JSON")
	}
	return nil
}

// This snapshot was just decoded from private JSON, so it has no shared owners.
func unknownCPU(value any) any {
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			if key == "cpuPercent" && v["cpuMeasured"] != true {
				v[key] = nil
			} else {
				v[key] = unknownCPU(child)
			}
		}
	case []any:
		for i, child := range v {
			v[i] = unknownCPU(child)
		}
	}
	return value
}
