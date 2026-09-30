package editor

import (
	"container/list"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	coreglib "github.com/diamondburned/gotk4/pkg/core/glib"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/markup"
)

// This file shows pictures inside a note, and adds them when one is pasted or
// dropped.
//
// A picture is not part of the text. The line "![alt](path)" stays in the buffer
// exactly as written, so the saved note, the index and undo never learn about
// pictures. What the editor adds is a widget laid over the text view just below
// that line, and some blank space under the line so the text after it does not
// run underneath the picture. Child anchors are not used for this on purpose:
// Content() reads a line that starts with an anchor as a checkbox.
//
// Only a line whose sole content is one image counts. An image in the middle of
// a sentence stays plain text, which is what a Markdown reader without pictures
// would show too.
//
// The parts that need no GTK (spotting an image line, fitting a size, deciding
// where newlines go, the texture cache) are plain functions and are tested on
// their own.
//
// Tables are laid over the text the same way, and share the placement below (see
// block and placeBlocks); tables.go says the rest.

const (
	// maxImageHeight keeps a tall picture from taking over the screen.
	maxImageHeight = 480
	// imageGap is the blank space left under a picture.
	imageGap = 8
	// imagePadStep is what a picture's height is rounded up to before it becomes
	// blank space under its line. Notes tend to hold a few sizes of picture, and
	// a tag per exact height would be a tag per pixel.
	imagePadStep = 16
	// imageNoteHeight is the room kept for the "Image not found" label.
	imageNoteHeight = 24
	// imageCacheSize is how many decoded pictures are kept for the open note.
	imageCacheSize = 32
	// maxImageBytes is the largest file taken from a drop or a pasted file.
	maxImageBytes = 50 << 20
	// maxPooledBoxes caps the widgets kept for reuse, as large as the checklist
	// row pool. A box the pool has no room for is one the bindings never free, so
	// a small cap turns every picture beyond it into a leak.
	maxPooledBoxes = 512
	// maxLoadsAtOnce is how many pictures are being read or waiting to be decoded
	// at the same time.
	maxLoadsAtOnce = 4
	// placeRetries is how often positions are tried again when the text layout
	// has not settled yet (a view that is not on screen does not lay itself out).
	placeRetries = 10
)

// loadSlots bounds how many pictures are loaded at once, across editors.
var loadSlots = make(chan struct{}, maxLoadsAtOnce)

// imageHit is an image line the render pass has just seen.
type imageHit struct {
	line int
	path string
}

// overlay is what anything laid over the text under a line has in common, a
// picture or a table: the line it belongs to (held by a mark, so that edits above
// it move it along), the spacing tag that keeps the text after the line clear of
// it, and where it was last put.
type overlay struct {
	mark  *gtk.TextMark
	pad   string // name of the spacing tag on the line, or "" for none
	shown bool   // the widget is in the view as an overlay
	x, y  int    // where the overlay was last put
}

// imageItem is one picture: its overlay bookkeeping, where its data stands, and
// its widget.
type imageItem struct {
	overlay
	path       string
	tex        *gdk.Texture
	natW, natH int
	failed     bool
	box        *imageBox // nil until there is a picture or a failure note to show
}

// imageBox is the widget for one picture, kept together so it can be reused for
// another one. Widgets the bindings wrap are held until the process ends, so
// dropping and rebuilding them per picture would grow memory for as long as the
// app runs. For the same reason its click handler holds the editor and a number,
// never the widget itself.
type imageBox struct {
	serial int
	box    *gtk.Box
	pic    *gtk.Picture
	note   *gtk.Label
}

// imageState is the editor's picture bookkeeping.
type imageState struct {
	items  []*imageItem
	hits   []imageHit         // image lines seen by the render pass in progress
	byLine map[int]*imageItem // items by line, built when a pass needs it
	cache  *lru[*gdk.Texture] // decoded pictures of the open note, by path
	loads  map[string]bool    // paths being loaded right now
	gen    int                // moves on when another note opens, so late results are dropped
	serial int                // last box number handed out
	pool   []*imageBox        // boxes kept for reuse
	queued bool               // a placement pass is waiting for an idle moment
	retry  int                // placement passes repeated while the layout settles
	paste  bool               // the default paste is being let through
	// A placement pass skipped its spacing changes because text was selected;
	// they are made once the selection collapses (see New).
	padDeferred bool
}

// imagesOn is whether the app supplied a way to load pictures. Without one the
// editor keeps treating an image line as plain text.
func (e *Editor) imagesOn() bool { return e.LoadImage != nil }

