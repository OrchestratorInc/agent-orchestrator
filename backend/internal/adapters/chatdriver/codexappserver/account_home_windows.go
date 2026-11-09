//go:build windows

package codexappserver

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// accountHomeACLHeader 按 Win32 ACL 的 8 字节头布局读取 ACE 数量。
type accountHomeACLHeader struct {
	revision byte
	padding  byte
	size     uint16
	count    uint16
	padding2 uint16
}

// accountHomeACEPrefix 只读取各 ACE 共用的头部和访问掩码；SID 由类型决定。
type accountHomeACEPrefix struct {
	header windows.ACE_HEADER
	mask   windows.ACCESS_MASK
}

const (
	accountHomeAccessDeniedObjectACEType         uint8 = 0x06
	accountHomeAccessDeniedCallbackACEType       uint8 = 0x0a
	accountHomeAccessDeniedCallbackObjectACEType uint8 = 0x0c
)

// managedHomePrivate reports whether an AO-owned Codex credential home is still
// private to this user.
//
// Windows has no POSIX mode bits: a directory created with os.Mkdir(path, 0o700)
// is reported back by Lstat as 0777, so the Unix mode comparison can never
// succeed here and every managed-home open failed. AO instead protects a managed
// home with an explicit owner-only DACL when it creates it, so that is what gets
// verified: the current user owns the directory, its DACL is protected from
// inheritance, and no untrusted principal holds any allow ACE on it.
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

	// 只查询当前进程 SID；TOKEN_QUERY 伪令牌不归调用方关闭。
	token := windows.GetCurrentProcessToken()
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

// managedHomeDACLPrivate 只接受当前用户和系统主体的有效授权，其他有效 ACE 失败关闭。
func managedHomeDACLPrivate(dacl *windows.ACL, user, system, administrators *windows.SID) bool {
	header := (*accountHomeACLHeader)(unsafe.Pointer(dacl)) // #nosec G103 -- DACL 来自 Windows 有效安全描述符，Go 堆内存由 dacl 保持可达；结构匹配 ACL 头。
	for index := uint32(0); index < uint32(header.count); index++ {
		var raw *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &raw); err != nil || raw == nil {
			return false
		}
		prefix := (*accountHomeACEPrefix)(unsafe.Pointer(raw)) // #nosec G103 -- GetAce 返回同一 DACL 内的有效 ACE；这里只读共用头和掩码，未知有效类型仍拒绝。
		if prefix.header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
			continue
		}
		switch prefix.header.AceType {
		case windows.ACCESS_ALLOWED_ACE_TYPE:
			if uint32(prefix.mask) == 0 {
				continue
			}
			sid := (*windows.SID)(unsafe.Pointer(&raw.SidStart)) // #nosec G103 -- 仅 ACCESS_ALLOWED_ACE_TYPE 使用此 SID 偏移；raw 保持缓冲可达，随后验证 SID。
			if !sid.IsValid() || (!sid.Equals(user) && !sid.Equals(system) && !sid.Equals(administrators)) {
				return false
			}
		case windows.ACCESS_DENIED_ACE_TYPE,
			accountHomeAccessDeniedObjectACEType,
			accountHomeAccessDeniedCallbackACEType,
			accountHomeAccessDeniedCallbackObjectACEType:
			// Deny ACEs cannot make the managed home less private.
			continue
		default:
			// Only the simple allow layout above is decoded. Fail closed for
			// every other effective ACE instead of guessing where its SID lives.
			return false
		}
	}
	return true
}
