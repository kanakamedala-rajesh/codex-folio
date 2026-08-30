//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"unsafe"
)

const (
	accessAllowedAceType = 0
	aclSizeInformation   = 2
	trusteeIsGroup       = 2
)

type windowsACLSizeInformation struct {
	aceCount      uint32
	aclBytesInUse uint32
	aclBytesFree  uint32
}

type windowsACEHeader struct {
	aceType  byte
	aceFlags byte
	aceSize  uint16
}

var (
	getNamedSecurityInfoForTest = syscall.NewLazyDLL("advapi32.dll").NewProc("GetNamedSecurityInfoW")
	getAclInformationForTest    = syscall.NewLazyDLL("advapi32.dll").NewProc("GetAclInformation")
	getAceForTest               = syscall.NewLazyDLL("advapi32.dll").NewProc("GetAce")
)

func TestAcquireRemediatesUnsafeWindowsDirectoryACL(t *testing.T) {
	root := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := setEveryoneFullControlForTest(root); err != nil {
		t.Fatalf("set unsafe DACL: %v", err)
	}
	if hasEveryone, err := windowsPathHasEveryoneACE(root); err != nil {
		t.Fatalf("inspect unsafe DACL: %v", err)
	} else if !hasEveryone {
		t.Fatal("unsafe DACL setup did not grant Everyone access")
	}

	ownerHome := filepath.Join(t.TempDir(), "owner-home")
	override := root
	paths, err := ResolvePaths(PathOptions{
		Platform:          PlatformWindows,
		HomeDir:           ownerHome,
		OwnerHomeDir:      ownerHome,
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	owner, err := Acquire(paths, OwnerOptions{})
	if err != nil {
		t.Fatalf("Acquire() error = %v", err)
	}
	defer func() { _ = owner.Close() }()

	if hasEveryone, err := windowsPathHasEveryoneACE(root); err != nil {
		t.Fatalf("inspect remediated DACL: %v", err)
	} else if hasEveryone {
		t.Fatal("remediated state directory still grants access to Everyone")
	}
}

func setEveryoneFullControlForTest(path string) error {
	everyoneSID, err := syscall.StringToSid("S-1-1-0")
	if err != nil {
		return err
	}
	entry := windowsExplicitAccess{
		accessPermissions: genericAll,
		accessMode:        setAccess,
		inheritance:       subcontainersAndObjectsInherit,
		trustee: windowsTrustee{
			trusteeForm: trusteeIsSID,
			trusteeType: trusteeIsGroup,
			name:        uintptr(unsafe.Pointer(everyoneSID)),
		},
	}
	var acl uintptr
	result, _, _ := setEntriesInACLProc.Call(
		1,
		uintptr(unsafe.Pointer(&entry)),
		0,
		uintptr(unsafe.Pointer(&acl)),
	)
	if result != 0 {
		return syscall.Errno(result)
	}
	defer func() { _, _, _ = localFreeProc.Call(acl) }()

	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	result, _, _ = setNamedSecurityInfoProc.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		securityObjectFile,
		daclSecurityInformation|protectedDACLSecurityInformation,
		0,
		0,
		acl,
		0,
	)
	if result != 0 {
		return syscall.Errno(result)
	}
	return nil
}

func windowsPathHasEveryoneACE(path string) (bool, error) {
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return false, err
	}
	var dacl uintptr
	var descriptor uintptr
	result, _, callErr := getNamedSecurityInfoForTest.Call(
		uintptr(unsafe.Pointer(pathPointer)),
		securityObjectFile,
		daclSecurityInformation,
		0,
		0,
		uintptr(unsafe.Pointer(&dacl)),
		0,
		uintptr(unsafe.Pointer(&descriptor)),
	)
	if result != 0 {
		return false, syscall.Errno(result)
	}
	if descriptor != 0 {
		defer func() { _, _, _ = localFreeProc.Call(descriptor) }()
	}
	if dacl == 0 {
		return false, nil
	}

	var aclInfo windowsACLSizeInformation
	result, _, callErr = getAclInformationForTest.Call(
		dacl,
		uintptr(unsafe.Pointer(&aclInfo)),
		unsafe.Sizeof(aclInfo),
		aclSizeInformation,
	)
	if result == 0 {
		if callErr == nil {
			callErr = errors.New("GetAclInformation failed")
		}
		return false, callErr
	}

	for index := uint32(0); index < aclInfo.aceCount; index++ {
		var ace *byte
		result, _, callErr = getAceForTest.Call(dacl, uintptr(index), uintptr(unsafe.Pointer(&ace)))
		if result == 0 {
			if callErr == nil {
				callErr = errors.New("GetAce failed")
			}
			return false, callErr
		}
		header := (*windowsACEHeader)(unsafe.Pointer(ace))
		if header.aceType != accessAllowedAceType || header.aceSize < 8 {
			continue
		}
		aceSID := (*syscall.SID)(unsafe.Add(unsafe.Pointer(ace), 8))
		var sidText *uint16
		if err := syscall.ConvertSidToStringSid(aceSID, &sidText); err != nil {
			return false, err
		}
		sid := syscall.UTF16ToString((*[1 << 20]uint16)(unsafe.Pointer(sidText))[:])
		_, _ = syscall.LocalFree(syscall.Handle(unsafe.Pointer(sidText)))
		if sid == "S-1-1-0" {
			return true, nil
		}
	}
	return false, nil
}
