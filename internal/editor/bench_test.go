package editor

import (
	"strings"
	"testing"
)

const richLine = "The **release** notes mention *italics*, `inline code`, and a <!-- priority:high --> marker."

func BenchmarkParseLineSpans(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseLineSpans(richLine, false)
	}
}

func BenchmarkParseLineSpansHeading(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseLineSpans("## A heading with **bold** in it", false)
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
			parseLineSpans(line, false)
		}
	}
}
