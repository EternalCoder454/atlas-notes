//go:build !android && !gio

// The phone interface is behind a build tag; see internal/mobile. This stands
// in for it so that building every package in the repository does not fail on
// a machine without Gio's dependencies.
package main

import "fmt"

func main() {
	fmt.Println("Atlas Notes for phones is built with the gio tag:")
	fmt.Println()
	fmt.Println("    go build -tags gio ./cmd/atlas-mobile   # for this machine")
	fmt.Println("    make apk                                # for a phone")
}
