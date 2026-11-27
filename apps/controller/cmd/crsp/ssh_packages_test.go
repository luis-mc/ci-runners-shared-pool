package main

import (
	"context"
	"strings"
	"testing"
)

type sumsFake struct{ sums string }

func (f sumsFake) Sums(context.Context) ([]byte, string, error) { return []byte(f.sums), "1.2.3", nil }

func TestControllerPackagesPointsAtTheConsole(t *testing.T) {
	sum := strings.Repeat("a", 64)
	c := controllerPackages{sums: sumsFake{sums: sum + "  crsp-windows-amd64.msi\n"}, console: "https://pool.lan:8460/"}
	ref, err := c.WindowsPackage(context.Background(), "amd64")
	if err != nil {
		t.Fatal(err)
	}
	if ref.URL != "https://pool.lan:8460/api/v1/packages/crsp-windows-amd64.msi" || ref.SHA256 != sum || !ref.SelfSigned {
		t.Fatalf("ref = %+v", ref)
	}
	if _, err := c.WindowsPackage(context.Background(), "arm64"); err == nil {
		t.Fatal("arm64 is not listed; want an error")
	}
}
