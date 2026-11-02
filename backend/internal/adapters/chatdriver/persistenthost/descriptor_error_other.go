//go:build !windows

package persistenthost

func isDescriptorSharingViolation(error) bool {
	return false
}
