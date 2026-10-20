package host

import "syscall"

// DieWithParent has the kernel send sig to the process attr starts when its
// parent dies, however it dies.
func DieWithParent(attr *syscall.SysProcAttr, sig syscall.Signal) {
	attr.Pdeathsig = sig
}
