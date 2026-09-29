package export

import (
	"archive/zip"
	"bytes"
	"fmt"
	"hash/crc32"
	"strings"
	"time"
)

// OpenDocument Text (.odt): LibreOffice's own format, and an ISO standard.
//
// Two details decide whether a reader accepts the file at all. The mimetype
// entry has to be the first in the zip, and stored rather than compressed, so
// that a program can recognise the format from the first bytes. And ODF
// collapses runs of spaces the way HTML does, so the spaces that indent a code
// block have to be written as explicit space elements or they vanish.

const odtNS = `xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" ` +
	`xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" ` +
	`xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" ` +
	`xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" ` +
	`xmlns:xlink="http://www.w3.org/1999/xlink" ` +
	`xmlns:dc="http://purl.org/dc/elements/1.1/" ` +
	`xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0" ` +
	`xmlns:svg="urn:oasis:names:tc:opendocument:xmlns:svg-compatible:1.0" ` +
	`xmlns:draw="urn:oasis:names:tc:opendocument:xmlns:drawing:1.0" ` +
	`office:version="1.3"`

// odtStyles defines the named styles, with the names LibreOffice gives its
// own built-in ones, so a heading here is a heading in its navigator and its
// table of contents.
func odtStyles() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n<office:document-styles " + odtNS + "><office:styles>\n")
	b.WriteString(`<style:default-style style:family="paragraph"><style:paragraph-properties fo:margin-bottom="0.21cm"/>` +
		`<style:text-properties fo:font-size="11pt"/></style:default-style>` + "\n")
	b.WriteString(`<style:style style:name="Standard" style:family="paragraph" style:class="text"/>` + "\n")
	b.WriteString(`<style:style style:name="Text_20_body" style:display-name="Text body" style:family="paragraph" ` +
		`style:parent-style-name="Standard" style:class="text"><style:paragraph-properties fo:margin-bottom="0.21cm" fo:line-height="115%"/></style:style>` + "\n")
	sizes := []string{"20pt", "16pt", "14pt", "12pt", "11pt", "11pt"}
	for i, sz := range sizes {
		fmt.Fprintf(&b, `<style:style style:name="Heading_20_%d" style:display-name="Heading %d" style:family="paragraph" `+
			`style:parent-style-name="Standard" style:next-style-name="Text_20_body" style:default-outline-level="%d" style:class="text">`+
			`<style:paragraph-properties fo:margin-top="0.42cm" fo:margin-bottom="0.14cm" fo:keep-with-next="always"/>`+
			`<style:text-properties fo:font-size="%s" fo:font-weight="bold"/></style:style>`+"\n", i+1, i+1, i+1, sz)
	}
	b.WriteString(`<style:style style:name="Quotations" style:family="paragraph" style:parent-style-name="Standard" style:class="html">` +
		`<style:paragraph-properties fo:margin-left="0.6cm" fo:padding-left="0.3cm" fo:border-left="1.5pt solid #bbbbbb"/>` +
		`<style:text-properties fo:font-style="italic" fo:color="#555555"/></style:style>` + "\n")
	b.WriteString(`<style:style style:name="Preformatted_20_Text" style:display-name="Preformatted Text" style:family="paragraph" ` +
		`style:parent-style-name="Standard" style:class="html"><style:paragraph-properties fo:background-color="#f3eff7" fo:padding="0.2cm"/>` +
		`<style:text-properties style:font-name="Liberation Mono" fo:font-family="'Liberation Mono', monospace" fo:font-size="10pt"/></style:style>` + "\n")
	b.WriteString(`<style:style style:name="List_20_Contents" style:display-name="List Contents" style:family="paragraph" ` +
		`style:parent-style-name="Standard" style:class="html"><style:paragraph-properties fo:margin-bottom="0.07cm"/></style:style>` + "\n")
	b.WriteString(`<style:style style:name="Internet_20_link" style:display-name="Internet link" style:family="text">` +
		`<style:text-properties fo:color="#0563c1" style:text-underline-style="solid" style:text-underline-width="auto" style:text-underline-color="font-color"/></style:style>` + "\n")
	b.WriteString("</office:styles></office:document-styles>")
	return b.String()
}

