//go:build darwin && cgo

// Package keychaintest contains native Keychain inspection helpers used only
// by macOS integration tests. Production code does not import this package.
package keychaintest

/*
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <Security/SecAccess.h>
#include <Security/SecACL.h>
#include <stdlib.h>
#include <string.h>

static int read_codex_folio_keychain_record(const char *service, const char *account,
	unsigned char **output, size_t *output_length) {
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return errSecAllocate;
	}
	const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnData, kSecMatchLimit};
	const void *query_values[] = {kSecClassGenericPassword, service_value, account_value, kCFBooleanTrue, kSecMatchLimitOne};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, query_keys, query_values, 5,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(service_value);
	CFRelease(account_value);
	if (query == NULL) return errSecAllocate;
	CFTypeRef result = NULL;
	OSStatus status = SecItemCopyMatching(query, &result);
	CFRelease(query);
	if (status != errSecSuccess) {
		if (result != NULL) CFRelease(result);
		return status;
	}
	if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
		if (result != NULL) CFRelease(result);
		return errSecDecode;
	}
	CFDataRef data = (CFDataRef)result;
	CFIndex length = CFDataGetLength(data);
	if (length <= 0 || length > 65536) {
		CFRelease(result);
		return errSecDecode;
	}
	unsigned char *copy = (unsigned char *)malloc((size_t)length);
	if (copy == NULL) {
		CFRelease(result);
		return errSecAllocate;
	}
	memcpy(copy, CFDataGetBytePtr(data), (size_t)length);
	CFRelease(result);
	*output = copy;
	*output_length = (size_t)length;
	return errSecSuccess;
}

static int delete_codex_folio_keychain_record(const char *service, const char *account) {
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return errSecAllocate;
	}
	const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount};
	const void *query_values[] = {kSecClassGenericPassword, service_value, account_value};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, query_keys, query_values, 3,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(service_value);
	CFRelease(account_value);
	if (query == NULL) return errSecAllocate;
	OSStatus status = SecItemDelete(query);
	CFRelease(query);
	return status == errSecItemNotFound ? errSecSuccess : status;
}

static int configure_codex_folio_test_keychain(const char *path, const char *password) {
	if (path == NULL || password == NULL) return errSecParam;
	SecKeychainRef keychain = NULL;
	OSStatus status = SecKeychainOpen(path, &keychain);
	if (status != errSecSuccess) return status;

	const void *items[] = {keychain};
	CFArrayRef search_list = CFArrayCreate(kCFAllocatorDefault, items, 1, &kCFTypeArrayCallBacks);
	if (search_list == NULL) {
		CFRelease(keychain);
		return errSecAllocate;
	}
	status = SecKeychainSetSearchList(search_list);
	CFRelease(search_list);
	if (status == errSecSuccess) status = SecKeychainSetDefault(keychain);
	if (status == errSecSuccess) {
		status = SecKeychainUnlock(keychain, (UInt32)strlen(password), password, true);
	}
	CFRelease(keychain);
	return status;
}

static int lock_codex_folio_default_keychain(void) {
	SecKeychainRef keychain = NULL;
	OSStatus status = SecKeychainCopyDefault(&keychain);
	if (status == errSecSuccess) {
		status = SecKeychainLock(keychain);
		CFRelease(keychain);
	}
	return status;
}

static int unlock_codex_folio_default_keychain(const char *password) {
	SecKeychainRef keychain = NULL;
	OSStatus status = SecKeychainCopyDefault(&keychain);
	if (status == errSecSuccess) {
		status = SecKeychainUnlock(keychain, (UInt32)strlen(password), password, true);
		CFRelease(keychain);
	}
	return status;
}

static int set_codex_folio_keychain_item_trust(const char *service, const char *account, Boolean trust_all) {
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return errSecParam;
	}
	const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecReturnRef, kSecMatchLimit};
	const void *values[] = {kSecClassGenericPassword, service_value, account_value, kCFBooleanTrue, kSecMatchLimitOne};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 5,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(service_value);
	CFRelease(account_value);
	if (query == NULL) return errSecAllocate;
	SecKeychainItemRef item = NULL;
	OSStatus status = SecItemCopyMatching(query, (CFTypeRef *)&item);
	CFRelease(query);
	if (status != errSecSuccess) return status;
	SecAccessRef access = NULL;
	status = SecKeychainItemCopyAccess(item, &access);
	if (status == errSecSuccess) {
		CFArrayRef acls = SecAccessCopyMatchingACLList(access, kSecACLAuthorizationDecrypt);
		if (acls == NULL) {
			status = errSecInvalidACL;
		} else {
			CFArrayRef applications = trust_all ? NULL : CFArrayCreate(kCFAllocatorDefault, NULL, 0, &kCFTypeArrayCallBacks);
			SecKeychainPromptSelector prompt_selector = trust_all ? 0 : kSecKeychainPromptRequirePassphase;
			CFIndex count = CFArrayGetCount(acls);
			if (count == 0) status = errSecInvalidACL;
			for (CFIndex index = 0; status == errSecSuccess && index < count; index++) {
				SecACLRef acl = (SecACLRef)CFArrayGetValueAtIndex(acls, index);
				status = SecACLSetContents(acl, applications, CFSTR("CodexFolio native test"), prompt_selector);
			}
			if (status == errSecSuccess) status = SecKeychainItemSetAccess(item, access);
			if (applications != NULL) CFRelease(applications);
			CFRelease(acls);
		}
		CFRelease(access);
	}
	CFRelease(item);
	return status;
}
*/
import "C"