// installImages connects everything pictures need. It runs once, from New.
func (e *Editor) installImages() {
	s := &e.img
	s.cache = newLRU[*gdk.Texture](imageCacheSize)
	s.loads = map[string]bool{}

	// Positions have to be redone when the text is laid out again. The view has
	// no signal for that, but its scroll range moves whenever the total height
	// does (a line wrapped differently, a heading changed size, blank space was
	// added), and its width shows in the horizontal page size.
	again := func() { e.queuePlace() }
	e.scroll.VAdjustment().NotifyProperty("upper", again)
	e.scroll.HAdjustment().NotifyProperty("page-size", again)
	e.view.ConnectMap(again)

	e.view.ConnectPasteClipboard(e.onPaste)

	drop := gtk.NewDropTarget(gdk.GTypeFileList, gdk.ActionCopy)
	drop.ConnectDrop(func(value *coreglib.Value, x, y float64) bool {
		return e.onDrop(value, x, y)
	})
	e.view.AddController(drop)
}

// clearImages forgets every picture. Opening another note replaces the text, so
// the lines the pictures belonged to are gone; the cache goes too, since the next
// note is most likely to want different pictures and a texture can be large.
func (e *Editor) clearImages() {
	s := &e.img
	for _, it := range s.items {
		e.dropItem(it)
	}
	s.items = nil
	s.hits = s.hits[:0]
	s.byLine = nil
	s.gen++
	s.loads = map[string]bool{}
	if s.cache != nil {
		s.cache.clear()
	}
}

// ---- Image lines -----------------------------------------------------------

// imageLineSpan reports whether a line is an image line: one whose only content,
// spaces aside, is a single image. It returns that image.
func imageLineSpan(line string) (markup.Span, bool) {
	found := markupSpans(line)
	if len(found) != 1 || found[0].Kind != markup.KindImage || found[0].Target == "" {
		return markup.Span{}, false
	}
	sp := found[0]
	if strings.TrimSpace(line[:sp.Start]) != "" || strings.TrimSpace(line[sp.End:]) != "" {
		return markup.Span{}, false
	}
	return sp, true
}

// tagImageLine is the render pass's part for a line that mentions "![". For an
// image line it hides the text (unless the caret is on the line: a picture is one
// thing, so unlike an inline construct it shows all of its Markdown or none),
// restores the spacing that the pass just stripped, and notes the line for
// syncImages.
func (e *Editor) tagImageLine(lineNum int, line string, reveal bool) {
	sp, ok := imageLineSpan(line)
	if !ok {
		return
	}
	e.img.hits = append(e.img.hits, imageHit{lineNum, sp.Target})
	if !reveal {
		// The same choice the other markers make: hidden outright, or dimmed when
		// ATLAS_NO_HIDE is set.
		name := "invisible"
		if !hideMarkers {
			name = "marker"
		}
		for _, s := range charSpans(line, []span{{name, sp.Start, sp.End}}) {
			e.applyTag(s.tag, lineNum, s.start, s.end)
		}
	}
	if it := e.itemAt(lineNum); it != nil && it.path == sp.Target && it.pad != "" {
		e.applyPad(&it.overlay, lineNum, it.pad)
	}
}

// itemLine is the line a picture's mark is on, or -1 when the mark is gone.
func (e *Editor) itemLine(it *imageItem) int { return e.markLine(it.mark) }

// markLine is the line a mark is on, or -1 when the mark is gone.
func (e *Editor) markLine(mark *gtk.TextMark) int {
	if mark.Deleted() {
		return -1
	}
	iter := e.buffer.IterAtMark(mark)
	if iter == nil {
		return -1
	}
	return iter.Line()
}

// itemAt finds the picture that belongs to a line.
func (e *Editor) itemAt(line int) *imageItem {
	s := &e.img
	if len(s.items) == 0 {
		return nil
	}
	if s.byLine == nil {
		s.byLine = make(map[int]*imageItem, len(s.items))
		for _, it := range s.items {
			if l := e.itemLine(it); l >= 0 {
				s.byLine[l] = it
			}
		}
	}
	return s.byLine[line]
}

