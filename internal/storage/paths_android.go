//go:build android

package storage

import (
	"os"
	"path/filepath"
)

// Android gives an application one private directory and tells it the path at
// runtime; there is no conventional location to guess at. The host passes it in
// through ATLAS_DATA_HOME before the store is opened, and these fallbacks exist
// so a misconfigured host fails somewhere writable rather than at the root.

func platformDataDir() string {
	if files := os.Getenv("ANDROID_FILES_DIR"); files != "" {
		return filepath.Join(files, AppName)
	}
	return filepath.Join(os.TempDir(), AppName)
}

func platformConfigDir() string { return platformDataDir() }
