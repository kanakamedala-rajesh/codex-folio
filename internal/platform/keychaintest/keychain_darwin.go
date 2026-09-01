//go:build darwin && cgo

// Package keychaintest contains native Keychain inspection helpers used only
// by macOS integration tests. Production code does not import this package.
package keychaintest

/*
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
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
*/
import "C"

import (
	"fmt"
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
