// Package dictation owns downloaded speech models and local inference processes.
package dictation

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

//go:embed worker.py
var workerScript string

//go:embed gguf_worker.py
var ggufWorkerScript string

//go:embed gguf-catalog.json
var ggufCatalog []byte

const runtimeVersion = "0.2.15"

// Model describes a built-in or downloaded local recognizer.
type Model struct {
	ID            string   `json:"id"`
	Name          string   `json:"name"`
	DownloadBytes int64    `json:"downloadBytes"`
	Installed     bool     `json:"installed"`
	Available     bool     `json:"available"`
	Description   string   `json:"description,omitempty"`
	Languages     []string `json:"languages,omitempty"`
	License       string   `json:"license,omitempty"`
}

// Status reports model selection and live installation/recording state.
type Status struct {
	Selected   string  `json:"selected"`
	Models     []Model `json:"models"`
	Installing string  `json:"installing"`
	Stage      string  `json:"stage"`
	// ProgressDone runs from 0 to ProgressTotal across the whole installation
	// (runtime, speech engine and model together) and never goes backwards. It
	// follows real bytes where they can be measured and eases toward the end of
	// a step where they cannot. ProgressTotal is zero when nothing is installing.
	ProgressDone  int64  `json:"progressDone"`
	ProgressTotal int64  `json:"progressTotal"`
	Error         string `json:"error"`
	Recording     bool   `json:"recording"`
	// Preparing is true while the selected local model is still loading into memory. Dictation
	// works then, but its first result waits for the load.
	Preparing bool `json:"preparing"`
}

// Event carries a local recognizer result to its owning client.
type Event struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Message string `json:"message,omitempty"`
}
type modelSpec struct {
	id, name, repo, directory                          string
	bytes                                              int64
	revision, filename, checksum, description, license string
	languages                                          []string
}

var models = loadModels()

func loadModels() []modelSpec {
	var catalog struct {
		Models []struct {
			ID, Revision, Name, Filename, SHA256, Description, License string
			Size                                                       int64
			Languages                                                  []string
		}
	}
	if err := json.Unmarshal(ggufCatalog, &catalog); err != nil {
		panic(err)
	}
	result := make([]modelSpec, 0, 2+len(catalog.Models))
	result = append(result, []modelSpec{
		{id: "phonon-2.5", name: "Phonon 2.5 · 25 languages", repo: "FermionResearch__Phonon-2.5", directory: "model_phonon25_f25x1_48k_int6", bytes: 156181652},
		{id: "phonon-2.5-en", name: "Phonon 2.5 English", repo: "FermionResearch__Phonon-2.5-English", directory: "model_phonon25_en_pen2u1125_int6", bytes: 157616598},
	}...)
	for _, m := range catalog.Models {
		result = append(result, modelSpec{id: m.ID, name: m.Name, bytes: m.Size, revision: m.Revision, filename: m.Filename, checksum: m.SHA256, description: m.Description, license: m.License, languages: m.Languages})
	}
	return result
}

func (m modelSpec) gguf() bool { return m.filename != "" }

func findModel(id string) (modelSpec, bool) {
	for _, m := range models {
		if m.id == id {
			return m, true
		}
	}
	return modelSpec{}, false
}

// Service serializes model installation and owns the reusable inference worker.
type Service struct {
	mu                                         sync.Mutex
	root, selected, installing, stage, failure string
	progressDone, progressTotal                int64
	stepFrom, stepTo                           int64
	cancelInstall                              context.CancelFunc
	installDone                                chan struct{}
	worker                                     *worker
	recording, closed                          bool
}

// selectedPreparing reports whether the selected local model is installed but its worker has not
// finished loading it. The caller holds the service lock.
func (s *Service) selectedPreparing() bool {
	m, ok := findModel(s.selected)
	if !ok || s.closed || s.installing != "" || !s.installed(m) {
		return false
	}
	return s.worker == nil || !s.worker.ready
}

func (s *Service) marker(m modelSpec) string {
	return filepath.Join(s.root, strings.ReplaceAll(m.id, "/", "--")+".ready")
}

