//go:build !windows

package profile

func samePath(left, right string) bool { return left == right }
