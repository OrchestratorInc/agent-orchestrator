//go:build !darwin

package process

func executable(name string) string { return name }
