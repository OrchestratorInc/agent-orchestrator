package agent

const (
	codexWindowsAttributeDirectory    uint32 = 0x00000010
	codexWindowsAttributeReparsePoint uint32 = 0x00000400
	codexWindowsOpenReparsePoint      uint32 = 0x00200000
	codexWindowsBackupSemantics       uint32 = 0x02000000
	codexWindowsMoveReplaceExisting   uint32 = 0x00000001
	codexWindowsMoveWriteThrough      uint32 = 0x00000008
	codexWindowsReadControl           uint32 = 0x00020000
	codexWindowsReadData              uint32 = 0x00000001
	codexWindowsWriteData             uint32 = 0x00000002
	codexWindowsAppendData            uint32 = 0x00000004
	codexWindowsWriteEA               uint32 = 0x00000010
	codexWindowsDeleteChild           uint32 = 0x00000040
	codexWindowsWriteAttributes       uint32 = 0x00000100
	codexWindowsDelete                uint32 = 0x00010000
	codexWindowsWriteDAC              uint32 = 0x00040000
	codexWindowsWriteOwner            uint32 = 0x00080000
	codexWindowsGenericAll            uint32 = 0x10000000
	codexWindowsGenericWrite          uint32 = 0x40000000
	codexWindowsGenericRead           uint32 = 0x80000000
)

type codexWindowsPathMetadata struct {
	Attributes    uint32
	HardLinks     uint32
	OwnerTrusted  bool
	ACLSafe       bool
	VolumeSerial  uint32
	FileIndexHigh uint32
	FileIndexLow  uint32
}

func codexWindowsNoFollowOpenFlags() uint32 {
	return codexWindowsOpenReparsePoint | codexWindowsBackupSemantics
}

func codexWindowsAtomicReplaceFlags() uint32 {
	return codexWindowsMoveReplaceExisting | codexWindowsMoveWriteThrough
}

func codexWindowsDirectoryFlushAccess() uint32 {
	return codexWindowsGenericWrite | codexWindowsReadControl
}

func codexWindowsPathMetadataIsSafe(metadata codexWindowsPathMetadata, directory, requireSingleLink bool) bool {
	if metadata.Attributes&codexWindowsAttributeReparsePoint != 0 || (metadata.Attributes&codexWindowsAttributeDirectory != 0) != directory || !metadata.OwnerTrusted || !metadata.ACLSafe {
		return false
	}
	return !requireSingleLink || metadata.HardLinks == 1
}

func codexWindowsSameStableIdentity(left, right codexWindowsPathMetadata) bool {
	return left.VolumeSerial == right.VolumeSerial && left.FileIndexHigh == right.FileIndexHigh && left.FileIndexLow == right.FileIndexLow
}

const codexWindowsMutationMask = codexWindowsWriteData | codexWindowsAppendData | codexWindowsWriteEA |
	codexWindowsDeleteChild | codexWindowsWriteAttributes | codexWindowsDelete | codexWindowsWriteDAC |
	codexWindowsWriteOwner | codexWindowsGenericAll | codexWindowsGenericWrite

const codexWindowsAncestorMutationMask = codexWindowsMutationMask &^ (codexWindowsWriteData | codexWindowsAppendData | codexWindowsWriteEA | codexWindowsWriteAttributes)

type codexWindowsACE struct {
	Allowed           bool
	PrincipalTrusted  bool
	PrincipalUnmapped bool
	PrincipalSandbox  bool
	Mask              uint32
}

func codexWindowsVaultACLIsSafe(ownerTrusted bool, aces []codexWindowsACE) bool {
	if !ownerTrusted {
		return false
	}
	for _, ace := range aces {
		if ace.Allowed && !ace.PrincipalTrusted && ace.Mask != 0 {
			return false
		}
	}
	return true
}

func codexWindowsAncestorACLIsSafe(aces []codexWindowsACE) bool {
	for _, ace := range aces {
		if ace.Allowed && !ace.PrincipalTrusted && ace.Mask&codexWindowsAncestorMutationMask != 0 {
			return false
		}
	}
	return true
}

func codexWindowsDeviceCredentialACLIsSafe(ownerTrusted bool, aces []codexWindowsACE) bool {
	if !ownerTrusted {
		return false
	}
	for _, ace := range aces {
		if !ace.Allowed || ace.Mask == 0 || ace.PrincipalTrusted {
			continue
		}
		if ace.PrincipalSandbox && ace.Mask&codexWindowsMutationMask == 0 {
			continue
		}
		return false
	}
	return true
}

const codexWindowsLegacyStaleSIDMask uint32 = 0x001201BF

func codexWindowsDeviceCredentialACLIsRepairable(ownerCurrent bool, aces []codexWindowsACE) bool {
	if !ownerCurrent {
		return false
	}
	repairNeeded := false
	for _, ace := range aces {
		if !ace.Allowed || ace.Mask == 0 || ace.PrincipalTrusted {
			continue
		}
		if ace.PrincipalSandbox {
			if ace.Mask&codexWindowsMutationMask != 0 {
				return false
			}
			continue
		}
		if !ace.PrincipalUnmapped || ace.Mask&^codexWindowsLegacyStaleSIDMask != 0 {
			return false
		}
		repairNeeded = true
	}
	return repairNeeded
}
