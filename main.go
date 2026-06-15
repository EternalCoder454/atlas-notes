// Command atlas-notes is a local-first note and checklist desktop app for
// GNOME, built on GTK4 + libadwaita with local AI assistance via Ollama.
package main

import (
	_ "embed"
	"os"

	"atlas-notes/internal/app"
)

//go:embed assets/style.css
var styleCSS string

func main() {
	os.Exit(app.New(styleCSS).Run(os.Args))
}
