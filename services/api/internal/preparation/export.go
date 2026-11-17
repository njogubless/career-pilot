package preparation

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Export formats. DOCX is built with the standard library (single-column,
// real text, no tables or images) so applicant tracking systems can parse
// it; PDF is produced by the browser from the print view (ADR-021).
const (
	FormatDOCX = "docx"
	FormatTXT  = "txt"
	FormatMD   = "md"
)

var formatMIME = map[string]string{
	FormatDOCX: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	FormatTXT:  "text/plain; charset=utf-8",
	FormatMD:   "text/markdown; charset=utf-8",
}

type lineKind int

const (
	lTitle lineKind = iota
	lContact
	lHeading
	lEntry
	lBullet
	lPara
	lMissing
)

type line struct {
	kind        lineKind
	text, right string
}

// layout flattens content into presentation lines shared by all formats.
func layout(c Content) []line {
	var out []line
	name := ""
	for _, s := range c.Sections {
		showHeading := s.Key != "contact" && (c.Kind == KindCV || c.Kind == KindAnswers)
		if showHeading && len(s.Blocks) > 0 {
			out = append(out, line{kind: lHeading, text: s.Title})
		}
		for _, b := range s.Blocks {
			switch b.Type {
			case "contact":
				name = b.Text
				out = append(out, line{kind: lTitle, text: b.Text})
				if ct := join(" · ", b.Meta["email"], b.Meta["location"], b.Meta["links"]); ct != "" {
					out = append(out, line{kind: lContact, text: ct})
				}
			case "entry":
				out = append(out, line{kind: lEntry, text: b.Text, right: join(" · ", b.Meta["location"], b.Meta["dates"])})
			case "bullet":
				out = append(out, line{kind: lBullet, text: b.Text})
			case "heading":
				out = append(out, line{kind: lHeading, text: b.Text})
			case "answer":
				if b.Text == "" {
					out = append(out, line{kind: lMissing, text: "Answer needed: " + b.Meta["question_for_user"]})
				} else {
					out = append(out, line{kind: lPara, text: b.Text})
				}
			default: // paragraph, skills
				text := b.Text
				if b.Meta["role"] == "closing" && name != "" {
					text += "\n" + name
				}
				if text != "" {
					out = append(out, line{kind: lPara, text: text})
				}
			}
		}
	}
	return out
}

func renderText(c Content) []byte {
	var b strings.Builder
	for i, l := range layout(c) {
		switch l.kind {
		case lHeading:
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(strings.ToUpper(l.text) + "\n")
		case lEntry:
			b.WriteString(l.text)
			if l.right != "" {
				b.WriteString("  (" + l.right + ")")
			}
			b.WriteString("\n")
		case lBullet:
			b.WriteString("• " + l.text + "\n")
		case lContact:
			b.WriteString(l.text + "\n\n")
		case lMissing:
			b.WriteString("[" + l.text + "]\n\n")
		default:
			b.WriteString(l.text + "\n")
			if l.kind == lPara {
				b.WriteString("\n")
			}
		}
	}
	return []byte(strings.TrimSpace(blankRuns.ReplaceAllString(b.String(), "\n\n")) + "\n")
}

var blankRuns = regexp.MustCompile(`\n{3,}`)

var mdSpecial = regexp.MustCompile(`([\\*_\[\]#<>` + "`" + `])`)

func mdEscape(s string) string { return mdSpecial.ReplaceAllString(s, `\$1`) }

func renderMarkdown(c Content) []byte {
	var b strings.Builder
	for _, l := range layout(c) {
		t := mdEscape(l.text)
		switch l.kind {
		case lTitle:
			b.WriteString("# " + t + "\n\n")
		case lContact:
			b.WriteString(t + "\n\n")
		case lHeading:
			b.WriteString("## " + t + "\n\n")
		case lEntry:
			b.WriteString("**" + t + "**")
			if l.right != "" {
				b.WriteString(" · " + mdEscape(l.right))
			}
			b.WriteString("\n\n")
		case lBullet:
			b.WriteString("- " + t + "\n")
		case lMissing:
			b.WriteString("> " + t + "\n\n")
		default:
			b.WriteString(strings.ReplaceAll(t, "\n", "  \n") + "\n\n")
		}
	}
	return []byte(strings.TrimRight(b.String(), "\n") + "\n")
}

// ---- DOCX ----

const (
	docxContentTypes = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`
	docxRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	docxDocRels = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`
	docxStyles = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Calibri" w:hAnsi="Calibri" w:eastAsia="Calibri" w:cs="Calibri"/><w:sz w:val="21"/><w:szCs w:val="21"/><w:lang w:val="en-GB"/></w:rPr></w:rPrDefault><w:pPrDefault><w:pPr><w:spacing w:after="80" w:line="264" w:lineRule="auto"/></w:pPr></w:pPrDefault></w:docDefaults>
<w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style>
<w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="40"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Contact"><w:name w:val="Contact"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="200"/></w:pPr><w:rPr><w:color w:val="555555"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="240" w:after="80"/><w:pBdr><w:bottom w:val="single" w:sz="4" w:space="1" w:color="999999"/></w:pBdr><w:outlineLvl w:val="0"/></w:pPr><w:rPr><w:b/><w:caps/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Entry"><w:name w:val="Entry"/><w:basedOn w:val="Normal"/><w:pPr><w:keepNext/><w:spacing w:before="120" w:after="40"/></w:pPr><w:rPr><w:b/></w:rPr></w:style>
<w:style w:type="paragraph" w:styleId="Bullet"><w:name w:val="Bullet"/><w:basedOn w:val="Normal"/><w:pPr><w:spacing w:after="40"/><w:ind w:left="360" w:hanging="220"/></w:pPr></w:style>
<w:style w:type="paragraph" w:styleId="Missing"><w:name w:val="Missing"/><w:basedOn w:val="Normal"/><w:rPr><w:i/><w:color w:val="A0522D"/></w:rPr></w:style>
</w:styles>`
	// A4 with 2 cm margins; the right tab stop sits on the right margin.
	docxSect = `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1134" w:right="1134" w:bottom="1134" w:left="1134" w:header="567" w:footer="567" w:gutter="0"/></w:sectPr>`
	rightTab = `<w:tabs><w:tab w:val="right" w:pos="9638"/></w:tabs>`
)

// xmlSafe drops characters XML 1.0 cannot represent.
func xmlSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || (r >= 0x20 && r != 0xFFFE && r != 0xFFFF && !(r >= 0xD800 && r <= 0xDFFF)) {
			return r
		}
		return -1
	}, s)
}

