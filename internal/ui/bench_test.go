package ui

import (
	"strings"
	"testing"
)

func BenchmarkMarkdownToPango(b *testing.B) {
	md := strings.Repeat("## Heading\n\nSome **bold** text with *italics* and `code`.\n\n- a bullet\n- another\n\n", 20)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		markdownToPango(md)
	}
}
