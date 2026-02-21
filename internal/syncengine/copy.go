package syncengine

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyFile copies src to dst, creating parent directories as needed.
// Returns true if the file was actually written (new or changed).
func copyFile(src, dst string) (bool, error) {
	// Fast path: if dst exists with same size, compare hashes.
	if filesEqual(src, dst) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return false, fmt.Errorf("creating parent dir for %s: %w", dst, err)
	}

	in, err := os.Open(src)
	if err != nil {
		return false, fmt.Errorf("opening source %s: %w", src, err)
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return false, fmt.Errorf("creating destination %s: %w", dst, err)
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return false, fmt.Errorf("copying %s to %s: %w", src, dst, err)
	}

	// Preserve source file permissions.
	srcInfo, err := os.Stat(src)
	if err == nil {
		_ = os.Chmod(dst, srcInfo.Mode())
	}

	return true, nil
}

// filesEqual returns true if both files exist and have identical content.
func filesEqual(a, b string) bool {
	infoA, errA := os.Stat(a)
	infoB, errB := os.Stat(b)
	if errA != nil || errB != nil {
		return false
	}
	// Quick size check.
	if infoA.Size() != infoB.Size() {
		return false
	}
	hashA, errA := sha256File(a)
	hashB, errB := sha256File(b)
	if errA != nil || errB != nil {
		return false
	}
	return hashA == hashB
}

// sha256File returns the hex-encoded SHA-256 hash of a file.
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", h.Sum(nil)), nil
}
