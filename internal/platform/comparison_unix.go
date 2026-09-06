//go:build !windows

package platform

func SamePath(left, right string) bool { return left == right }

func SameEnvironmentName(left, right string) bool { return left == right }
