package main

import "testing"

func TestInstallerCommandReconciliationTimeoutFlag(t *testing.T) {
	flag := NewInstallerCmd().Flags().Lookup("reconciliation-timeout")
	if flag == nil {
		t.Fatal("reconciliation-timeout flag is not registered")
	}
	if flag.DefValue != "15m0s" {
		t.Fatalf("reconciliation-timeout default = %q, want 15m0s", flag.DefValue)
	}
}
