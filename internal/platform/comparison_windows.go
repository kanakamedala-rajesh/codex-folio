//go:build windows

package platform

import "strings"

func SamePath(left, right string) bool { return strings.EqualFold(left, right) }

func SameEnvironmentName(left, right string) bool { return strings.EqualFold(left, right) }
