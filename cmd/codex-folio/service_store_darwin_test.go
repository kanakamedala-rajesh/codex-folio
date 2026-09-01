//go:build darwin && cgo

package main

/*
#cgo LDFLAGS: -framework Security -framework CoreFoundation
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
	const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecUseDataProtectionKeychain, kSecReturnData, kSecMatchLimit};
	const void *query_values[] = {kSecClassGenericPassword, service_value, account_value, kCFBooleanTrue, kCFBooleanTrue, kSecMatchLimitOne};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, query_keys, query_values, 6,
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
	const void *query_keys[] = {kSecClass, kSecAttrService, kSecAttrAccount, kSecUseDataProtectionKeychain};
	const void *query_values[] = {kSecClassGenericPassword, service_value, account_value, kCFBooleanTrue};
	CFDictionaryRef query = CFDictionaryCreate(kCFAllocatorDefault, query_keys, query_values, 4,
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
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"venkatasudha.com/codex-folio/internal/platform"
	"venkatasudha.com/codex-folio/internal/store"
)

func TestDarwinServiceStoreReusesKeychainForEncryptedProjectIdentity(t *testing.T) {
	root := testServiceTempDir(t)
	override := root
	paths, err := platform.ResolvePaths(platform.PathOptions{
		Platform:          platform.PlatformDarwin,
		HomeDir:           filepath.Join(root, "home"),
		OwnerHomeDir:      filepath.Join(root, "owner-home"),
		StateRootOverride: &override,
	})
	if err != nil {
		t.Fatalf("ResolvePaths() error = %v", err)
	}

	service := fmt.Sprintf("%s.test.%d", platform.DefaultKeychainService, os.Getpid())
	account := fmt.Sprintf("%s.test.%d", platform.DefaultKeychainAccount, os.Getpid())
	if err := deleteDarwinKeychainRecord(service, account); err != nil {
		t.Fatalf("delete stale test item: %v", err)
	}
	t.Cleanup(func() {
		if err := deleteDarwinKeychainRecord(service, account); err != nil {
			t.Errorf("delete test item: %v", err)
		}
	})

	keychainOptions := platform.KeychainOptions{
		Service:     service,
		Account:     account,
		AllowCreate: true,
	}
	project := store.ProjectIdentity{
		ProjectIdentityID: "project-1",
		ProjectAlias:      "example",
		CanonicalPath:     "/private/sentinel/project",
		CreatedAt:         time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC),
		UpdatedAt:         time.Date(2026, time.August, 31, 12, 1, 0, 0, time.UTC),
	}

	firstVault, err := platform.NewKeychainVaultWithOptions(keychainOptions)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() first error = %v", err)
	}
	first, err := store.OpenWithVault(paths.DatabaseFile, firstVault)
	if err != nil {
		t.Fatalf("OpenWithVault() first error = %v", err)
	}
	if err := first.PutProjectIdentity(context.Background(), project); err != nil {
		_ = first.Close()
		t.Fatalf("PutProjectIdentity() error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Store.Close() error = %v", err)
	}

	secondVault, err := platform.NewKeychainVaultWithOptions(keychainOptions)
	if err != nil {
		t.Fatalf("NewKeychainVaultWithOptions() after restart error = %v", err)
	}
	second, err := store.OpenWithVault(paths.DatabaseFile, secondVault)
	if err != nil {
		t.Fatalf("OpenWithVault() after restart error = %v", err)
	}
	got, err := second.GetProjectIdentity(context.Background(), project.ProjectIdentityID)
	if err != nil {
		_ = second.Close()
		t.Fatalf("GetProjectIdentity() after restart error = %v", err)
	}
	if got != project {
		_ = second.Close()
		t.Fatalf("GetProjectIdentity() after restart = %#v, want %#v", got, project)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("second Store.Close() error = %v", err)
	}

	keychainRecord, err := readDarwinKeychainRecord(service, account)
	if err != nil {
		t.Fatalf("find Keychain record: %v", err)
	}
	defer clear(keychainRecord)
	if len(keychainRecord) < 32 {
		t.Fatalf("Keychain record length = %d, want key material", len(keychainRecord))
	}
	keyMaterial := keychainRecord[len(keychainRecord)-32:]

	databaseBytes, err := os.ReadFile(paths.DatabaseFile)
	if err != nil {
		t.Fatalf("ReadFile(database): %v", err)
	}
	for name, forbidden := range map[string][]byte{
		"canonical project path": []byte(project.CanonicalPath),
		"Keychain record":        keychainRecord,
		"Keychain key material":  keyMaterial,
	} {
		if bytes.Contains(databaseBytes, forbidden) {
			t.Fatalf("database contains %s", name)
		}
	}
}

func readDarwinKeychainRecord(service, account string) ([]byte, error) {
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
			C.memset(unsafe.Pointer(output), 0, C.size_t(outputLength))
		}
		C.free(unsafe.Pointer(output))
	}()
	return C.GoBytes(unsafe.Pointer(output), C.int(outputLength)), nil
}

func deleteDarwinKeychainRecord(service, account string) error {
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
