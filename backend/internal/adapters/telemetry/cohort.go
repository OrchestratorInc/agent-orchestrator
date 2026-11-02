package telemetry

import (
	"crypto/rand"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// experimentCohortFile holds this install's experiment bucket, 0-99. It is
// assigned once, on the first run that reaches it (daemon or desktop, whichever
// is first), and never changes, so an onboarding test can split installs by
// bucket range without PostHog feature flags. The desktop app reads and creates
// the same file (frontend/src/shared/telemetry.ts).
const experimentCohortFile = "telemetry_experiment_cohort"

// experimentCohortBuckets is the bucket count: experiment_cohort is in [0, 100).
const experimentCohortBuckets = 100

// loadOrAssignExperimentCohort returns this install's bucket, assigning one if
// none exists. ok is false when the bucket cannot be read or stored, and the
// property is then omitted rather than guessed.
func loadOrAssignExperimentCohort(dataDir string) (int, bool) {
	if dataDir == "" {
		return 0, false
	}
	file := filepath.Join(dataDir, experimentCohortFile)
	for attempt := 0; attempt < 3; attempt++ {
		if cohort, err := readExperimentCohort(file); err == nil {
			return cohort, true
		} else if !errors.Is(err, os.ErrNotExist) {
			return 0, false
		}
		n, err := rand.Int(rand.Reader, big.NewInt(experimentCohortBuckets))
		if err != nil {
			return 0, false
		}
		// Write a complete temp file, then link it into place: the link fails if
		// another process won, and a reader never sees a half-written file.
		if err := os.MkdirAll(dataDir, 0o700); err != nil {
			return 0, false
		}
		tmp, err := os.CreateTemp(dataDir, ".telemetry_experiment_cohort_*")
		if err != nil {
			return 0, false
		}
		_, writeErr := tmp.WriteString(strconv.FormatInt(n.Int64(), 10) + "\n")
		closeErr := tmp.Close()
		if writeErr == nil && closeErr == nil {
			err = os.Link(tmp.Name(), file)
		}
		_ = os.Remove(tmp.Name())
		if writeErr != nil || closeErr != nil {
			return 0, false
		}
		if err == nil {
			return int(n.Int64()), true
		}
		if !errors.Is(err, os.ErrExist) {
			return 0, false
		}
	}
	return 0, false
}

func readExperimentCohort(file string) (int, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return 0, err
	}
	cohort, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || cohort < 0 || cohort >= experimentCohortBuckets {
		return 0, errors.New("invalid experiment cohort")
	}
	return cohort, nil
}