// odtAutoStyles are the styles the content refers to that are not worth a
// name: each combination of inline formatting, the lists, and the divider.
func odtAutoStyles() string {
	var b strings.Builder
	for _, s := range odtSpanStyles() {
		fmt.Fprintf(&b, `<style:style style:name="%s" style:family="text"><style:text-properties%s/></style:style>`+"\n", s.name, s.props)
	}
	bullets := []string{"•", "◦", "▪"}
	b.WriteString(`<text:list-style style:name="LBullet">`)
	for lvl := 1; lvl <= 10; lvl++ {
		fmt.Fprintf(&b, `<text:list-level-style-bullet text:level="%d" text:bullet-char="%s">`+
			`<style:list-level-properties text:list-level-position-and-space-mode="label-alignment">`+
			`<style:list-level-label-alignment text:label-followed-by="listtab" fo:text-indent="-0.4cm" fo:margin-left="%.1fcm"/>`+
			`</style:list-level-properties></text:list-level-style-bullet>`, lvl, bullets[(lvl-1)%3], 0.6+0.6*float64(lvl))
	}
	b.WriteString("</text:list-style>\n")
	formats := []string{"1", "a", "i"}
	b.WriteString(`<text:list-style style:name="LNumber">`)
	for lvl := 1; lvl <= 10; lvl++ {
		fmt.Fprintf(&b, `<text:list-level-style-number text:level="%d" style:num-suffix="." style:num-format="%s">`+
			`<style:list-level-properties text:list-level-position-and-space-mode="label-alignment">`+
			`<style:list-level-label-alignment text:label-followed-by="listtab" fo:text-indent="-0.5cm" fo:margin-left="%.1fcm"/>`+
			`</style:list-level-properties></text:list-level-style-number>`, lvl, formats[(lvl-1)%3], 0.6+0.6*float64(lvl))
	}
	b.WriteString("</text:list-style>\n")
	b.WriteString(`<style:style style:name="PDivider" style:family="paragraph" style:parent-style-name="Standard">` +
		`<style:paragraph-properties fo:border-bottom="0.5pt solid #bbbbbb" fo:padding-bottom="0.1cm"/></style:style>` + "\n")
	b.WriteString(`<style:style style:name="PTask" style:family="paragraph" style:parent-style-name="List_20_Contents"/>` + "\n")
	// OpenSymbol ships with every LibreOffice and has both boxes; the text
	// font may have only one, and a missing glyph draws as nothing.
	b.WriteString(`<style:style style:name="TBox" style:family="text"><style:text-properties style:font-name="OpenSymbol" fo:font-family="OpenSymbol"/></style:style>` + "\n")
	b.WriteString(`<style:style style:name="TDetail" style:family="text"><style:text-properties fo:color="#777777"/></style:style>` + "\n")
	return b.String()
}

type spanStyle struct {
	name                       string
	props                      string
	bold, italic, strike, mono bool
}

// odtSpanStyles is every combination of bold, italic, struck-through and code,
// so any run can refer to exactly one style.
func odtSpanStyles() []spanStyle {
	var out []spanStyle
	for mask := 1; mask < 16; mask++ {
		s := spanStyle{bold: mask&1 != 0, italic: mask&2 != 0, strike: mask&4 != 0, mono: mask&8 != 0}
		s.name = fmt.Sprintf("T%d", mask)
		if s.bold {
			s.props += ` fo:font-weight="bold"`
		}
		if s.italic {
			s.props += ` fo:font-style="italic"`
		}
		if s.strike {
			s.props += ` style:text-line-through-style="solid"`
		}
		if s.mono {
			s.props += ` style:font-name="Liberation Mono" fo:font-family="'Liberation Mono', monospace" fo:background-color="#f3eff7"`
		}
		out = append(out, s)
	}
	return out
}

func odtSpanName(r inline) string {
	mask := 0
	if r.bold {
		mask |= 1
	}
	if r.italic {
		mask |= 2
	}
	if r.strike {
		mask |= 4
	}
	if r.mono {
		mask |= 8
	}
	if mask == 0 {
		return ""
	}
	return fmt.Sprintf("T%d", mask)
}