// New restores and prepares the selected model without downloading anything.
func New(dataDir string) *Service {
	s := &Service{root: filepath.Join(dataDir, "dictation"), selected: "phonon-2.5"}
	if runtime.GOOS == "darwin" {
		s.selected = "native-macos"
	}
	if data, err := os.ReadFile(filepath.Join(s.root, "selected")); err == nil {
		id := string(data)
		if _, ok := findModel(id); ok || (id == "native-macos" && runtime.GOOS == "darwin") {
			s.selected = id
		}
	}
	s.prepare()
	return s
}
func (s *Service) modelPath(m modelSpec) string {
	if m.gguf() {
		return filepath.Join(s.root, "models", "gguf", strings.ReplaceAll(m.id, "/", "--"), m.filename)
	}
	return filepath.Join(s.root, "models", "speech", m.repo, m.directory)
}
func (s *Service) python() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(s.root, "runtime-"+runtimeVersion, "Scripts", "python.exe")
	}
	return filepath.Join(s.root, "runtime-"+runtimeVersion, "bin", "python")
}
func (s *Service) installed(m modelSpec) bool {
	py := s.python()
	paths := []string{py, filepath.Join(s.modelPath(m), "config.json"), filepath.Join(s.modelPath(m), "packed_manifest.json"), s.marker(m)}
	if m.gguf() {
		py = s.ggufPython()
		paths = []string{py, s.modelPath(m), s.marker(m), s.vadPath()}
	}
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	// The runtime writes its own marker last, so a half-built environment never counts as installed.
	_, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(py)), "ao-ready"))
	return err == nil
}

// Status derives readiness from completed installations on disk.
func (s *Service) Status(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	result := Status{Selected: s.selected, Models: []Model{}, Installing: s.installing, Stage: s.stage, ProgressDone: s.progressDone, ProgressTotal: s.progressTotal, Error: s.failure, Recording: s.recording}
	result.Preparing = s.selectedPreparing()
	if runtime.GOOS == "darwin" {
		result.Models = append(result.Models, Model{ID: "native-macos", Name: "macOS · on-device", Installed: true, Available: true})
	}
	_, available := uvAsset()
	for _, m := range models {
		result.Models = append(result.Models, Model{ID: m.id, Name: m.name, DownloadBytes: m.bytes, Installed: s.installed(m), Available: available, Description: m.description, Languages: m.languages, License: m.license})
	}
	return result, nil
}