// syncImages runs after the render pass has tagged lines [from, to]. Pictures
// whose line is in that range but no longer an image line (or points somewhere
// else now) go away, and new image lines get a picture. A picture outside the
// range was not looked at and is left alone.
func (e *Editor) syncImages(from, to int) {
	s := &e.img
	hits := s.hits
	s.hits = s.hits[:0]
	s.byLine = nil
	if len(s.items) == 0 && len(hits) == 0 {
		return
	}

	hitPath := func(line int) (string, bool) {
		for _, h := range hits {
			if h.line == line {
				return h.path, true
			}
		}
		return "", false
	}
	seen := map[int]bool{}
	kept := s.items[:0]
	for _, it := range s.items {
		line := e.itemLine(it)
		path, isHit := hitPath(line)
		inRange := line >= from && line <= to
		// Deleting text leaves the marks that were inside it piled on one line;
		// only the first of them is kept.
		if line < 0 || seen[line] || (inRange && (!isHit || path != it.path)) {
			e.dropItem(it)
			continue
		}
		seen[line] = true
		kept = append(kept, it)
	}
	for i := len(kept); i < len(s.items); i++ {
		s.items[i] = nil
	}
	s.items = kept

	for _, h := range hits {
		if !seen[h.line] {
			e.addItem(h)
		}
	}
	e.queuePlace()
}

// addItem starts a picture for an image line: from the cache when it is there,
// otherwise by loading it off the main thread.
func (e *Editor) addItem(h imageHit) {
	iter, ok := e.buffer.IterAtLine(h.line)
	if !ok {
		return
	}
	// Left gravity keeps the mark in front of anything typed at the start of the
	// line, so it stays on this line.
	it := &imageItem{overlay: overlay{mark: e.buffer.CreateMark("", iter, true)}, path: h.path}
	e.img.items = append(e.img.items, it)
	if tex, ok := e.img.cache.get(h.path); ok {
		e.setTexture(it, tex, nil)
		return
	}
	e.startLoad(h.path)
}

// dropItem lets go of a picture's widget and mark.
func (e *Editor) dropItem(it *imageItem) {
	if it.box != nil {
		if it.shown {
			e.view.Remove(it.box.box)
		}
		e.giveBox(it.box)
		it.box = nil
	}
	if !it.mark.Deleted() {
		e.buffer.DeleteMark(it.mark)
	}
}

// ---- Loading ---------------------------------------------------------------

// startLoad asks the app for a picture's bytes on a goroutine, so that opening a
// note never waits for its pictures. Only the path and the generation cross into
// it; everything that touches GTK happens back on the main thread.
func (e *Editor) startLoad(path string) {
	s := &e.img
	if s.loads[path] {
		return
	}
	s.loads[path] = true
	gen, load := s.gen, e.LoadImage
	go func() {
		// A note with a hundred pictures must not hold a hundred files in memory
		// while the main thread decodes them one at a time, so a slot is kept
		// until that decode has happened.
		loadSlots <- struct{}{}
		data, err := load(path)
		coreglib.IdleAdd(func() bool {
			e.imageLoaded(gen, path, data, err)
			<-loadSlots
			return false
		})
	}()
}

// imageLoaded turns loaded bytes into a texture and gives it to every picture
// that was waiting for that path.
func (e *Editor) imageLoaded(gen int, path string, data []byte, err error) {
	s := &e.img
	if gen != s.gen {
		return // another note is open now
	}
	delete(s.loads, path)
	var tex *gdk.Texture
	if err == nil {
		tex, err = gdk.NewTextureFromBytes(glib.NewBytes(data))
	}
	if err == nil {
		s.cache.put(path, tex)
	}
	for _, it := range s.items {
		if it.path == path && it.tex == nil && !it.failed {
			e.setTexture(it, tex, err)
		}
	}
	e.queuePlace()
}

// setTexture gives a picture its result, a texture or a failure, and dresses its
// widget to match.
func (e *Editor) setTexture(it *imageItem, tex *gdk.Texture, err error) {
	if err != nil || tex == nil {
		it.failed = true
	} else {
		it.tex = tex
		it.natW, it.natH = tex.Width(), tex.Height()
	}
	if it.box == nil {
		it.box = e.takeBox(it.tex)
	}
	b := it.box
	if it.failed {
		b.pic.SetPaintable(nil)
		b.pic.SetVisible(false)
		b.box.SetSizeRequest(-1, -1)
		b.note.SetText("Image not found: " + it.path)
		b.note.SetVisible(true)
		return
	}
	b.note.SetVisible(false)
	b.pic.SetVisible(true)
	b.pic.SetPaintable(it.tex)
}

