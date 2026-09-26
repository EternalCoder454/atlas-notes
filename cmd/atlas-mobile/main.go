//go:build android || gio

// Command atlas-mobile is Atlas Notes for a phone.
//
// It is the same vault, the same notes and the same encryption as the desktop
// application, with an interface drawn for a touch screen. The assistant is
// not here: it needs a model server running on the machine, which a phone does
// not have, and sending notes to someone else's would break the one promise
// the app is built on.
package main

import (
	"log"
	"os"

	"gioui.org/app"

	"atlas-notes/internal/mobile"
)

// version is what this build calls itself. The release job stamps it from
// internal/app/version.go, which is the one source of truth for it.
var version = "0.0.0"

func main() {
	go func() {
		if err := mobile.Run(version); err != nil {
			log.Println("atlas-notes:", err)
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}
