package shellterm

import (
	"crypto/rand"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// newCueCommandDirectory 只以当前进程的查询令牌取得 SID，为命令目录设置独占 DACL。
// 伪令牌无须关闭，不申请额外权限或依赖线程模拟身份。
func newCueCommandDirectory(root string) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user.User.Sid.String() + ")")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "command-"+rand.Text())
	ptr, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	if err := windows.CreateDirectory(ptr, &attributes); err != nil {
		return "", err
	}
	return dir, nil
}