// Select persists an installed model and unloads the previous worker.
func (s *Service) Select(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.recording || s.installing != "" {
		return errors.New("wait for dictation or installation to finish")
	}
	m, ok := findModel(id)
	if (id != "native-macos" || runtime.GOOS != "darwin") && (!ok || !s.installed(m)) {
		return errors.New("install this model before selecting it")
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	if s.selected == id {
		s.prepare()
		return nil
	}
	path := filepath.Join(s.root, "selected")
	if err := os.WriteFile(path+".tmp", []byte(id), 0o600); err != nil {
		return err
	}
	if err := os.Rename(path+".tmp", path); err != nil {
		return err
	}
	s.stopWorker()
	s.selected = id
	s.prepare()
	return nil
}

// Delete removes an unselected downloaded model.
func (s *Service) Delete(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	m, ok := findModel(id)
	if !ok {
		return errors.New("unknown downloaded model")
	}
	if s.recording || s.installing != "" || s.selected == id {
		return errors.New("choose another model before deleting this one")
	}
	if err := os.RemoveAll(filepath.Dir(s.modelPath(m))); err != nil {
		return err
	}
	err := os.Remove(s.marker(m))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// CancelInstall cancels the current download and its child processes.
func (s *Service) CancelInstall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancelInstall != nil {
		s.cancelInstall()
	}
}

// Close stops owned processes and waits for installer cleanup.
func (s *Service) Close() {
	s.mu.Lock()
	s.closed = true
	if s.cancelInstall != nil {
		s.cancelInstall()
	}
	done := s.installDone
	s.stopWorker()
	s.mu.Unlock()
	if done != nil {
		<-done
	}
}
func (s *Service) stopWorker() {
	if s.worker != nil {
		s.worker.cancel()
		s.worker = nil
	}
}
func (s *Service) env(offline bool) []string {
	env := append(os.Environ(), "FERMION_CACHE_DIR="+filepath.Join(s.root, "models"), "HF_HOME="+filepath.Join(s.root, "cache", "hf"), "XDG_CACHE_HOME="+filepath.Join(s.root, "cache"), "UV_CACHE_DIR="+filepath.Join(s.root, "cache", "uv"), "UV_PYTHON_INSTALL_DIR="+filepath.Join(s.root, "python"), "PYTHONNOUSERSITE=1", "UV_PYTHON_PREFERENCE=only-managed")
	if offline {
		env = append(env, "HF_HUB_OFFLINE=1", "HF_DATASETS_OFFLINE=1", "TRANSFORMERS_OFFLINE=1")
	}
	return env
}

type worker struct {
	input     io.WriteCloser
	events    chan Event
	cancel    context.CancelFunc
	resetting chan struct{}
	// ready is set, under the service lock, once the worker has loaded its model.
	ready bool
}

// Session exclusively owns one recording on the inference worker.
type Session struct {
	svc    *Service
	worker *worker
	mu     sync.Mutex
	ended  bool
	bytes  int
}

// prepare starts loading without waiting for inference readiness. Caller holds mu.
func (s *Service) prepare() {
	m, ok := findModel(s.selected)
	if !s.closed && !s.recording && s.installing == "" && ok && s.installed(m) {
		_ = s.ensureWorker(m)
	}
}

func (s *Service) ensureWorker(m modelSpec) error {
	if s.worker != nil {
		return nil
	}
	workerCtx, cancel := context.WithCancel(context.Background())
	python, script := s.python(), workerScript
	if m.gguf() {
		python, script = s.ggufPython(), ggufWorkerScript
	}
	cmd := process.CommandContext(workerCtx, python, "-u", "-c", script, s.modelPath(m), s.vadPath())
	process.ConfigureTreeCancellation(cmd)
	cmd.Env = s.env(true)
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return err
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return err
	}
	if err = cmd.Start(); err != nil {
		cancel()
		return err
	}
	w := &worker{input: input, events: make(chan Event, 32), cancel: cancel}
	s.worker = w
	go func() {
		defer close(w.events)
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 1024*1024)
		for scanner.Scan() {
			var event Event
			if json.Unmarshal(scanner.Bytes(), &event) == nil {
				if event.Type == "ready" {
					s.mu.Lock()
					w.ready = true
					s.mu.Unlock()
				}
				select {
				case w.events <- event:
				case <-workerCtx.Done():
				}
			}
		}
		_ = cmd.Wait()
		s.mu.Lock()
		if s.worker == w {
			s.worker = nil
		}
		s.mu.Unlock()
	}()
	return nil
}

// Open reserves the selected worker for one streaming recording.
func (s *Service) Open(ctx context.Context) (*Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	for s.worker != nil && s.worker.resetting != nil {
		done := s.worker.resetting
		s.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		s.mu.Lock()
	}
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m, ok := findModel(s.selected)
	if s.closed || s.recording || s.installing != "" {
		return nil, errors.New("dictation is busy")
	}
	if !ok || !s.installed(m) {
		return nil, errors.New("select an installed local model in Settings → Dictation")
	}
	if err := s.ensureWorker(m); err != nil {
		return nil, err
	}

	s.recording = true
	session := &Session{svc: s, worker: s.worker}
	if _, err := io.WriteString(s.worker.input, "{\"type\":\"start\"}\n"); err != nil {
		s.recording = false
		s.stopWorker()
		return nil, err
	}
	return session, nil
}

// Events returns ordered partial and final recognition results.
func (s *Session) Events() <-chan Event { return s.worker.events }

// Audio accepts bounded 16 kHz mono little-endian float32 samples.
func (s *Session) Audio(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return errors.New("recording has finished")
	}
	s.bytes += len(data)
	if len(data) == 0 || len(data)%4 != 0 || len(data) > 64000 || s.bytes > 16000*4*300 {
		return errors.New("dictation is limited to five minutes")
	}
	return json.NewEncoder(s.worker.input).Encode(map[string]string{"type": "audio", "audio": base64.StdEncoding.EncodeToString(data)})
}