func run(b *bytes.Buffer, text string) {
	for i, part := range strings.Split(xmlSafe(text), "\n") {
		if i > 0 {
			b.WriteString(`<w:r><w:br/></w:r>`)
		}
		b.WriteString(`<w:r><w:t xml:space="preserve">`)
		_ = xml.EscapeText(b, []byte(part))
		b.WriteString(`</w:t></w:r>`)
	}
}

func para(b *bytes.Buffer, style, text, right string) {
	b.WriteString(`<w:p><w:pPr><w:pStyle w:val="` + style + `"/>`)
	if right != "" {
		b.WriteString(rightTab)
	}
	b.WriteString(`</w:pPr>`)
	run(b, text)
	if right != "" {
		b.WriteString(`<w:r><w:rPr><w:b w:val="0"/></w:rPr><w:tab/><w:t xml:space="preserve">`)
		_ = xml.EscapeText(b, []byte(xmlSafe(right)))
		b.WriteString(`</w:t></w:r>`)
	}
	b.WriteString(`</w:p>`)
}

func renderDOCX(c Content) ([]byte, error) {
	var body bytes.Buffer
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, l := range layout(c) {
		switch l.kind {
		case lTitle:
			para(&body, "Title", l.text, "")
		case lContact:
			para(&body, "Contact", l.text, "")
		case lHeading:
			para(&body, "Heading1", l.text, "")
		case lEntry:
			para(&body, "Entry", l.text, l.right)
		case lBullet:
			para(&body, "Bullet", "• "+l.text, "")
		case lMissing:
			para(&body, "Missing", "["+l.text+"]", "")
		default:
			para(&body, "Normal", l.text, "")
		}
	}
	body.WriteString(docxSect + `</w:body></w:document>`)

	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	// Fixed timestamps keep exports byte-for-byte reproducible.
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, f := range []struct{ name, data string }{
		{"[Content_Types].xml", docxContentTypes},
		{"_rels/.rels", docxRels},
		{"word/_rels/document.xml.rels", docxDocRels},
		{"word/styles.xml", docxStyles},
		{"word/document.xml", body.String()},
	} {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: stamp})
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(f.data)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func slug(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	return strings.Trim(b.String(), "-")
}

// render produces the export bytes, MIME type and a safe file name.
func render(c Content, format, company string, versionNo int) ([]byte, string, string, error) {
	mime, ok := formatMIME[format]
	if !ok {
		return nil, "", "", fmt.Errorf("unsupported format %q", format)
	}
	var data []byte
	var err error
	switch format {
	case FormatDOCX:
		data, err = renderDOCX(c)
	case FormatTXT:
		data = renderText(c)
	case FormatMD:
		data = renderMarkdown(c)
	}
	if err != nil {
		return nil, "", "", err
	}
	name := join("-", slug(kindTitles[c.Kind]), slug(company))
	return data, mime, fmt.Sprintf("%s-v%d.%s", name, versionNo, format), nil
}
