package export

import (
	"strings"

	"atlas-notes/internal/diagram"
)

func isMermaid(bl block) bool { return strings.EqualFold(bl.lang, "mermaid") }

// mermaidSVG draws a flowchart, Gantt chart or sequence diagram code block as an SVG picture for a web page, or
// gives "" when the block is not one the diagram package can draw, so that it
// stays a code block. The text is sized by estimate here, as there is no font at
// hand, so the boxes are a little roomy.
func mermaidSVG(bl block) string {
	if !isMermaid(bl) {
		return ""
	}
	doc, err := diagram.ParseDocDrawable(bl.code)
	if err != nil {
		return ""
	}
	return doc.Scene(nil, 0).SVG(diagram.LightPalette)
}
