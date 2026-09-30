package editor

import (
	"strings"
	"testing"
)

const richLine = "The **release** notes mention *italics*, `inline code`, and a <!-- priority:high --> marker."

func BenchmarkParseLineSpans(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseLineSpans(richLine, -1)
	}
}

// BenchmarkParseLineSpansCaret is the caret's own line: the caret sits inside the
// bold, so its markers are left out and the rest are hidden.
func BenchmarkParseLineSpansCaret(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseLineSpans(richLine, 10)
	}
}

func BenchmarkParseLineSpansHeading(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseLineSpans("## A heading with **bold** in it", -1)
	}
}

// BenchmarkParseDocumentSpans approximates one editor reparse of a 400-line
// note, which is what the 50ms debounce runs after each pause in typing.
func BenchmarkParseDocumentSpans(b *testing.B) {
	lines := make([]string, 0, 400)
	for i := 0; i < 400; i++ {
		lines = append(lines, richLine)
	}
	doc := strings.Join(lines, "\n")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range strings.Split(doc, "\n") {
			parseLineSpans(line, -1)
		}
	}
}
