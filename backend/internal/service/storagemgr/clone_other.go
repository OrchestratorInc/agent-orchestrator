//go:build !darwin && !linux && !windows

package storagemgr

func cloneTree(string, string) error {
	return errCloneUnsupported
}
