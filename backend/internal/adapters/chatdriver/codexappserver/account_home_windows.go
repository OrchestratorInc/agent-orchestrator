//go:build windows

package codexappserver

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

type accountHomeACLHeader struct {
	revision byte
	padding  byte
	size     uint16
	count    uint16
	padding2 uint16
}

type accountHomeACEPrefix struct {
	header windows.ACE_HEADER
	mask   windows.ACCESS_MASK
}

const (
	accountHomeAccessDeniedObjectACEType         uint8 = 0x06
	accountHomeAccessDeniedCallbackACEType       uint8 = 0x0a
	accountHomeAccessDeniedCallbackObjectACEType uint8 = 0x0c
)

func managedHomePrivate(path string, _ os.FileInfo) bool {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return false
	}
	handle, err := windows.CreateFile(
		ptr,
		windows.READ_CONTROL,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return false
	}
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	administrators, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false
	}
	sd, err := windows.GetSecurityInfo(handle, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil || sd == nil {
		return false
	}
	owner, _, err := sd.Owner()
	if err != nil || owner == nil || !owner.Equals(user.User.Sid) {
		return false
	}
	control, _, err := sd.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		return false
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return false
	}
	return managedHomeDACLPrivate(dacl, user.User.Sid, system, administrators)
}

func managedHomeDACLPrivate(dacl *windows.ACL, user, system, administrators *windows.SID) bool {
	header := (*accountHomeACLHeader)(unsafe.Pointer(dacl))
	for index := uint32(0); index < uint32(header.count); index++ {
		var raw *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &raw); err != nil || raw == nil {
			return false
		}
		prefix := (*accountHomeACEPrefix)(unsafe.Pointer(raw))
		if prefix.header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch prefix.header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if uint32(prefix.mask) == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&raw.SidStart))
			if !sid.IsValid() || (!sid.Equals(user) && !sid.Equals(system) && !sid.Equals(administrators)) {
				return false
			}
		case windows.ACCESS_DENIED_ACE_TYPE,
			accountHomeAccessDeniedObjectACEType,
			accountHomeAccessDeniedCallbackACEType,
			accountHomeAccessDeniedCallbackObjectACEType:
			continue
		default:
			return false
		}
	}
	return true
}
