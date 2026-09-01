//go:build darwin && cgo

package platform

/*
#cgo darwin LDFLAGS: -framework Security -framework CoreFoundation
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

// The traditional user login Keychain is used here instead of the data
// protection Keychain. The latter requires an application entitlement that
// unsigned command-line binaries, including the native CI test binary, do
// not have. The login Keychain remains user-scoped and its default item
// accessibility is When Unlocked.
static CFDictionaryRef codex_folio_keychain_identity_query(CFStringRef service, CFStringRef account,
	Boolean returnData, CFArrayRef searchList) {
	const void *keys[6];
	const void *values[6];
	CFIndex count = 3;
	keys[0] = kSecClass;
	values[0] = kSecClassGenericPassword;
	keys[1] = kSecAttrService;
	values[1] = service;
	keys[2] = kSecAttrAccount;
	values[2] = account;
	if (searchList != NULL) {
		keys[count] = kSecMatchSearchList;
		values[count] = searchList;
		count++;
	}
	if (returnData) {
		keys[count] = kSecReturnData;
		values[count] = kCFBooleanTrue;
		count++;
		keys[count] = kSecMatchLimit;
		values[count] = kSecMatchLimitOne;
		count++;
	}
	return CFDictionaryCreate(kCFAllocatorDefault, keys, values, count,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
}

static int codex_folio_keychain_find(const char *service, const char *account,
	unsigned char **output, size_t *output_length) {
	if (service == NULL || account == NULL || output == NULL || output_length == NULL) {
		return errSecParam;
	}
	*output = NULL;
	*output_length = 0;
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return errSecParam;
	}
	CFArrayRef search_list = NULL;
	OSStatus status = SecKeychainCopySearchList(&search_list);
	if (status != errSecSuccess) {
		CFRelease(service_value);
		CFRelease(account_value);
		return status;
	}
	CFDictionaryRef query = codex_folio_keychain_identity_query(service_value, account_value, true, search_list);
	CFRelease(service_value);
	CFRelease(account_value);
	if (search_list != NULL) CFRelease(search_list);
	if (query == NULL) {
		return errSecAllocate;
	}

	CFTypeRef result = NULL;
	status = SecItemCopyMatching(query, &result);
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

static int codex_folio_keychain_add(const char *service, const char *account,
	const unsigned char *value, size_t value_length) {
	if (service == NULL || account == NULL || value == NULL || value_length == 0 || value_length > 65536) {
		return errSecParam;
	}
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	CFDataRef data = CFDataCreate(kCFAllocatorDefault, value, (CFIndex)value_length);
	if (service_value == NULL || account_value == NULL || data == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		if (data != NULL) CFRelease(data);
		return errSecAllocate;
	}

	const void *keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecValueData};
	const void *values[] = {
		kSecClassGenericPassword,
		service_value,
		account_value,
		data,
	};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, keys, values, 4,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	CFRelease(service_value);
	CFRelease(account_value);
	CFRelease(data);
	if (query == NULL) {
		return errSecAllocate;
	}
	SecKeychainRef default_keychain = NULL;
	OSStatus status = SecKeychainCopyDefault(&default_keychain);
	if (status != errSecSuccess) {
		CFRelease(query);
		return status;
	}
	CFMutableDictionaryRef add_query = CFDictionaryCreateMutableCopy(kCFAllocatorDefault, 0, query);
	CFRelease(query);
	if (add_query == NULL) {
		CFRelease(default_keychain);
		return errSecAllocate;
	}
	CFDictionarySetValue(add_query, kSecUseKeychain, default_keychain);
	CFRelease(default_keychain);
	status = SecItemAdd(add_query, NULL);
	CFRelease(add_query);
	return status;
}

static int codex_folio_keychain_delete(const char *service, const char *account) {
	if (service == NULL || account == NULL) {
		return errSecParam;
	}
	CFStringRef service_value = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
	CFStringRef account_value = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
	if (service_value == NULL || account_value == NULL) {
		if (service_value != NULL) CFRelease(service_value);
		if (account_value != NULL) CFRelease(account_value);
		return errSecParam;
	}
	CFArrayRef search_list = NULL;
	OSStatus status = SecKeychainCopySearchList(&search_list);
	if (status != errSecSuccess) {
		CFRelease(service_value);
		CFRelease(account_value);
		return status;
	}
	CFDictionaryRef query = codex_folio_keychain_identity_query(service_value, account_value, false, search_list);
	CFRelease(service_value);
	CFRelease(account_value);
	if (search_list != NULL) CFRelease(search_list);
	if (query == NULL) {
		return errSecAllocate;
	}
	status = SecItemDelete(query);
	CFRelease(query);
	return status;
}

static void codex_folio_keychain_free(unsigned char *value, size_t value_length) {
	if (value == NULL) return;
	if (value_length > 0) memset(value, 0, value_length);
	free(value);
}

static int codex_folio_keychain_status_success(void) { return errSecSuccess; }
static int codex_folio_keychain_status_item_not_found(void) { return errSecItemNotFound; }
static int codex_folio_keychain_status_duplicate(void) { return errSecDuplicateItem; }
static int codex_folio_keychain_status_locked(void) { return errSecInteractionNotAllowed; }
static int codex_folio_keychain_status_auth_failed(void) { return errSecAuthFailed; }
static int codex_folio_keychain_status_user_canceled(void) { return errSecUserCanceled; }
static int codex_folio_keychain_status_decode(void) { return errSecDecode; }
static int codex_folio_keychain_status_no_keychain(void) { return errSecNoDefaultKeychain; }
static int codex_folio_keychain_status_not_available(void) { return errSecNotAvailable; }
*/
import "C"

