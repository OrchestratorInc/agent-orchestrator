package sessionimport

import (
	"context"
	"errors"
	"slices"
)

var errSourceFingerprintChanged = errors.New("native source changed since selection; scan again before importing")

// checkSourceFingerprint adds no reads for importers without fingerprints.
// Provider discovery recomputes its evidence; this never invents a fingerprint
// from display text or assumes that the main database alone is the input.
func (s *Service) checkSourceFingerprint(ctx context.Context, source transcript, projects []string) error {
	if source.fingerprint == "" {
		return nil
	}
	var d discovery
	d.files, d.err = s.discoverFor(ctx, source.harness, source.root, projects)
	return d.match(source)
}

// discovery is one provider store's discovered sources, shared by every
// selected chat from that store.
type discovery struct {
	files []sourceFiles
	err   error
}

// match reports whether the store still holds the source's selected history.
func (d discovery) match(source transcript) error {
	if source.fingerprint == "" {
		return nil
	}
	if d.err != nil {
		return d.err
	}
	found := false
	for _, file := range d.files {
		if file.nativeID() != source.nativeID || canonical(file.root) != canonical(source.root) {
			continue
		}
		if len(source.paths) > 0 && !slices.Equal(file.paths, source.paths) {
			continue
		}
		if len(source.paths) == 0 && source.path != "" && !slices.Contains(file.paths, source.path) {
			continue
		}
		if found || file.fingerprint != source.fingerprint {
			return errSourceFingerprintChanged
		}
		found = true
	}
	if !found {
		return errSourceFingerprintChanged
	}
	return nil
}
