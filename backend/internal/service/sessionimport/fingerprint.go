package sessionimport

import (
	"errors"
	"slices"
)

var errSourceFingerprintChanged = errors.New("native source changed since selection; scan again before importing")

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
