package systeminstall

import (
	"bytes"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

type ProgressPhase string

const (
	ProgressDownloading ProgressPhase = "downloading"
	ProgressInstalling  ProgressPhase = "installing"
)

type Progress struct {
	Phase   ProgressPhase `json:"phase,omitempty" enum:"downloading,installing" description:"Installer phase the output names. Absent until a line names one."`
	Percent *int          `json:"percent,omitempty" minimum:"0" maximum:"100" description:"Progress through the current phase, present only when the installer's output states sizes or counts."`
	Detail  string        `json:"detail,omitempty" description:"The installer's latest output line."`
}

const progressPublishInterval = 500 * time.Millisecond

const (
	maxProgressDetailRunes = 160
	maxProgressPending     = 4096
)

var (
	ansiEscape        = regexp.MustCompile(`\x1b(?:\[[\x30-\x3f]*[\x20-\x2f]*[\x40-\x7e]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
	uvDownloading     = regexp.MustCompile(`^Downloading (\S+) \(([\d.]+)\s*([KMGT]?i?B)\)$`)
	uvDownloaded      = regexp.MustCompile(`^Downloaded (\S+)$`)
	brewDependencies  = regexp.MustCompile(`^==> Installing dependencies for \S+: (.+)$`)
	brewDependency    = regexp.MustCompile(`^==> (?:Installing|Upgrading) \S+ dependency: `)
	brewFormula       = regexp.MustCompile(`^==> Installing \S+(?: --\S+)*$`)
	byteRatio         = regexp.MustCompile(`([\d.]+)\s*([KMGT]?i?B)\s*/\s*([\d.]+)\s*([KMGT]?i?B)`)
	percentValue      = regexp.MustCompile(`(?:^|[\s(\[])(\d{1,3}(?:\.\d+)?)\s?%`)
	installingLine    = regexp.MustCompile(`(?i)^(?:\[[a-z]+\]\s*)?(?:==> (?:Pouring|Linking|Caveats)|🍺|Installed\b|Extracting\b|Unpacking\b|Linking\b|Prepared \d+ packages?|(?:added|changed) \d+ packages?)`)
	installStartLine  = regexp.MustCompile(`(?i)^(?:\[[a-z]+\]\s*)?(?:==> )?Installing\b`)
	installTargetLine = regexp.MustCompile(`(?i)^(?:\[[a-z]+\]\s*)?installing (?:to|into)\b`)
	installedLine     = regexp.MustCompile(`(?i)\b(?:installed successfully|successfully installed|installation (?:complete|completed|verified|successful))\b`)
	downloadingLine   = regexp.MustCompile(`(?i)^(?:\[[a-z]+\]\s*)?(?:==> (?:Fetching|Downloading)|✔︎ (?:Bottle|Formula|Resource|Cask)|Downloading\b|Fetching\b|Resolved \d+ packages?)`)
)

type progressTracker struct {
	mu            sync.Mutex
	dst           io.Writer
	pending       []byte
	phase         ProgressPhase
	percent       int
	detail        string
	uvSizes       map[string]float64
	uvDone        map[string]bool
	brewSteps     int
	brewStarted   int
	brewInstalled bool
}

func newProgressTracker(dst io.Writer) *progressTracker {
	return &progressTracker{dst: dst, percent: -1}
}

func (t *progressTracker) Write(p []byte) (int, error) {
	n, err := t.dst.Write(p)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = append(t.pending, p...)
	for {
		end := bytes.IndexAny(t.pending, "\r\n")
		if end < 0 {
			break
		}
		t.observe(string(t.pending[:end]))
		t.pending = t.pending[end+1:]
	}
	if len(t.pending) > maxProgressPending {
		t.pending = append([]byte(nil), t.pending[len(t.pending)-maxProgressPending:]...)
	}
	if line := strings.TrimSpace(ansiEscape.ReplaceAllString(string(t.pending), "")); line != "" {
		if percent, ok := reportedPercent(line); ok {
			t.percent = percent
			t.detail = truncateRunes(line, maxProgressDetailRunes)
		}
	}
	return n, err
}

func (t *progressTracker) Snapshot() *Progress {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.phase == "" && t.detail == "" && t.percent < 0 {
		return nil
	}
	progress := &Progress{Phase: t.phase, Detail: t.detail}
	if t.percent >= 0 {
		percent := t.percent
		progress.Percent = &percent
	}
	return progress
}

func (t *progressTracker) observe(raw string) {
	line := strings.TrimSpace(ansiEscape.ReplaceAllString(raw, ""))
	if line == "" {
		return
	}
	t.detail = truncateRunes(line, maxProgressDetailRunes)

	if match := uvDownloading.FindStringSubmatch(line); match != nil {
		if t.uvSizes == nil {
			t.uvSizes, t.uvDone = map[string]float64{}, map[string]bool{}
		}
		t.uvSizes[match[1]] = byteSize(match[2], match[3])
		t.setPhase(ProgressDownloading)
		t.percent = t.uvPercent()
		return
	}
	if match := uvDownloaded.FindStringSubmatch(line); match != nil && t.uvSizes != nil {
		t.uvDone[match[1]] = true
		t.percent = t.uvPercent()
		return
	}
	if match := brewDependencies.FindStringSubmatch(line); match != nil {
		t.setPhase(ProgressInstalling)
		t.brewSteps = len(splitSentence(match[1])) + 1
		t.percent = 0
		return
	}
	if brewDependency.MatchString(line) && t.brewSteps > 0 {
		t.setPhase(ProgressInstalling)
		t.percent = min(100, t.brewStarted*100/t.brewSteps)
		t.brewStarted++
		return
	}
	if brewFormula.MatchString(line) && t.brewSteps > 0 && !t.brewInstalled {
		t.brewInstalled = true
		t.setPhase(ProgressInstalling)
		t.percent = (t.brewSteps - 1) * 100 / t.brewSteps
		return
	}
	if percent, ok := reportedPercent(line); ok {
		t.percent = percent
		return
	}
	if installTargetLine.MatchString(line) {
		return
	}
	if installingLine.MatchString(line) || installedLine.MatchString(line) ||
		(installStartLine.MatchString(line) && t.phase == ProgressDownloading) {
		if t.setPhase(ProgressInstalling) && t.brewSteps == 0 {
			t.percent = -1
		}
		return
	}
	if downloadingLine.MatchString(line) {
		t.setPhase(ProgressDownloading)
	}
}

func (t *progressTracker) setPhase(phase ProgressPhase) bool {
	if t.phase == phase || (t.phase == ProgressInstalling && phase == ProgressDownloading) {
		return false
	}
	t.phase = phase
	return true
}

func (t *progressTracker) uvPercent() int {
	var total, done float64
	for name, size := range t.uvSizes {
		total += size
		if t.uvDone[name] {
			done += size
		}
	}
	if total <= 0 {
		return -1
	}
	return int(done * 100 / total)
}

func reportedPercent(line string) (int, bool) {
	if match := byteRatio.FindStringSubmatch(line); match != nil {
		done, total := byteSize(match[1], match[2]), byteSize(match[3], match[4])
		if total > 0 && done <= total {
			return int(done * 100 / total), true
		}
	}
	matches := percentValue.FindAllStringSubmatch(line, -1)
	if len(matches) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(matches[len(matches)-1][1], 64)
	if err != nil || value > 100 {
		return 0, false
	}
	return int(value), true
}

func byteSize(value, unit string) float64 {
	number, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0
	}
	scale := 1.0
	switch strings.TrimSuffix(strings.TrimSuffix(unit, "B"), "i") {
	case "K":
		scale = 1 << 10
	case "M":
		scale = 1 << 20
	case "G":
		scale = 1 << 30
	case "T":
		scale = 1 << 40
	}
	return number * scale
}

func splitSentence(list string) []string {
	var names []string
	for _, part := range strings.Split(strings.ReplaceAll(list, " and ", ", "), ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit-1]) + "…"
}

func (s *Service) publishAgentProgress(job *Job, out *capturedOutput, tracker *progressTracker) (stop func()) {
	s.mu.Lock()
	base := job.Output
	s.mu.Unlock()
	publish := func() {
		output, progress := out.String(), tracker.Snapshot()
		now := time.Now().UTC()
		s.mu.Lock()
		if job.Status == StatusInstalling {
			job.Output = combineOutput(base, output)
			job.Progress = progress
			job.UpdatedAt = &now
		}
		s.mu.Unlock()
	}
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(progressPublishInterval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				publish()
			}
		}
	}()
	return func() {
		close(done)
		<-finished
		s.mu.Lock()
		job.Output = base
		job.Progress = nil
		s.mu.Unlock()
	}
}
