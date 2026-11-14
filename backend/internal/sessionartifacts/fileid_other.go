//go:build !linux && !darwin

package sessionartifacts

import (
	"io/fs"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func quickIdentity(fs.FileInfo) domain.FileIdentity { return domain.FileIdentity{} }

func fullIdentity(_ string, info fs.FileInfo) domain.FileIdentity { return quickIdentity(info) }
