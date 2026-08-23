package fetchbin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVersionParsesPIN(t *testing.T) {
	dir := t.TempDir()
	pin := filepath.Join(dir, "PIN")
	os.WriteFile(pin, []byte("postgrest: v16.0\ncommit: ac464c368153851fd7746cf761b2ee11d7200e62\n"), 0o644)
	v, err := Version(pin)
	if err != nil || v != "v16.0" {
		t.Fatalf("got %q, %v; want v16.0", v, err)
	}
}

func TestAssetNameForPlatform(t *testing.T) {
	for _, tc := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "postgrest-v16.0-linux-static-x86-64.tar.xz"},
		{"linux", "arm64", "postgrest-v16.0-linux-static-aarch64.tar.xz"},
		{"darwin", "amd64", "postgrest-v16.0-macos-x86-64.tar.xz"},
		{"darwin", "arm64", "postgrest-v16.0-macos-aarch64.tar.xz"},
	} {
		got, err := assetName("v16.0", tc.goos, tc.goarch)
		if err != nil || got != tc.want {
			t.Fatalf("%s/%s: got %q, %v", tc.goos, tc.goarch, got, err)
		}
	}
	if _, err := assetName("v16.0", "plan9", "386"); err == nil {
		t.Fatal("want error for unsupported platform")
	}
}

func TestVerifyChecksumRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "a.tar.xz")
	os.WriteFile(f, []byte("bytes"), 0o644)
	sums := filepath.Join(dir, "bin.sha256")
	os.WriteFile(sums, []byte("deadbeef  a.tar.xz\n"), 0o644)
	if err := verifyChecksum(f, sums); err == nil {
		t.Fatal("want checksum mismatch error")
	}
}
