// Package mobile is the touch interface for Atlas Notes: the same vault, the
// same notes and the same encryption as the desktop application, drawn for a
// phone with Gio.
//
// Every file in it is behind a build tag, because Gio links against the
// system's graphics and keyboard libraries and most machines building the
// desktop app do not have their headers installed. Without the guard,
// `go build ./...` on an ordinary Linux checkout fails on a package that
// machine was never going to run.
//
//	go build -tags gio ./cmd/atlas-mobile   # look at it on this machine
//	make apk                                # build it for a phone
//
// The Android build picks it up on its own: the android tag is set for it.
package mobile