// takeBox hands out a widget, from the pool when it holds one.
func (e *Editor) takeBox(tex *gdk.Texture) *imageBox {
	s := &e.img
	if n := len(s.pool); n > 0 {
		b := s.pool[n-1]
		s.pool = s.pool[:n-1]
		return b
	}
	s.serial++
	b := &imageBox{serial: s.serial}
	if tex != nil {
		b.pic = gtk.NewPictureForPaintable(tex)
	} else {
		b.pic = gtk.NewPicture()
	}
	b.pic.SetContentFit(gtk.ContentFitContain)
	b.pic.SetCanShrink(true)

	b.note = gtk.NewLabel("")
	b.note.AddCSSClass("dim-label")
	b.note.SetXAlign(0)
	b.note.SetVisible(false)

	b.box = gtk.NewBox(gtk.OrientationVertical, 0)
	b.box.AddCSSClass("atlas-image")
	b.box.SetCursorFromName("default")
	b.box.Append(b.pic)
	b.box.Append(b.note)

	// A press on a picture is claimed, so the text view underneath never sees it:
	// no caret move, no selection. A double-click opens the picture. The handler
	// is handed the gesture rather than closing over it, for the reason given at
	// imageBox: a handler that holds the widget it sits on keeps it alive for good.
	click := gtk.NewGestureClick()
	click.SetButton(1)
	serial := b.serial
	click.Connect("pressed", func(g *gtk.GestureClick, nPress int) {
		g.SetState(gtk.EventSequenceClaimed)
		if nPress == 2 {
			e.openImage(serial)
		}
	})
	b.box.AddController(click)
	return b
}

// giveBox takes a widget back into the pool, or lets it go when the pool is full.
func (e *Editor) giveBox(b *imageBox) {
	b.pic.SetPaintable(nil) // do not keep a texture alive from the pool
	s := &e.img
	if len(s.pool) < maxPooledBoxes {
		s.pool = append(s.pool, b)
	}
}

// openImage hands a double-clicked picture to the app, from an idle callback
// for the same reason openLink does.
func (e *Editor) openImage(serial int) {
	cb := e.OnOpenImage
	if cb == nil {
		return
	}
	for _, it := range e.img.items {
		if it.box != nil && it.box.serial == serial {
			path := it.path
			coreglib.IdleAdd(func() bool {
				cb(path)
				return false
			})
			return
		}
	}
}

// ---- Size and position -----------------------------------------------------

// fitImage is the size a picture is drawn at: the width the view has to give, but
// never more than the picture's own, and never taller than maxImageHeight, with
// the proportions kept. A view with no width yet (it has not been allocated)
// gives 0, 0, which means "not ready" rather than a sliver. Once there is any
// width at all the result is at least one pixel each way.
func fitImage(natW, natH, availW int) (w, h int) {
	if natW <= 0 || natH <= 0 || availW <= 0 {
		return 0, 0
	}
	w, h = natW, natH
	if w > availW {
		h = scaleDim(natH, availW, natW)
		w = availW
	}
	if h > maxImageHeight {
		w = scaleDim(w, maxImageHeight, h)
		h = maxImageHeight
	}
	return max(w, 1), max(h, 1)
}

// scaleDim is v*num/den, rounded to the nearest whole number.
func scaleDim(v, num, den int) int { return (v*num + den/2) / den }

// imagePad names the spacing tag for a picture of height h and says how many
// pixels of space it puts under the line: the height rounded up to a step, plus
// the gap.
func imagePad(h int) (name string, px int) {
	step := (h + imagePadStep - 1) / imagePadStep * imagePadStep
	return "image-pad-" + strconv.Itoa(step), step + imageGap
}

// padPixels is the space the spacing tag called name puts under its line, and 0
// for no tag. Tables have tags of their own (see tablePad).
func padPixels(name string) int {
	if name == "" {
		return 0
	}
	if px, ok := strings.CutPrefix(name, tablePadPrefix); ok {
		n, _ := strconv.Atoi(px)
		return n
	}
	h, _ := strconv.Atoi(strings.TrimPrefix(name, "image-pad-"))
	_, px := imagePad(h)
	return px
}

// fit is the size this picture is drawn at, given the width the view has to give.
func (it *imageItem) fit(availW int) (w, h int) {
	if it.failed {
		if availW <= 0 {
			return 0, 0
		}
		return availW, imageNoteHeight
	}
	return fitImage(it.natW, it.natH, availW)
}

// block is something the placement pass puts under a line of text: a picture or a
// table. The pass is the same for both. What differs (how big the thing is, how
// much space its line needs) is asked of the block.
type block interface {
	over() *overlay
	// widget is what is laid over the text, or nil while there is nothing to show.
	widget() gtk.Widgetter
	// wantPad is the spacing tag the block's line should carry, given the width the
	// view has to give, or "" for none.
	wantPad(e *Editor, line, avail int) string
	// ready reports whether the block can be sized yet. A view with no width has
	// nothing to size against.
	ready(avail int) bool
	// resize gives the widget the size it is to be drawn at.
	resize(e *Editor, avail int)
}