import (
	"errors"
	"unsafe"
)

func newSystemKeychainBackend() keychainBackend {
	return systemKeychainBackend{}
}

func (systemKeychainBackend) find(service, account string) ([]byte, error) {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))

	var output *C.uchar
	var outputLength C.size_t
	status := C.codex_folio_keychain_find(serviceValue, accountValue, &output, &outputLength)
	if int(status) != int(C.codex_folio_keychain_status_success()) {
		return nil, keychainErrorForStatus(int(status))
	}
	if output == nil || outputLength == 0 || outputLength > C.size_t(keychainRecordSize) {
		C.codex_folio_keychain_free(output, outputLength)
		return nil, ErrKeychainProtectedMaterial
	}
	defer C.codex_folio_keychain_free(output, outputLength)
	return C.GoBytes(unsafe.Pointer(output), C.int(outputLength)), nil
}

func (systemKeychainBackend) add(service, account string, record []byte) error {
	if len(record) == 0 {
		return ErrKeychainProtectedMaterial
	}
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	value := C.CBytes(record)
	defer C.codex_folio_keychain_free((*C.uchar)(value), C.size_t(len(record)))
	status := C.codex_folio_keychain_add(serviceValue, accountValue, (*C.uchar)(value), C.size_t(len(record)))
	return keychainErrorForStatus(int(status))
}

func (systemKeychainBackend) delete(service, account string) error {
	serviceValue := C.CString(service)
	accountValue := C.CString(account)
	defer C.free(unsafe.Pointer(serviceValue))
	defer C.free(unsafe.Pointer(accountValue))
	status := C.codex_folio_keychain_delete(serviceValue, accountValue)
	if int(status) == int(C.codex_folio_keychain_status_success()) || int(status) == int(C.codex_folio_keychain_status_item_not_found()) {
		return nil
	}
	return keychainErrorForStatus(int(status))
}

func keychainErrorForStatus(status int) error {
	switch {
	case status == int(C.codex_folio_keychain_status_item_not_found()):
		return ErrKeychainItemNotFound
	case status == int(C.codex_folio_keychain_status_duplicate()):
		return ErrKeychainItemExists
	case status == int(C.codex_folio_keychain_status_locked()):
		return ErrKeychainLocked
	case status == int(C.codex_folio_keychain_status_auth_failed()) || status == int(C.codex_folio_keychain_status_user_canceled()):
		return ErrKeychainAccessDenied
	case status == int(C.codex_folio_keychain_status_decode()):
		return ErrKeychainProtectedMaterial
	case status == int(C.codex_folio_keychain_status_no_keychain()) || status == int(C.codex_folio_keychain_status_not_available()):
		return ErrKeychainUnavailable
	default:
		return errors.Join(ErrKeychainUnavailable, errors.New("Keychain operation failed"))
	}
}
