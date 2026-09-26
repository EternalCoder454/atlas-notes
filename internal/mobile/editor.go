//go:build android || gio

package mobile

import (
	"image"
	"strings"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"atlas-notes/internal/checklist"
)

// The note, open. A phone has room for one thing at a time, so the editor is
// the whole screen: a bar with the way back and the note's name, the tasks it
// contains as real checkboxes, and the text underneath.
//
// The desktop app renders Markdown in place and hides the markers. That is not
// repeated here. It depends on a text widget that can hide characters and
// embed others, which this toolkit does not have, and the half-built version
// of it would be worse than plain text that is honest about being plain.

// rebuildTasks reads the checklist items out of the open note. They are
// rebuilt on every load and after every toggle, because the text is the truth
// and this is a view of it.
func (a *App) rebuildTasks() {
	a.tasks = a.tasks[:0]
	for i, line := range strings.Split(a.body.Text(), "\n") {
		it, ok := checklist.ParseLine(line)
		if !ok {
			continue
		}
		row := taskRow{line: i, text: it.Text}
		row.check.Value = it.Checked
		a.tasks = append(a.tasks, row)
	}
}

// toggleTask flips one item and writes it back into the note's text.
func (a *App) toggleTask(idx int, checked bool) {
	if idx < 0 || idx >= len(a.tasks) {
		return
	}
	lines := strings.Split(a.body.Text(), "\n")
	ln := a.tasks[idx].line
	if ln < 0 || ln >= len(lines) {
		return
	}
	it, ok := checklist.ParseLine(lines[ln])
	if !ok {
		return
	}
	it.Checked = checked
	lines[ln] = it.Marshal()

	// Setting the text moves the caret, so the selection is put back where the
	// person left it rather than jumping to the end of the note.
	start, end := a.body.Selection()
	a.body.SetText(strings.Join(lines, "\n"))
	a.body.SetCaret(start, end)
	a.rebuildTasks()
	a.markDirty()
}

func (a *App) layoutEditor(gtx layout.Context) layout.Dimensions {
	if a.backBtn.Clicked(gtx) {
		a.closeNote()
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	if a.deleteBtn.Clicked(gtx) {
		a.deleteCurrent()
		return layout.Dimensions{Size: gtx.Constraints.Max}
	}
	for i := range a.tasks {
		if a.tasks[i].check.Update(gtx) {
			a.toggleTask(i, a.tasks[i].check.Value)
			break // the rows were just rebuilt; the rest are stale
		}
	}
	for {
		ev, ok := a.body.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			a.rebuildTasks()
			a.markDirty()
		}
	}

	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(a.layoutEditorBar),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			list := material.List(a.theme, &a.bodyState)
			return list.Layout(gtx, 2, func(gtx layout.Context, i int) layout.Dimensions {
				if i == 0 {
					return a.layoutTasks(gtx)
				}
				return layout.Inset{
					Left: unit.Dp(16), Right: unit.Dp(16),
					Top: unit.Dp(4), Bottom: unit.Dp(32),
				}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					ed := material.Editor(a.theme, &a.body, "Start writing…")
					ed.Color = colorOnSurface
					return ed.Layout(gtx)
				})
			})
		}),
	)
}

// layoutEditorBar is the bar across the top of an open note.
func (a *App) layoutEditorBar(gtx layout.Context) layout.Dimensions {
	return layout.Inset{
		Top: unit.Dp(8), Bottom: unit.Dp(8),
		Left: unit.Dp(8), Right: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.IconButton(a.theme, &a.backBtn, iconBack, "Back to the vault")
				btn.Background = colorSurface
				btn.Color = colorOnSurface
				btn.Size = unit.Dp(22)
				btn.Inset = layout.UniformInset(unit.Dp(10))
				return btn.Layout(gtx)
			}),
			layout.Rigid(layout.Spacer{Width: unit.Dp(4)}.Layout),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !a.editorLock {
							return layout.Dimensions{}
						}
						gtx.Constraints.Min = image.Pt(gtx.Dp(16), gtx.Dp(16))
						return iconLock.Layout(gtx, colorLocked)
					}),
					layout.Rigid(layout.Spacer{Width: unit.Dp(6)}.Layout),
					layout.Flexed(1, material.Subtitle1(a.theme, noteName(a.current)).Layout),
				)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				btn := material.IconButton(a.theme, &a.deleteBtn, iconDelete, "Delete this note")
				btn.Background = colorSurface
				btn.Color = colorDanger
				btn.Size = unit.Dp(20)
				btn.Inset = layout.UniformInset(unit.Dp(10))
				return btn.Layout(gtx)
			}),
		)
	})
}

// layoutTasks draws the note's checklist items above its text, as real
// checkboxes with a thumb-sized target.
func (a *App) layoutTasks(gtx layout.Context) layout.Dimensions {
	if len(a.tasks) == 0 {
		return layout.Dimensions{}
	}
	return layout.Inset{
		Left: unit.Dp(12), Right: unit.Dp(16),
		Top: unit.Dp(4), Bottom: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		children := make([]layout.FlexChild, 0, len(a.tasks)+1)
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			done := 0
			for _, t := range a.tasks {
				if t.check.Value {
					done++
				}
			}
			lbl := material.Caption(a.theme, itoa(done)+" of "+itoa(len(a.tasks))+" done")
			lbl.Color = colorOnSurfaceDim
			return layout.Inset{Left: unit.Dp(4), Bottom: unit.Dp(4)}.Layout(gtx, lbl.Layout)
		}))
		for i := range a.tasks {
			t := &a.tasks[i]
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				cb := material.CheckBox(a.theme, &t.check, t.text)
				cb.Color = colorOnSurface
				cb.IconColor = colorPrimary
				cb.Size = unit.Dp(24)
				return layout.Inset{Top: unit.Dp(6), Bottom: unit.Dp(6)}.Layout(gtx, cb.Layout)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}
