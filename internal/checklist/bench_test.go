package checklist

import (
	"strings"
	"testing"
)

const taskLine = "- [x] Ship the release notes and update the changelog <!-- priority:high due:2026-07-01 order:3 -->"
const plainLine = "Atlas Notes keeps every note as a compressed markdown file on disk."

func BenchmarkParseLineTask(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, ok := ParseLine(taskLine); !ok {
			b.Fatal("not parsed")
		}
	}
}

// BenchmarkParseLinePlain is the common case: most lines in a note are not
// tasks, and every render pass tests every line.
func BenchmarkParseLinePlain(b *testing.B) {
	for i := 0; i < b.N; i++ {
		if _, ok := ParseLine(plainLine); ok {
			b.Fatal("parsed a plain line")
		}
	}
}

func BenchmarkParseDocument(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 200; i++ {
		sb.WriteString(plainLine + "\n")
		sb.WriteString(taskLine + "\n")
	}
	doc := sb.String()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		Parse(doc)
	}
}

func BenchmarkTextOffset(b *testing.B) {
	for i := 0; i < b.N; i++ {
		TextOffset(taskLine)
	}
}

func BenchmarkMarshal(b *testing.B) {
	it, _ := ParseLine(taskLine)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		it.Marshal()
	}
}
