package bridge

import (
	"os"
	"path/filepath"
	"strings"
)

// setEnv points the storage layer at the directory Android handed us.
//
// The storage layer finds the vault through the same two variables on every
// platform, which is what keeps one code path for all of them. On a desktop
// they are usually unset and the XDG defaults apply; on a phone there is no
// convention to fall back on, so the host is the only thing that knows.
func setEnv(dataDir string) error {
	if dataDir == "" {
		return os.ErrInvalid
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	if err := os.Setenv("ATLAS_DATA_HOME", dataDir); err != nil {
		return err
	}
	// Configuration lives beside the data. Android backs up and deletes an
	// application's files as one unit, so splitting them would only mean two
	// places for the same lifetime.
	return os.Setenv("ATLAS_CONFIG_HOME", filepath.Join(dataDir, "config"))
}

// splitLines and joinLines are a matched pair: splitting and rejoining a note
// has to leave it byte-for-byte identical, or every save would drift the file.
func splitLines(s string) []string { return strings.Split(s, "\n") }

func joinLines(lines []string) string { return strings.Join(lines, "\n") }