func renderODT(title string, blocks []block) ([]byte, error) {
	var body strings.Builder
	pics := &odtPictures{}
	for i := 0; i < len(blocks); {
		bl := blocks[i]
		switch bl.kind {
		case heading:
			fmt.Fprintf(&body, `<text:h text:style-name="Heading_20_%d" text:outline-level="%d">%s</text:h>`+"\n",
				bl.level, bl.level, odtLines(bl.lines))
		case paragraph:
			body.WriteString(`<text:p text:style-name="Text_20_body">` + odtLines(bl.lines) + "</text:p>\n")
		case quote:
			body.WriteString(`<text:p text:style-name="Quotations">` + odtLines(bl.lines) + "</text:p>\n")
		case code:
			var lines []string
			for _, l := range strings.Split(bl.code, "\n") {
				lines = append(lines, odtText(l))
			}
			body.WriteString(`<text:p text:style-name="Preformatted_20_Text">` + strings.Join(lines, "<text:line-break/>") + "</text:p>\n")
		case divider:
			body.WriteString(`<text:p text:style-name="PDivider"/>` + "\n")
		case figure:
			if bl.pic != nil {
				body.WriteString(`<text:p text:style-name="Text_20_body">` + pics.frame(bl) + "</text:p>\n")
			} else {
				body.WriteString(`<text:p text:style-name="Text_20_body">` + odtText(imageLabel(bl.alt, bl.src)) + "</text:p>\n")
			}
		default:
			j := i
			for j < len(blocks) && isListItem(blocks[j]) {
				j++
			}
			odtList(&body, blocks[i:j], 0)
			i = j
			continue
		}
		i++
	}

	content := `<?xml version="1.0" encoding="UTF-8"?>` + "\n<office:document-content " + odtNS + ">\n" +
		"<office:automatic-styles>\n" + odtAutoStyles() + "</office:automatic-styles>\n" +
		"<office:body><office:text>\n" + body.String() + "</office:text></office:body></office:document-content>"
	meta := `<?xml version="1.0" encoding="UTF-8"?>` + "\n<office:document-meta " + odtNS + "><office:meta>" +
		"<dc:title>" + xmlText(title) + "</dc:title><meta:generator>Atlas Notes</meta:generator>" +
		"<meta:creation-date>" + time.Now().UTC().Format("2006-01-02T15:04:05") + "</meta:creation-date>" +
		"</office:meta></office:document-meta>"
	manifest := `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.3">` + "\n" +
		`<manifest:file-entry manifest:full-path="/" manifest:version="1.3" manifest:media-type="application/vnd.oasis.opendocument.text"/>` + "\n" +
		`<manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>` + "\n" +
		`<manifest:file-entry manifest:full-path="styles.xml" manifest:media-type="text/xml"/>` + "\n" +
		`<manifest:file-entry manifest:full-path="meta.xml" manifest:media-type="text/xml"/>` + "\n" +
		pics.manifestEntries() +
		"</manifest:manifest>"

	var out bytes.Buffer
	z := zip.NewWriter(&out)
	// First, and stored: see the top of this file. And written raw, with its
	// size and checksum in its own header. The zip writer otherwise streams an
	// entry and puts those in a trailer, marking the header to say so, and a
	// stored entry has nothing in it that says where it ends: LibreOffice
	// will not open a package whose mimetype entry is written that way.
	mimetype := []byte("application/vnd.oasis.opendocument.text")
	w, err := z.CreateRaw(&zip.FileHeader{
		Name:               "mimetype",
		Method:             zip.Store,
		CRC32:              crc32.ChecksumIEEE(mimetype),
		CompressedSize64:   uint64(len(mimetype)),
		UncompressedSize64: uint64(len(mimetype)),
	})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(mimetype); err != nil {
		return nil, err
	}
	for _, f := range []struct{ name, data string }{
		{"content.xml", content},
		{"styles.xml", odtStyles()},
		{"meta.xml", meta},
		{"META-INF/manifest.xml", manifest},
	} {
		w, err := z.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.data)); err != nil {
			return nil, err
		}
	}
	// Deflated like the rest, not stored: only the mimetype entry is written
	// raw, and a stored entry streamed by the zip writer has the problem
	// described above.
	for i, p := range pics.list {
		w, err := z.Create(odtPicturePath(i, p))
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(p.data); err != nil {
			return nil, err
		}
	}
	if err := z.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// odtPictures collects the pictures a document shows. Each is a file under
// Pictures/, listed in the manifest, and a frame in the text points at it.
type odtPictures struct {
	list   []*picture // the distinct pictures; list[i] is Pictures/image(i+1)
	frames int        // how many are placed, for their unique names
}

func odtPicturePath(i int, p *picture) string {
	return fmt.Sprintf("Pictures/image%d.%s", i+1, p.ext)
}

// frame places a picture in the text, anchored as a character so that it sits
// in its paragraph as a letter would. A picture used twice is one file with two
// frames.
func (o *odtPictures) frame(bl block) string {
	idx := -1
	for i, m := range o.list {
		if m == bl.pic {
			idx = i
		}
	}
	if idx < 0 {
		o.list = append(o.list, bl.pic)
		idx = len(o.list) - 1
	}
	o.frames++
	w, h := bl.pic.inches()
	return fmt.Sprintf(`<draw:frame draw:name="Image%d" text:anchor-type="as-char" svg:width="%.4fin" svg:height="%.4fin">`+
		`<draw:image xlink:href="%s" xlink:type="simple" xlink:show="embed" xlink:actuate="onLoad"/>`+
		`<svg:desc>%s</svg:desc></draw:frame>`,
		o.frames, w, h, odtPicturePath(idx, bl.pic), xmlText(imageLabel(bl.alt, bl.src)))
}

