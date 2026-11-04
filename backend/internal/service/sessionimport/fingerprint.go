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
	importer := harnessImporters[source.harness]
	var files []sourceFiles
	var err error
	switch {
	case importer.discoverRegistered != nil:
		files, err = importer.discoverRegistered(ctx, projects)
		files = registeredSources(files, projects)
	case importer.discoverWithProjects != nil:
		files, err = importer.discoverWithProjects(ctx, source.root, projects)
	default:
		files, err = s.discover(ctx, source.harness, source.root)
	}
	if err != nil {
		return err
	}
	found := false
	for _, file := range files {
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
