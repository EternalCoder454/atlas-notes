package bridge

import (
	"path"

	"atlas-notes/internal/export"
)

// ExportFormats lists what a note can be exported as, as JSON, in the order
// they are offered.
func ExportFormats() (string, error) {
	type format struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Ext  string `json:"ext"`
		MIME string `json:"mime"`
	}
	out := make([]format, 0, len(export.Formats))
	for _, f := range export.Formats {
		out = append(out, format{f.ID, f.Name, f.Ext, f.MIME})
	}
	return toJSON(out)
}

// ExportNote renders a note in a format, for the app to write wherever the
// user chose. A locked note has to be unlocked first, and fails like ReadNote.
func ExportNote(rel, format string) ([]byte, error) {
	text, err := ReadNote(rel)
	if err != nil {
		return nil, err
	}
	s, err := vault()
	if err != nil {
		return nil, err
	}
	// Pictures are read from the vault as the note names them, decrypted
	// when the note is protected, so the document carries them.
	opt := export.Options{Image: func(p string) ([]byte, error) { return s.ReadAttachment(rel, p) }}
	return export.RenderWith(format, path.Base(rel), text, opt)
}

// ExportFileName is the name an export of a note is offered under.
func ExportFileName(rel, format string) string {
	f, ok := export.Lookup(format)
	if !ok {
		return path.Base(rel)
	}
	return export.FileName(path.Base(rel), f)
}