func (it *imageItem) over() *overlay { return &it.overlay }

func (it *imageItem) widget() gtk.Widgetter {
	if it.box == nil {
		return nil
	}
	return it.box.box
}

func (it *imageItem) wantPad(_ *Editor, _, avail int) string {
	if _, h := it.fit(avail); h > 0 {
		name, _ := imagePad(h)
		return name
	}
	return ""
}

func (it *imageItem) ready(avail int) bool {
	w, _ := it.fit(avail)
	return w > 0
}

func (it *imageItem) resize(_ *Editor, avail int) {
	if it.failed {
		return
	}
	w, h := it.fit(avail)
	it.box.pic.SetSizeRequest(w, h)
	it.box.box.SetSizeRequest(w, h)
}

// queuePlace asks for the pictures and tables to be placed once the text has been
// laid out. Idle callbacks run after GTK has validated the layout, which is what
// makes the line positions below trustworthy. Asking twice is one pass.
func (e *Editor) queuePlace() {
	s := &e.img
	if s.queued || (len(s.items) == 0 && len(e.tbl.items) == 0 && len(e.emb.items) == 0 && len(e.rich.active) == 0 && len(e.items) == 0 && !e.props.on) {
		return
	}
	s.queued = true
	coreglib.IdleAdd(func() bool {
		s.queued = false
		e.placeBlocks()
		return false
	})
}

// placeBlocks sizes every picture and table and puts each under its line.
//
// It works in two steps. First each line gets the blank space its block needs;
// that changes the layout, so the positions are only read on the next pass, which
// the scroll range's change triggers and which is also queued here to be safe.
func (e *Editor) placeBlocks() {
	s := &e.img
	blocks := make([]block, 0, len(s.items)+len(e.tbl.items)+len(e.emb.items))
	for _, it := range s.items {
		blocks = append(blocks, it)
	}
	for _, it := range e.tbl.items {
		blocks = append(blocks, it)
	}
	for _, it := range e.emb.items {
		blocks = append(blocks, it)
	}
	if e.props.on {
		blocks = append(blocks, &e.props.item)
	}
	if len(blocks) == 0 {
		e.retryPlace(e.placeDecor() || e.placeChips())
		return
	}
	avail := e.view.Width() - e.view.LeftMargin() - e.view.RightMargin()

	// The spacing is a tag, and tags must not change while text is selected: the
	// person may be dragging, and GTK hit-tests the text with every move (see
	// reparse). The blocks keep the spacing they have until the selection
	// collapses, which queues this pass again.
	holdTags := e.handsBusy()
	if holdTags {
		e.whenHandsFree()
	}
	if holdTags {
		s.padDeferred = true
	}

	changed := false
	for _, b := range blocks {
		if holdTags || b.widget() == nil {
			continue
		}
		o := b.over()
		line := e.markLine(o.mark)
		if line < 0 {
			continue
		}
		want := b.wantPad(e, line, avail)
		if e.rich.foldedAt(line) {
			want = "" // folded away: no space for it either
		}
		if want != o.pad {
			e.applyPad(o, line, want)
			changed = true
		}
	}
	if changed {
		e.markLayoutStale()
		e.queuePlace()
		return
	}

	unsettled := false
	for _, b := range blocks {
		w := b.widget()
		if w == nil {
			continue
		}
		o := b.over()
		line := e.markLine(o.mark)
		if line < 0 {
			continue
		}
		if e.rich.foldedAt(line) {
			gtk.BaseWidget(w).SetVisible(false)
			continue
		}
		if !b.ready(avail) {
			// The view has no width yet. Its size arrives with a notification, and
			// the retry below covers a notification that came too early to count.
			gtk.BaseWidget(w).SetVisible(false)
			unsettled = true
			continue
		}
		iter, ok := e.buffer.IterAtLine(line)
		if !ok {
			continue
		}
		top, lineH := e.view.LineYrange(iter)
		// What is on the line now, which is not what wantPad would ask for while the
		// spacing changes are held back.
		pad := padPixels(o.pad)
		// The line's height includes the space added under it, so what is left is
		// the text. A height that does not even cover the space means the layout
		// has not reached this line yet.
		textH := lineH - pad
		if textH < 0 {
			unsettled = true
			continue
		}
		b.resize(e, avail)
		gtk.BaseWidget(w).SetVisible(true)
		// Left edge: where the line's first character sits. That is the text's own
		// left edge whether the view counts its left margin as padding around the
		// text or as part of the layout, so the block lines up with the words.
		x, y := e.view.IterLocation(iter).X(), top+textH
		switch {
		case !o.shown:
			e.view.AddOverlay(w, x, y)
			o.shown, o.x, o.y = true, x, y
		case x != o.x || y != o.y:
			e.view.MoveOverlay(w, x, y)
			o.x, o.y = x, y
		}
	}

	decor := e.placeDecor()
	decor = e.placeChips() || decor
	e.retryPlace(unsettled || decor)
}