func (o *odtPictures) manifestEntries() string {
	var b strings.Builder
	for i, p := range o.list {
		fmt.Fprintf(&b, `<manifest:file-entry manifest:full-path="%s" manifest:media-type="%s"/>`+"\n", odtPicturePath(i, p), p.mime)
	}
	return b.String()
}

// odtList writes consecutive list items as nested lists. A checklist item is a
// list item without a bullet, carrying its box as text.
func odtList(b *strings.Builder, items []block, depth int) {
	for i := 0; i < len(items); {
		k := items[i].kind
		j := i
		for j < len(items) && (items[j].level > depth || items[j].kind == k) {
			j++
		}
		style := "LBullet"
		if k == numbered {
			style = "LNumber"
		}
		if k == task {
			// Tasks are paragraphs, indented to their depth, not list items:
			// a list would draw a bullet in front of the box.
			for x := i; x < j; x++ {
				it := items[x]
				if it.level > depth && it.kind != task {
					y := x
					for y < j && items[y].level > depth {
						y++
					}
					odtList(b, items[x:y], depth+1)
					x = y - 1
					continue
				}
				line := it.lines[0]
				if it.checked {
					line = append([]inline(nil), line...)
					for n := range line {
						line[n].strike = true
					}
				}
				fmt.Fprintf(b, `<text:p text:style-name="PTask">%s<text:span text:style-name="TBox">%s</text:span>%s</text:p>`+"\n",
					odtIndent(it.level), odtText(checkbox(it.checked)), odtRuns(line)+odtDetail(it.detail))
			}
			i = j
			continue
		}
		fmt.Fprintf(b, `<text:list text:style-name="%s">`, style)
		for x := i; x < j; {
			it := items[x]
			y := x + 1
			for y < j && items[y].level > depth {
				y++
			}
			// A numbered list starts from the number the note gave it, which
			// is how a list interrupted by a paragraph carries on.
			start := ""
			if x == i && k == numbered && it.number > 1 {
				start = fmt.Sprintf(` text:start-value="%d"`, it.number)
			}
			b.WriteString(`<text:list-item` + start + `><text:p text:style-name="List_20_Contents">` + odtRuns(it.lines[0]) + "</text:p>")
			if y > x+1 {
				odtList(b, items[x+1:y], depth+1)
			}
			b.WriteString("</text:list-item>")
			x = y
		}
		b.WriteString("</text:list>\n")
		i = j
	}
}

func odtIndent(level int) string {
	if level == 0 {
		return ""
	}
	return fmt.Sprintf(`<text:s text:c="%d"/>`, 4*level)
}

func odtDetail(detail string) string {
	if detail == "" {
		return ""
	}
	return `<text:span text:style-name="TDetail"> (` + odtText(detail) + `)</text:span>`
}

func odtLines(lines [][]inline) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		parts[i] = odtRuns(l)
	}
	return strings.Join(parts, "<text:line-break/>")
}

func odtRuns(runs []inline) string {
	var b strings.Builder
	for _, r := range runs {
		t := odtText(r.text)
		if name := odtSpanName(r); name != "" {
			t = `<text:span text:style-name="` + name + `">` + t + "</text:span>"
		}
		if r.link != "" {
			t = `<text:a xlink:type="simple" xlink:href="` + xmlAttr(safeLink(r.link)) + `" text:style-name="Internet_20_link">` + t + "</text:a>"
		}
		b.WriteString(t)
	}
	return b.String()
}

// odtText escapes text and writes out the whitespace ODF would otherwise
// collapse: a run of spaces keeps its first space as text and the rest as a
// counted space element, and a tab becomes a tab element.
func odtText(s string) string {
	var b strings.Builder
	spaces := 0
	flush := func() {
		if spaces == 0 {
			return
		}
		// Leading spaces are dropped by readers too, so at the start of the
		// text every one of them is written as an element.
		if b.Len() == 0 {
			fmt.Fprintf(&b, `<text:s text:c="%d"/>`, spaces)
			spaces = 0
			return
		}
		b.WriteString(" ")
		if spaces > 1 {
			fmt.Fprintf(&b, `<text:s text:c="%d"/>`, spaces-1)
		}
		spaces = 0
	}
	for _, r := range xmlText(s) {
		switch r {
		case ' ':
			spaces++
			continue
		case '\t':
			flush()
			b.WriteString("<text:tab/>")
			continue
		case '\n', '\r':
			flush()
			continue
		}
		flush()
		b.WriteRune(r)
	}
	flush()
	return b.String()
}
