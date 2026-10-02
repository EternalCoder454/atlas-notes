// Command atlas-notes is a local-first note and checklist desktop app for
// GNOME, built on GTK4 + libadwaita with local AI assistance via Ollama.
package main

import (
	_ "embed"
	"os"

	"atlas-notes/internal/app"
	"atlas-notes/internal/mcp"
)

//go:embed assets/style.css
var styleCSS string

func main() {
	// "atlas-notes mcp" is the server Claude Code talks to. It opens no window,
	// so it is told apart before GTK is started.
	if len(os.Args) == 2 && os.Args[1] == "mcp" {
		os.Exit(mcp.Run(mcp.Options{Version: app.Version(), Trash: app.TrashFile}))
	}
	os.Exit(app.New(styleCSS).Run(os.Args))
}