// retryPlace tries the placement pass again in a moment when the text layout was
// not ready for it, a few times over.
func (e *Editor) retryPlace(unsettled bool) {
	s := &e.img
	if !unsettled {
		s.retry = 0
		return
	}
	if s.retry < placeRetries {
		s.retry++
		coreglib.TimeoutAdd(100, func() bool {
			e.queuePlace()
			return false
		})
	}
}

// applyPad puts the spacing tag called name on the line of a picture or a table,
// in place of the one it had. An empty name only removes.
func (e *Editor) applyPad(o *overlay, line int, name string) {
	n, ok := e.lineCharLen(line)
	if !ok {
		return
	}
	start, ok1 := e.buffer.IterAtLineOffset(line, 0)
	end, ok2 := e.buffer.IterAtLineOffset(line, n)
	if !ok1 || !ok2 {
		return
	}
	e.markLayoutStale()
	if old := e.tags[o.pad]; old != nil && o.pad != name {
		e.buffer.RemoveTag(old, start, end)
	}
	o.pad = name
	if name == "" || n == 0 {
		return
	}
	tag := e.tags[name]
	if tag == nil {
		e.newTag(name, map[string]any{"pixels-below-lines": padPixels(name)})
		tag = e.tags[name]
	}
	e.buffer.ApplyTag(tag, start, end)
}

// ---- The texture cache -----------------------------------------------------

// lru is a small least-recently-used cache keyed by path.
type lru[V any] struct {
	max   int
	order *list.List // front is the most recently used
	byKey map[string]*list.Element
}

type lruEntry[V any] struct {
	key string
	val V
}

func newLRU[V any](max int) *lru[V] {
	return &lru[V]{max: max, order: list.New(), byKey: map[string]*list.Element{}}
}

// get returns a value and counts it as used.
func (c *lru[V]) get(key string) (V, bool) {
	el, ok := c.byKey[key]
	if !ok {
		var zero V
		return zero, false
	}
	c.order.MoveToFront(el)
	return el.Value.(*lruEntry[V]).val, true
}

// put stores a value as the most recently used, and drops the least recently
// used one when the cache is over its size.
func (c *lru[V]) put(key string, val V) {
	if el, ok := c.byKey[key]; ok {
		el.Value.(*lruEntry[V]).val = val
		c.order.MoveToFront(el)
		return
	}
	c.byKey[key] = c.order.PushFront(&lruEntry[V]{key, val})
	for c.order.Len() > c.max {
		last := c.order.Back()
		delete(c.byKey, last.Value.(*lruEntry[V]).key)
		c.order.Remove(last)
	}
}

func (c *lru[V]) clear() {
	c.order.Init()
	c.byKey = map[string]*list.Element{}
}

// keys lists the cached keys, most recently used first.
func (c *lru[V]) keys() []string {
	out := make([]string, 0, c.order.Len())
	for el := c.order.Front(); el != nil; el = el.Next() {
		out = append(out, el.Value.(*lruEntry[V]).key)
	}
	return out
}

// ---- Adding pictures -------------------------------------------------------

// imageSource is a picture waiting to be saved: bytes already in hand (from the
// clipboard) or a file to read (from a drop or a copied file).
type imageSource struct {
	data []byte
	path string
}

// newlinePadding decides which newlines an inserted image line needs so that it
// sits on a line of its own. One goes before it unless it starts a line, and one
// after it unless a newline already follows.
func newlinePadding(atLineStart, nextIsNewline bool) (before, after bool) {
	return !atLineStart, !nextIsNewline
}

// hasImageMIME reports whether any of the clipboard's formats is a picture.
func hasImageMIME(mimes []string) bool {
	for _, m := range mimes {
		if strings.HasPrefix(m, "image/") {
			return true
		}
	}
	return false
}