import (
	"fmt"
	"os"
	"unsafe"
)

// Read returns the raw record stored under the production Keychain identity.
func Read(service, account string) ([]byte, error) {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var output *C.uchar
	var outputLength C.size_t
	status := C.read_codex_folio_keychain_record(serviceValue, accountValue, &output, &outputLength)
	if int(status) != 0 {
		return nil, fmt.Errorf("Security.framework status %d", int(status))
	}
	defer func() {
		if output != nil && outputLength > 0 {
			C.memset(unsafe.Pointer(output), 0, outputLength)
		}
		C.free(unsafe.Pointer(output))
	}()
	return C.GoBytes(unsafe.Pointer(output), C.int(outputLength)), nil
}

// Delete removes the raw record stored under the production Keychain identity.
func Delete(service, account string) error {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	status := C.delete_codex_folio_keychain_record(serviceValue, accountValue)
	if int(status) != 0 {
		return fmt.Errorf("Security.framework status %d", int(status))
	}
	return nil
}

// ConfigureFromEnvironment selects and unlocks the test-only Keychain named by
// pathEnv. An unset path leaves the developer's normal user Keychain intact.
func ConfigureFromEnvironment(pathEnv string) error {
	path := os.Getenv(pathEnv)
	if path == "" {
		return nil
	}
	password := os.Getenv("CODEX_FOLIO_TEST_KEYCHAIN_PASSWORD")
	if password == "" {
		return fmt.Errorf("%s is set but CODEX_FOLIO_TEST_KEYCHAIN_PASSWORD is empty", pathEnv)
	}
	return Configure(path, password)
}

// Configure selects and unlocks a test-only file-based Keychain for the
// current test process.
func Configure(path, password string) error {
	pathValue := C.CString(path)
	passwordValue := C.CString(password)
	defer C.free(unsafe.Pointer(pathValue))
	defer C.free(unsafe.Pointer(passwordValue))
	if status := C.configure_codex_folio_test_keychain(pathValue, passwordValue); int(status) != 0 {
		return fmt.Errorf("Security.framework status %d", int(status))
	}
	return nil
}

// LockDefault locks the current process's default file-based Keychain.
func LockDefault() error {
	if status := C.lock_codex_folio_default_keychain(); int(status) != 0 {
		return fmt.Errorf("Security.framework status %d", int(status))
	}
	return nil
}

// UnlockDefault unlocks the current user's default login Keychain with the
// ephemeral test-runner password.
func UnlockDefault(password string) error {
	passwordValue := C.CString(password)
	defer C.free(unsafe.Pointer(passwordValue))
	if status := C.unlock_codex_folio_default_keychain(passwordValue); int(status) != 0 {
		return fmt.Errorf("Security.framework status %d", int(status))
	}
	return nil
}

// SetItemTrustAll changes the ACL of a test item so native reads are either
// allowed or denied without involving a user prompt.
func SetItemTrustAll(service, account string, trustAll bool) error {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	trustAllValue := C.Boolean(0)
	if trustAll {
		trustAllValue = C.Boolean(1)
	}
	if status := C.set_codex_folio_keychain_item_trust(serviceValue, accountValue, trustAllValue); int(status) != 0 {
		return fmt.Errorf("Security.framework status %d", int(status))
	}
	return nil
}
