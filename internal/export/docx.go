package export

import (
	"archive/zip"
	"bytes"
	"fmt"
	"strings"
	"time"
)

// Word (.docx): WordprocessingML, a zip of a few XML parts.
//
// Headings use Word's own built-in style IDs, so they appear in its
// navigation pane and table of contents as headings rather than as big bold
// paragraphs. Lists are real Word lists, with numbering definitions, so
// adding an item in Word continues them. A checklist item is a paragraph
// starting with a box character: Word's own checkbox is a content control,
// which LibreOffice and most other readers do not show as one.

const docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults>
<w:rPrDefault><w:rPr><w:sz w:val="22"/><w:szCs w:val="22"/><w:lang w:val="en-US"/></w:rPr></w:rPrDefault>
<w:pPrDefault><w:pPr><w:spacing w:after="120" w:line="276" w:lineRule="auto"/></w:pPr></w:pPrDefault>
</w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/><w:qFormat/></w:style>
%s
<w:style w:type="paragraph" w:styleId="Quote"><w:name w:val="Quote"/><w:basedOn w:val="Normal"/><w:qFormat/>
<w:pPr><w:pBdr><w:left w:val="single" w:sz="18" w:space="8" w:color="BBBBBB"/></w:pBdr><w:ind w:left="360"/></w:pPr>
<w:rPr><w:i/><w:color w:val="555555"/></w:rPr></w:style>
<w:style w:type="paragraph" w:customStyle="1" w:styleId="Code"><w:name w:val="Code"/><w:basedOn w:val="Normal"/>
<w:pPr><w:shd w:val="clear" w:color="auto" w:fill="F3EFF7"/><w:spacing w:after="120" w:line="240" w:lineRule="auto"/></w:pPr>
<w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:sz w:val="20"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:qFormat/>
<w:pPr><w:spacing w:after="40"/><w:contextualSpacing/></w:pPr></w:style>
<w:style w:type="character" w:styleId="Hyperlink"><w:name w:val="Hyperlink"/><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr></w:style>
<w:style w:type="character" w:customStyle="1" w:styleId="InlineCode"><w:name w:val="Inline Code"/>
<w:rPr><w:rFonts w:ascii="Courier New" w:hAnsi="Courier New" w:cs="Courier New"/><w:shd w:val="clear" w:color="auto" w:fill="F3EFF7"/></w:rPr></w:style>
</w:styles>`

// docxHeadingStyles are Heading1 to Heading6, sized like the editor's.
func docxHeadingStyles() string {
	sizes := []int{40, 32, 28, 24, 22, 22} // half-points
	var b strings.Builder
	for i, sz := range sizes {
		fmt.Fprintf(&b, `<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/>`+
			`<w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:qFormat/>`+
			`<w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:outlineLvl w:val="%d"/></w:pPr>`+
			`<w:rPr><w:b/><w:sz w:val="%d"/><w:szCs w:val="%d"/></w:rPr></w:style>`+"\n", i+1, i+1, i, sz, sz)
	}
	return b.String()
}

func renderDOCX(title string, blocks []block) ([]byte, error) {
	d := &docxWriter{}
	d.body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" ` +
		`xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" ` +
		`xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing" ` +
		`xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" ` +
		`xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><w:body>` + "\n")
	for i, bl := range blocks {
		switch bl.kind {
		case heading:
			d.para(fmt.Sprintf(`<w:pStyle w:val="Heading%d"/>`, bl.level), bl.lines)
		case paragraph:
			d.para("", bl.lines)
		case quote:
			d.para(`<w:pStyle w:val="Quote"/>`, bl.lines)
		case code:
			d.codeBlock(bl.code)
		case divider:
			d.body.WriteString(`<w:p><w:pPr><w:pBdr><w:bottom w:val="single" w:sz="6" w:space="1" w:color="BBBBBB"/></w:pBdr></w:pPr></w:p>` + "\n")
		case figure:
			if bl.pic != nil {
				d.picture(bl)
			} else {
				d.para("", [][]inline{{{text: imageLabel(bl.alt, bl.src)}}})
			}
		case bullet, numbered, task:
			// A numbered list restarts where a new one begins, which in Word
			// takes a numbering instance of its own.
			if bl.kind == numbered && (i == 0 || blocks[i-1].kind != numbered && blocks[i-1].level <= bl.level) {
				d.numLists = append(d.numLists, bl.number)
			}
			d.listItem(bl)
		}
	}
	d.body.WriteString(`<w:sectPr/></w:body></w:document>`)

	var out bytes.Buffer
	z := zip.NewWriter(&out)
	files := []struct{ name, data string }{
		{"[Content_Types].xml", d.contentTypes()},
		{"_rels/.rels", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/>
<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
</Relationships>`},
		{"docProps/core.xml", `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" ` +
			`xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" ` +
			`xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
			`<dc:title>` + xmlText(title) + `</dc:title><dc:creator>Atlas Notes</dc:creator>` +
			`<dcterms:created xsi:type="dcterms:W3CDTF">` + time.Now().UTC().Format(time.RFC3339) + `</dcterms:created>` +
			`</cp:coreProperties>`},
		{"word/_rels/document.xml.rels", d.rels()},
		{"word/document.xml", d.body.String()},
		{"word/styles.xml", fmt.Sprintf(docxStyles, docxHeadingStyles())},
		{"word/numbering.xml", d.numbering()},
	}
	for _, f := range files {
		w, err := z.Create(f.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.data)); err != nil {
			return nil, err
		}
	}
	// Each picture is a part of its own, as it came: an image file is already
	// compressed, and Word does not look inside it.
	for i, p := range d.media {
		w, err := z.Create(fmt.Sprintf("word/media/image%d.%s", i+1, p.ext))
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

// docxWriter accumulates the document body, and the links, pictures and
// numbered lists it needs relationships and numbering definitions for.
type docxWriter struct {
	body     strings.Builder
	links    []string   // hyperlink targets; each is relationship rId(100+index)
	numLists []int      // the starting number of each numbered list, in order
	media    []*picture // the distinct pictures; media[i] is word/media/image(i+1)
	drawings int        // how many pictures are placed, which is the last docPr id used
}

// numbering ids: 1 is every bullet and checklist list; 10 onwards are the
// numbered lists, one each.
const (
	docxBulletNum    = 1
	docxNumberedBase = 10
)

func (d *docxWriter) para(pPr string, lines [][]inline) {
	d.body.WriteString("<w:p>")
	if pPr != "" {
		d.body.WriteString("<w:pPr>" + pPr + "</w:pPr>")
	}
	for i, l := range lines {
		if i > 0 {
			d.body.WriteString("<w:r><w:br/></w:r>")
		}
		d.runs(l)
	}
	d.body.WriteString("</w:p>\n")
}

func (d *docxWriter) listItem(bl block) {
	num := docxBulletNum
	if bl.kind == numbered {
		if len(d.numLists) == 0 {
			d.numLists = append(d.numLists, bl.number)
		}
		num = docxNumberedBase + len(d.numLists) - 1
	}
	pPr := fmt.Sprintf(`<w:pStyle w:val="ListParagraph"/><w:numPr><w:ilvl w:val="%d"/><w:numId w:val="%d"/></w:numPr>`,
		min(bl.level, 8), num)
	if bl.kind == task {
		// Checklist items take the list's indentation without its bullet.
		pPr = fmt.Sprintf(`<w:pStyle w:val="ListParagraph"/><w:ind w:left="%d"/>`, 360*(bl.level+1))
	}
	d.body.WriteString("<w:p><w:pPr>" + pPr + "</w:pPr>")
	if bl.kind == task {
		// The box in a font that has it: the document's own font may not, and
		// a missing glyph draws as nothing at all.
		d.body.WriteString(`<w:r><w:rPr><w:rFonts w:ascii="Segoe UI Symbol" w:hAnsi="Segoe UI Symbol" w:cs="Segoe UI Symbol"/></w:rPr><w:t xml:space="preserve">` + checkbox(bl.checked) + `</w:t></w:r>`)
		line := bl.lines[0]
		if bl.checked {
			line = append([]inline(nil), line...)
			for i := range line {
				line[i].strike = true
			}
		}
		d.runs(line)
		if bl.detail != "" {
			d.body.WriteString(`<w:r><w:rPr><w:color w:val="777777"/></w:rPr><w:t xml:space="preserve"> (` + xmlText(bl.detail) + `)</w:t></w:r>`)
		}
	} else {
		d.runs(bl.lines[0])
	}
	d.body.WriteString("</w:p>\n")
}

func (d *docxWriter) codeBlock(text string) {
	d.body.WriteString(`<w:p><w:pPr><w:pStyle w:val="Code"/></w:pPr>`)
	for i, l := range strings.Split(text, "\n") {
		if i > 0 {
			d.body.WriteString("<w:r><w:br/></w:r>")
		}
		if l != "" {
			d.body.WriteString(`<w:r><w:t xml:space="preserve">` + xmlText(l) + `</w:t></w:r>`)
		}
	}
	d.body.WriteString("</w:p>\n")
}

func (d *docxWriter) runs(runs []inline) {
	for _, r := range runs {
		if r.link != "" {
			d.links = append(d.links, r.link)
			fmt.Fprintf(&d.body, `<w:hyperlink r:id="rId%d">`, 100+len(d.links)-1)
			d.run(r)
			d.body.WriteString("</w:hyperlink>")
			continue
		}
		d.run(r)
	}
}

func (d *docxWriter) run(r inline) {
	var rPr strings.Builder
	if r.link != "" {
		rPr.WriteString(`<w:rStyle w:val="Hyperlink"/>`)
	} else if r.mono {
		rPr.WriteString(`<w:rStyle w:val="InlineCode"/>`)
	}
	if r.bold {
		rPr.WriteString("<w:b/>")
	}
	if r.italic {
		rPr.WriteString("<w:i/>")
	}
	if r.strike {
		rPr.WriteString("<w:strike/>")
	}
	d.body.WriteString("<w:r>")
	if rPr.Len() > 0 {
		d.body.WriteString("<w:rPr>" + rPr.String() + "</w:rPr>")
	}
	d.body.WriteString(`<w:t xml:space="preserve">` + xmlText(r.text) + `</w:t></w:r>`)
}

func (d *docxWriter) rels() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + "\n" +
		`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>` + "\n" +
		`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/numbering" Target="numbering.xml"/>` + "\n")
	for i, l := range d.links {
		fmt.Fprintf(&b, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="%s" TargetMode="External"/>`+"\n",
			100+i, xmlAttr(safeLink(l)))
	}
	for i, p := range d.media {
		fmt.Fprintf(&b, `<Relationship Id="%s" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image%d.%s"/>`+"\n",
			imageRelID(i), i+1, p.ext)
	}
	b.WriteString("</Relationships>")
	return b.String()
}

// imageRelID is the relationship a picture's part is reached by. Its name is
// its own rather than a number, so it can never meet a link's rId(100+n).
func imageRelID(i int) string { return fmt.Sprintf("rIdImage%d", i+1) }

// contentTypes says what each part is. A picture's part is named by its
// extension, so each extension in use is declared once.
func (d *docxWriter) contentTypes() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
<Default Extension="xml" ContentType="application/xml"/>
`)
	seen := map[string]bool{}
	for _, p := range d.media {
		if !seen[p.ext] {
			seen[p.ext] = true
			fmt.Fprintf(&b, `<Default Extension="%s" ContentType="%s"/>`+"\n", p.ext, p.mime)
		}
	}
	b.WriteString(`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
<Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/>
<Override PartName="/word/numbering.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.numbering+xml"/>
<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`)
	return b.String()
}

// picture places a picture in a paragraph of its own, as Word does when one is
// inserted. A picture used twice in a note is one part in the file with two
// drawings pointing at it, and each drawing has an id of its own, which Word
// wants to be unique across the document.
func (d *docxWriter) picture(bl block) {
	idx := -1
	for i, m := range d.media {
		if m == bl.pic {
			idx = i
		}
	}
	if idx < 0 {
		d.media = append(d.media, bl.pic)
		idx = len(d.media) - 1
	}
	d.drawings++
	id := d.drawings
	w, h := bl.pic.inches()
	cx, cy := emu(w), emu(h)
	fmt.Fprintf(&d.body, `<w:p><w:r><w:drawing><wp:inline distT="0" distB="0" distL="0" distR="0">`+
		`<wp:extent cx="%d" cy="%d"/>`+
		`<wp:docPr id="%d" name="Picture %d" descr="%s"/>`+
		`<wp:cNvGraphicFramePr><a:graphicFrameLocks noChangeAspect="1"/></wp:cNvGraphicFramePr>`+
		`<a:graphic><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture">`+
		`<pic:pic><pic:nvPicPr><pic:cNvPr id="%d" name="image%d.%s"/><pic:cNvPicPr/></pic:nvPicPr>`+
		`<pic:blipFill><a:blip r:embed="%s"/><a:stretch><a:fillRect/></a:stretch></pic:blipFill>`+
		`<pic:spPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="%d" cy="%d"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom></pic:spPr>`+
		`</pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>`+"\n",
		cx, cy, id, id, xmlAttr(imageLabel(bl.alt, bl.src)), id, idx+1, bl.pic.ext, imageRelID(idx), cx, cy)
}