// hasTextMIME reports whether any of the clipboard's formats is plain text.
func hasTextMIME(mimes []string) bool {
	for _, m := range mimes {
		if m == "text/plain" || m == "text/plain;charset=utf-8" {
			return true
		}
	}
	return false
}

// isPicture is whether a clipboard holds a picture and nothing the text view
// could paste. Apps that copy a picture together with its caption or its source
// offer text as well, and then the text is what was meant.
func isPicture(hasTexture bool, mimes []string) bool {
	return (hasTexture || hasImageMIME(mimes)) && !hasTextMIME(mimes)
}

// keepImagePaths keeps the entries that name an image file.
func keepImagePaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		if p != "" && markup.IsImagePath(p) {
			out = append(out, p)
		}
	}
	return out
}

// imagePathsIn lists the image files in a file list. Only files on this machine
// have a path.
func imagePathsIn(fl *gdk.FileList) []string {
	var all []string
	for _, f := range fl.Files() {
		all = append(all, f.Path())
	}
	return keepImagePaths(all)
}

// InsertImage puts "![](mdPath)" on a line of its own at the caret, as typing it
// would, or after the caret's line when that line is a task. The app calls it
// for its Insert Image menu item.
func (e *Editor) InsertImage(mdPath string) {
	e.insertImages([]string{mdPath}, nil)
}

// clearOfTask moves iter to the end of its line when the line is a task, so a
// picture never lands between a checkbox and its text: a line that starts with
// the checkbox's anchor is read back as a task, and a picture split off it would
// take half the text with it.
func (e *Editor) clearOfTask(iter *gtk.TextIter) {
	line, ok := e.lineText(iter.Line())
	if !ok || !strings.HasPrefix(line, anchorChar) {
		return
	}
	if !iter.EndsLine() {
		iter.ForwardToLineEnd()
	}
}

// insertImages puts each picture on its own line, at a mark or at the caret when
// there is none. It is one user action, so a single undo takes the lot back, and
// its edits are ordinary buffer inserts, so autosave and the dirty-line tracking
// see typing. Where that point is on a task line the pictures go after the line
// instead (see clearOfTask).
func (e *Editor) insertImages(paths []string, at *gtk.TextMark) {
	if at != nil {
		// The mark is spent however this ends, including when every save failed
		// and there is nothing to insert.
		defer func() {
			if !at.Deleted() {
				e.buffer.DeleteMark(at)
			}
		}()
	}
	if len(paths) == 0 {
		return
	}
	e.buffer.BeginUserAction()
	defer e.buffer.EndUserAction()

	var iter *gtk.TextIter
	if at != nil {
		iter = e.buffer.IterAtMark(at)
	} else {
		e.buffer.DeleteSelection(true, true)
		iter = e.buffer.IterAtMark(e.buffer.GetInsert())
	}
	if iter == nil {
		return
	}
	e.clearOfTask(iter)
	for _, p := range paths {
		nextIsNewline := iter.EndsLine() && !iter.IsEnd()
		before, after := newlinePadding(iter.StartsLine(), nextIsNewline)
		text := "![](" + p + ")"
		if before {
			text = "\n" + text
		}
		if after {
			text += "\n"
		}
		e.buffer.Insert(iter, text)
		// The caret goes to the line after the picture, so that the picture shows
		// on its own instead of beside the text that would reveal.
		if !after && !iter.IsEnd() {
			iter.ForwardLine()
		}
		e.buffer.PlaceCursor(iter)
	}
}

// saveImages saves each source through the app's SaveImage on a goroutine (it
// writes files), then inserts the ones that worked back on the main thread. A
// failure goes to OnImageError and does not stop the others.
func (e *Editor) saveImages(srcs []imageSource, at *gtk.TextMark) {
	save := e.SaveImage
	if save == nil || len(srcs) == 0 {
		return
	}
	gen := e.img.gen
	go func() {
		var paths []string
		var errs []error
		for _, src := range srcs {
			data := src.data
			var err error
			if data == nil {
				data, err = readImageFile(src.path)
			}
			if err == nil {
				var md string
				if md, err = save(data); err == nil {
					paths = append(paths, md)
				}
			}
			if err != nil {
				errs = append(errs, err)
			}
		}
		coreglib.IdleAdd(func() bool {
			for _, err := range errs {
				e.imageError(err)
			}
			// Another note has been opened while this was saving; the text it
			// was meant for is gone, so the pictures are not put into the new one.
			if gen == e.img.gen {
				e.insertImages(paths, at)
			} else if at != nil {
				e.buffer.DeleteMark(at)
			}
			return false
		})
	}()
}

