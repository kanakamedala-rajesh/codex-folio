//go:build windows

package main

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

type systemBootEnvironmentInformation struct {
	BootIdentifier windows.GUID
	FirmwareType   uint32
	BootFlags      uint64
}

func (foregroundProcessInspector) BootSessionID() (string, error) {
	var information systemBootEnvironmentInformation
	var returned uint32
	err := windows.NtQuerySystemInformation(
		windows.SystemBootEnvironmentInformation,
		unsafe.Pointer(&information),
		uint32(unsafe.Sizeof(information)),
		&returned,
	)
	if err != nil {
		return "", err
	}
	if information.BootIdentifier == (windows.GUID{}) {
		return "", fmt.Errorf("system boot session is unavailable")
	}
	return information.BootIdentifier.String(), nil
}
