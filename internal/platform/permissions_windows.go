//go:build windows

package platform

import (
	"errors"
	"syscall"
	"unsafe"
)

const (
	securityObjectFile               = 1
	daclSecurityInformation          = 0x00000004
	protectedDACLSecurityInformation = 0x80000000
	setAccess                        = 2
	subcontainersAndObjectsInherit   = 0x00000003
	trusteeIsSID                     = 0
	trusteeIsUser                    = 1
	genericAll                       = 0x10000000
)

type windowsTrustee struct {
	multipleTrustee          uintptr
	multipleTrusteeOperation uint32
	trusteeForm              uint32
	trusteeType              uint32
	name                     uintptr
}

type windowsExplicitAccess struct {
	accessPermissions uint32
	accessMode        uint32
	inheritance       uint32
	trustee           windowsTrustee
}

var (
	advapi32                 = syscall.NewLazyDLL("advapi32.dll")
	setEntriesInACLProc      = advapi32.NewProc("SetEntriesInAclW")
	setNamedSecurityInfoProc = advapi32.NewProc("SetNamedSecurityInfoW")
	kernel32ForPermissions   = syscall.NewLazyDLL("kernel32.dll")
	localFreeProc            = kernel32ForPermissions.NewProc("LocalFree")
)

func enforcePrivatePermissions(path string) error {
	token, err := syscall.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer func() { _ = token.Close() }()

	user, err := token.GetTokenUser()
	if err != nil {
		return err
	}
	if user == nil || user.User.Sid == nil {
		return errors.New("current token has no user SID")
	}

	entry := windowsExplicitAccess{
		accessPermissions: genericAll,
		accessMode:        setAccess,
		inheritance:       subcontainersAndObjectsInherit,
		trustee: windowsTrustee{
			trusteeForm: trusteeIsSID,
			trusteeType: trusteeIsUser,
			name:        uintptr(unsafe.Pointer(user.User.Sid)),
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
	defer func() {
		_, _, _ = localFreeProc.Call(acl)
	}()

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