func (e *Editor) imageError(err error) {
	if e.OnImageError != nil && err != nil {
		e.OnImageError(err)
	}
}

// readImageFile reads a picture, refusing one over maxImageBytes without reading
// all of it. The file is looked at before it is opened: opening a FIFO with no
// writer blocks until one turns up, and this runs on a goroutine that a drop or
// a paste is waiting on.
func readImageFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s is not a regular file", filepath.Base(path))
	}
	if info.Size() > maxImageBytes {
		return nil, fmt.Errorf("%s is larger than 50 MiB", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxImageBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImageBytes {
		return nil, fmt.Errorf("%s is larger than 50 MiB", filepath.Base(path))
	}
	return data, nil
}

// onPaste runs before the text view's own paste. A clipboard that holds a
// picture and no text, or picture files, is taken over here; anything else falls
// through to the normal paste.
func (e *Editor) onPaste() {
	if e.img.paste || e.SaveImage == nil {
		return
	}
	clip := e.view.Clipboard()
	fm := clip.Formats()
	if fm == nil {
		return
	}
	switch {
	case isPicture(fm.ContainGType(gdk.GTypeTexture), fm.MIMETypes()):
		e.view.StopEmission("paste-clipboard")
		e.pasteTexture(clip)
	case fm.ContainGType(gdk.GTypeFileList) || fm.ContainMIMEType("text/uri-list"):
		e.view.StopEmission("paste-clipboard")
		e.pasteFiles(clip)
	}
}

// passPaste hands a paste back to the text view, past onPaste.
func (e *Editor) passPaste() {
	e.img.paste = true
	e.view.Emit("paste-clipboard")
	e.img.paste = false
}

// pasteTexture reads the clipboard's picture and saves it as PNG. When it cannot
// be read the paste goes to the text view, as a copied file that is no picture
// does (see pasteFiles).
func (e *Editor) pasteTexture(clip *gdk.Clipboard) {
	clip.ReadTextureAsync(context.Background(), func(res gio.AsyncResulter) {
		tex, err := clip.ReadTextureFinish(res)
		if err != nil || tex == nil {
			e.passPaste()
			return
		}
		png := gdk.BaseTexture(tex).SaveToPNGBytes().Data()
		e.saveImages([]imageSource{{data: png}}, nil)
	})
}

// pasteFiles takes the picture files from a copied file list. When there are none
// (a text file was copied, or a link) the paste is handed back to the text view.
func (e *Editor) pasteFiles(clip *gdk.Clipboard) {
	clip.ReadValueAsync(context.Background(), gdk.GTypeFileList, glib.PRIORITY_DEFAULT, func(res gio.AsyncResulter) {
		var paths []string
		if val, err := clip.ReadValueFinish(res); err == nil && val != nil {
			if fl, ok := val.GoValue().(*gdk.FileList); ok && fl != nil {
				paths = imagePathsIn(fl)
			}
		}
		if len(paths) == 0 {
			e.passPaste()
			return
		}
		e.saveImages(sourcesFor(paths), nil)
	})
}

func sourcesFor(paths []string) []imageSource {
	out := make([]imageSource, len(paths))
	for i, p := range paths {
		out[i] = imageSource{path: p}
	}
	return out
}

// onDrop takes dropped picture files and puts them at the drop point. It says no
// to a drop with no pictures in it, so the drag looks refused.
func (e *Editor) onDrop(value *coreglib.Value, x, y float64) bool {
	if e.SaveImage == nil || value == nil {
		return false
	}
	fl, ok := value.GoValue().(*gdk.FileList)
	if !ok || fl == nil {
		return false
	}
	paths := imagePathsIn(fl)
	if len(paths) == 0 {
		return false
	}
	// The pictures go after the line that was dropped on, and only the line is
	// looked up: finding the character under the pointer is a byte-level hit-test,
	// which aborts the process when the line's hidden runs have changed since it
	// was laid out (see linkUnder). insertImages puts them on a line of their own.
	_, by := e.view.WindowToBufferCoords(gtk.TextWindowWidget, int(x), int(y))
	var iter *gtk.TextIter
	if line, _ := e.view.LineAtY(by); line != nil {
		iter = line
		if !iter.EndsLine() {
			iter.ForwardToLineEnd()
		}
	} else {
		_, iter = e.buffer.Bounds()
	}
	// The drop point is remembered as a mark: the files take a moment to save,
	// and the text may change in the meantime.
	e.saveImages(sourcesFor(paths), e.buffer.CreateMark("", iter, false))
	return true
}
