//go:build windows

package profile

import "strings"

func samePath(left, right string) bool { return strings.EqualFold(left, right) }
