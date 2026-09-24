package app

// version is the application version, and the one source of truth for it: the
// Makefile reads this line to stamp builds and packaging.
//
// It is overridable at build time via:
//
//	-ldflags "-X 'atlas-notes/internal/app.version=<v>'"
//
// The default matters. A plain `go build .` produces a binary that reports this
// number, and the launch-time update check compares it against what has been
// published — so a stale default here would offer every user an update they
// already have.
var version = "0.5.5"