func (d *docxWriter) numbering() string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n" +
		`<w:numbering xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">` + "\n")
	bullets := []string{"•", "◦", "▪"} // • ◦ ▪
	b.WriteString(`<w:abstractNum w:abstractNumId="0"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for lvl := 0; lvl < 9; lvl++ {
		fmt.Fprintf(&b, `<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="bullet"/><w:lvlText w:val="%s"/>`+
			`<w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`,
			lvl, bullets[lvl%3], 720+360*lvl)
	}
	b.WriteString("</w:abstractNum>\n")
	formats := []string{"decimal", "lowerLetter", "lowerRoman"}
	b.WriteString(`<w:abstractNum w:abstractNumId="1"><w:multiLevelType w:val="hybridMultilevel"/>`)
	for lvl := 0; lvl < 9; lvl++ {
		fmt.Fprintf(&b, `<w:lvl w:ilvl="%d"><w:start w:val="1"/><w:numFmt w:val="%s"/><w:lvlText w:val="%%%d."/>`+
			`<w:lvlJc w:val="left"/><w:pPr><w:ind w:left="%d" w:hanging="360"/></w:pPr></w:lvl>`,
			lvl, formats[lvl%3], lvl+1, 720+360*lvl)
	}
	b.WriteString("</w:abstractNum>\n")
	fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="0"/></w:num>`+"\n", docxBulletNum)
	for i, start := range d.numLists {
		fmt.Fprintf(&b, `<w:num w:numId="%d"><w:abstractNumId w:val="1"/>`+
			`<w:lvlOverride w:ilvl="0"><w:startOverride w:val="%d"/></w:lvlOverride></w:num>`+"\n",
			docxNumberedBase+i, max(start, 1))
	}
	b.WriteString("</w:numbering>")
	return b.String()
}

// xmlText escapes text for an XML element, and drops the characters XML 1.0
// cannot hold at all: a stray control character in a note would otherwise
// make the whole document unreadable.
func xmlText(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r == '\t', r == '\n', r == '\r':
			b.WriteRune(r)
		case r < 0x20, r == 0xFFFE, r == 0xFFFF:
			// not allowed in XML 1.0
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// xmlAttr escapes text for an attribute value.
func xmlAttr(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(xmlText(s), `"`, "&quot;"), "'", "&apos;")
}
