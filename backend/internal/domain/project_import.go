package domain

import "fmt"

const (
	ProjectImportConflictSameIDArchivedTarget      = "same id matches archived target project"
	ProjectImportConflictSameIDDifferentActivePath = "same id with different active path"
	ProjectImportConflictSamePathDifferentActiveID = "same path with different active id"
)

type ProjectImportConflict struct {
	ProjectID  string
	Path       string
	Reason     string
	TargetID   string
	TargetPath string
}

type ProjectImportConflictError struct {
	Conflict ProjectImportConflict
}

func (e *ProjectImportConflictError) Error() string {
	return fmt.Sprintf("project import conflict for %s at %s: %s", e.Conflict.ProjectID, e.Conflict.Path, e.Conflict.Reason)
}