// Finish ends audio input and requests a final transcript exactly once.
func (s *Session) Finish() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ended {
		return nil
	}
	s.ended = true
	_, err := io.WriteString(s.worker.input, "{\"type\":\"stop\"}\n")
	return err
}

// Close clears cancelled recordings while retaining the loaded model.
func (s *Session) Close(completed bool) {
	s.svc.mu.Lock()
	defer s.svc.mu.Unlock()
	s.svc.recording = false
	if completed || s.svc.worker != s.worker || s.worker.resetting != nil {
		return
	}
	done := make(chan struct{})
	s.worker.resetting = done
	go s.reset(done)
}

func (s *Session) reset(done chan struct{}) {
	// Killing a stuck worker also interrupts a blocked pipe write.
	timeout := time.AfterFunc(2500*time.Millisecond, s.worker.cancel)
	s.mu.Lock()
	s.ended = true
	_, err := io.WriteString(s.worker.input, "{\"type\":\"cancel\"}\n")
	s.mu.Unlock()
	reset := false
	if err == nil {
		for event := range s.worker.events {
			if event.Type == "cancelled" {
				reset = true
				break
			}
			if event.Type == "error" {
				break
			}
		}
	}
	timely := timeout.Stop()
	s.svc.mu.Lock()
	defer s.svc.mu.Unlock()
	if s.svc.worker == s.worker {
		s.worker.resetting = nil
		if !reset || !timely {
			s.svc.stopWorker()
			s.svc.prepare()
		}
	}
	close(done)
}

// progressScale is the whole installation: ProgressDone runs from 0 up to it.
const progressScale = 1000

// planInstall splits the installation into steps sized by weight. The returned
// func starts the next step, so progress moves through one continuous range
// instead of restarting for each download.
func (s *Service) planInstall(weights ...int64) func(stage string) {
	var sum int64
	for _, w := range weights {
		sum += w
	}
	bounds := make([]int64, 1, len(weights)+1)
	var acc int64
	for _, w := range weights {
		acc += w
		bounds = append(bounds, acc*progressScale/sum)
	}
	next := 0
	return func(stage string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		i := min(next, len(weights)-1)
		next++
		s.stage, s.stepFrom, s.stepTo = stage, bounds[i], bounds[i+1]
		s.progressDone, s.progressTotal = max(s.progressDone, s.stepFrom), progressScale
	}
}

// stepProgress moves the current step to the given fraction. It never moves back.
func (s *Service) stepProgress(fraction float64) {
	fraction = min(max(fraction, 0), 1)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.stepTo == 0 {
		return
	}
	s.progressDone = max(s.progressDone, s.stepFrom+int64(fraction*float64(s.stepTo-s.stepFrom)))
}

// every runs fn on a timer until the returned func is called.
func every(interval time.Duration, fn func()) func() {
	stop, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				fn()
			}
		}
	}()
	return func() { close(stop); <-done }
}

// easeStep advances a step that has nothing to measure. It slows as it nears
// the end of the step and never reaches it, so the bar keeps moving without
// claiming to be done before the work is.
func (s *Service) easeStep(tau time.Duration) func() {
	start := time.Now()
	return every(250*time.Millisecond, func() {
		s.stepProgress(0.95 * (1 - math.Exp(-float64(time.Since(start))/float64(tau))))
	})
}

// watchBlobs reports how much of total has landed in dir while a helper process
// downloads into it. The returned func stops the watcher.
func (s *Service) watchBlobs(dir string, total int64) func() {
	baseline := dirSize(dir)
	return every(500*time.Millisecond, func() {
		if total > 0 {
			s.stepProgress(float64(max(dirSize(dir)-baseline, 0)) / float64(total))
		}
	})
}

func dirSize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // A file vanishing mid-walk only makes the estimate smaller.
		}
		if info, err := entry.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
		}
		return nil
	})
	return size
}
func (s *Service) run(ctx context.Context, name string, args ...string) error {
	cmd := process.CommandContext(ctx, name, args...)
	process.ConfigureTreeCancellation(cmd)
	cmd.Env = s.env(false)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s failed: %w", filepath.Base(name), err)
	}
	return nil
}
