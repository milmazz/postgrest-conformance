// Package fetchbin downloads and verifies the PostgREST release binary
// pinned by the repository's PIN file, caching the unpacked binary under
// tools/oracle/.cache/ for reuse across runs.
package fetchbin

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// releaseBaseURL is the GitHub release download base for PostgREST.
const releaseBaseURL = "https://github.com/PostgREST/postgrest/releases/download"

// httpTimeout bounds the tarball download.
const httpTimeout = 5 * time.Minute

// Version reads pinPath (the repository's PIN file) and returns the pinned
// PostgREST version, e.g. "v16.0", parsed from a line of the form
// "postgrest: v16.0". It returns an error if the file cannot be read or no
// such line is present.
func Version(pinPath string) (string, error) {
	f, err := os.Open(pinPath)
	if err != nil {
		return "", fmt.Errorf("fetchbin: open %s: %w", pinPath, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	const prefix = "postgrest:"
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, prefix) {
			v := strings.TrimSpace(strings.TrimPrefix(line, prefix))
			if v == "" {
				return "", fmt.Errorf("fetchbin: %s: %q line has no version", pinPath, prefix)
			}
			return v, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("fetchbin: read %s: %w", pinPath, err)
	}
	return "", fmt.Errorf("fetchbin: %s: no %q line found", pinPath, prefix)
}

// assetName returns the PostgREST release tarball filename for the given
// version and platform (runtime.GOOS/runtime.GOARCH values). It returns an
// error for platforms PostgREST does not publish a static release for.
func assetName(version, goos, goarch string) (string, error) {
	var platform string
	switch {
	case goos == "linux" && goarch == "amd64":
		platform = "linux-static-x86-64"
	case goos == "linux" && goarch == "arm64":
		platform = "linux-static-aarch64"
	case goos == "darwin" && goarch == "amd64":
		platform = "macos-x86-64"
	case goos == "darwin" && goarch == "arm64":
		platform = "macos-aarch64"
	default:
		return "", fmt.Errorf("fetchbin: unsupported platform %s/%s", goos, goarch)
	}
	return fmt.Sprintf("postgrest-%s-%s.tar.xz", version, platform), nil
}

// verifyChecksum computes the SHA-256 of the file at tarPath and compares it
// against the entry for filepath.Base(tarPath) in sumsFile (a file in
// `shasum -a 256` output format: "<hex>  <filename>" per line). It returns
// an error if the entry is missing or the checksum does not match.
func verifyChecksum(tarPath, sumsFile string) error {
	want, err := lookupChecksum(sumsFile, filepath.Base(tarPath))
	if err != nil {
		return err
	}

	f, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("fetchbin: open %s: %w", tarPath, err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("fetchbin: hash %s: %w", tarPath, err)
	}
	got := hex.EncodeToString(h.Sum(nil))

	if !strings.EqualFold(got, want) {
		return fmt.Errorf("fetchbin: checksum mismatch for %s: got %s, want %s", tarPath, got, want)
	}
	return nil
}

// lookupChecksum finds the checksum entry for name in sumsFile. A missing
// entry is a hard error, never a warning.
func lookupChecksum(sumsFile, name string) (string, error) {
	f, err := os.Open(sumsFile)
	if err != nil {
		return "", fmt.Errorf("fetchbin: open %s: %w", sumsFile, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 {
			continue
		}
		sum, fname := fields[0], fields[1]
		if fname == name {
			return sum, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("fetchbin: read %s: %w", sumsFile, err)
	}
	return "", fmt.Errorf("fetchbin: no checksum entry for %q in %s", name, sumsFile)
}

// Fetch downloads (if not already cached), verifies, and unpacks the
// PostgREST release binary pinned by <repoRoot>/PIN for the current
// runtime.GOOS/runtime.GOARCH. It returns the absolute path to the unpacked
// "postgrest" executable.
//
// Fetch is idempotent: a cached tarball is reused rather than re-downloaded,
// but its checksum is re-verified on every call.
func Fetch(repoRoot string) (string, error) {
	pinPath := filepath.Join(repoRoot, "PIN")
	version, err := Version(pinPath)
	if err != nil {
		return "", err
	}

	asset, err := assetName(version, runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return "", err
	}

	oracleDir := filepath.Join(repoRoot, "tools", "oracle")
	sumsFile := filepath.Join(oracleDir, "bin.sha256")
	cacheDir := filepath.Join(oracleDir, ".cache")
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", fmt.Errorf("fetchbin: create cache dir %s: %w", cacheDir, err)
	}

	tarPath := filepath.Join(cacheDir, asset)
	if _, err := os.Stat(tarPath); err != nil {
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("fetchbin: stat %s: %w", tarPath, err)
		}
		url := fmt.Sprintf("%s/%s/%s", releaseBaseURL, version, asset)
		if err := download(url, tarPath); err != nil {
			return "", err
		}
	}

	if err := verifyChecksum(tarPath, sumsFile); err != nil {
		return "", err
	}

	destDir := filepath.Join(cacheDir, fmt.Sprintf("postgrest-%s", version))
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("fetchbin: create dest dir %s: %w", destDir, err)
	}
	if err := unpack(tarPath, destDir); err != nil {
		return "", err
	}

	binPath, err := findBinary(destDir)
	if err != nil {
		return "", err
	}
	if err := os.Chmod(binPath, 0o755); err != nil {
		return "", fmt.Errorf("fetchbin: chmod %s: %w", binPath, err)
	}
	return binPath, nil
}

// download fetches url to destPath, following redirects, bounded by
// httpTimeout. It writes to a temp file first and renames on success so a
// failed download never leaves a corrupt file at destPath.
func download(url, destPath string) error {
	client := &http.Client{Timeout: httpTimeout}
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("fetchbin: download %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fetchbin: download %s: unexpected status %s", url, resp.Status)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("fetchbin: create temp file for %s: %w", destPath, err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath) // no-op once renamed

	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return fmt.Errorf("fetchbin: write %s: %w", tmpPath, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("fetchbin: close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("fetchbin: rename %s to %s: %w", tmpPath, destPath, err)
	}
	return nil
}

// unpack extracts the .tar.xz at tarPath into destDir using the system tar.
func unpack(tarPath, destDir string) error {
	cmd := exec.Command("tar", "-xJf", tarPath, "-C", destDir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("fetchbin: tar -xJf %s: %w: %s", tarPath, err, out)
	}
	return nil
}

// findBinary walks destDir looking for the "postgrest" executable, which may
// sit at the top level or under a bin/ subdirectory depending on the release
// layout.
func findBinary(destDir string) (string, error) {
	var found string
	err := filepath.WalkDir(destDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "postgrest" {
			abs, err := filepath.Abs(path)
			if err != nil {
				return err
			}
			found = abs
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("fetchbin: walk %s: %w", destDir, err)
	}
	if found == "" {
		return "", fmt.Errorf("fetchbin: no postgrest binary found under %s", destDir)
	}
	return found, nil
}
