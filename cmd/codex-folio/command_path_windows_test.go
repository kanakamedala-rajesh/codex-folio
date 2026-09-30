//go:build windows

package main

import "testing"

func TestWindowsEnvironmentChangeBroadcast(t *testing.T) {
	// Notify existing applications without changing the registry or user PATH.
	if err := broadcastWindowsEnvironmentChange(); err != nil {
		t.Fatal(err)
	}
}
