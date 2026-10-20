package config

import (
	"errors"
	"os"
)

// Paths is the file-location projection of configuration. Resolving it does
// not validate unrelated environment settings, read files, or create state.
type Paths struct {
	DataDir       string
	RunFilePath   string
	DataDirSource string
	RunFileSource string
}

// ResolvePaths resolves the same paths as Load without loading daemon config.
// A failure in one path does not discard the other path.
func ResolvePaths() (Paths, error) {
	paths := Paths{DataDirSource: "default", RunFileSource: "default"}
	if os.Getenv("AO_DATA_DIR") != "" {
		paths.DataDirSource = "AO_DATA_DIR"
	}
	if os.Getenv("AO_RUN_FILE") != "" {
		paths.RunFileSource = "AO_RUN_FILE"
	}
	var dataErr, runErr error
	paths.DataDir, dataErr = resolveDataDir()
	paths.RunFilePath, runErr = resolveRunFilePath()
	return paths, errors.Join(dataErr, runErr)
}
