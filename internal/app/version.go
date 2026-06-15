package app

// version is the application version. It is overridable at build time via:
//
//	-ldflags "-X 'atlas-notes/internal/app.version=<v>'"
//
// and defaults to the value in the Makefile's VERSION.
var version = "0.2.0"
