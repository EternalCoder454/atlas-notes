//go:build tools

package bridge

// gomobile builds the Android bindings from this package, and the bindings it
// generates are compiled against golang.org/x/mobile/bind. Nothing in Atlas
// Notes imports that package directly, so without this file `go mod tidy` drops
// it from go.mod and the Android build fails on a machine that has not happened
// to download it.
//
// The build tag means this file is never part of a real build; `go mod tidy`
// reads it anyway, which is the whole point.

import _ "golang.org/x/mobile/bind"
