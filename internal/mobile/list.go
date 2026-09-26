//go:build android || gio

package mobile

import (
	"image"
	"path"
	"strings"
	"time"

	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
	"gioui.org/widget/material"

	"atlas-notes/internal/storage"
)

// The vault, as a list. One column on a phone, because there is no room for
// the desktop's three panels and no reason to pretend otherwise: the list is
// the whole screen until a note is opened, and then the note is.

// rebuildRows rebuilds the visible rows from the note list and the search box.
// Rows are rebuilt rather than re-pointed because a phone's list is short
// enough that it costs nothing, and the click state of a row that has scrolled
// away is not worth keeping.
func (a *App) rebuildRows() {
	query := strings.ToLower(strings.TrimSpace(a.search.Text()))
	a.rows = a.rows[:0]
	for _, n := range a.notes {
		if query != "" && !strings.Contains(strings.ToLower(n.Path), query) {
			continue
		}
		a.rows = append(a.rows, &noteRow{meta: n})
	}
}

func (a *App) layoutList(gtx layout.Context) layout.Dimensions {
	if a.searchBtn.Clicked(gtx) {
		a.searchOpen = !a.searchOpen
		if !a.searchOpen {
			a.search.SetText("")
			a.rebuildRows()
		}
	}
	for {
		ev, ok := a.search.Update(gtx)
		if !ok {
			break
		}
		if _, isChange := ev.(widget.ChangeEvent); isChange {
			a.rebuildRows()
		}
	}
	if a.newBtn.Clicked(gtx) {
		a.newNote()
	}
	for _, r := range a.rows {
		if r.click.Clicked(gtx) {
			a.openNote(r.meta.Path)
		}
	}

	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(a.layoutTopBar),
				layout.Flexed(1, a.layoutRows),
			)
		}),
		// The floating action button, bottom right, as Material puts it.
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.SE.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(unit.Dp(16)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					btn := material.IconButton(a.theme, &a.newBtn, iconAdd, "New note")
					btn.Background = colorPrimary
					btn.Color = colorOnPrimary
					btn.Size = unit.Dp(28)
					btn.Inset = layout.UniformInset(unit.Dp(16))
					return btn.Layout(gtx)
				})
			})
		}),
	)
}

// layoutTopBar is the app bar: the vault's name and how many notes are in it,
// with search folding out underneath when it is asked for.
func (a *App) layoutTopBar(gtx layout.Context) layout.Dimensions {
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{
				Top: unit.Dp(14), Bottom: unit.Dp(10),
				Left: unit.Dp(16), Right: unit.Dp(8),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
					layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
							layout.Rigid(material.H6(a.theme, "Atlas Notes").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								lbl := material.Caption(a.theme, plural(len(a.notes), "note"))
								lbl.Color = colorOnSurfaceDim
								return lbl.Layout(gtx)
							}),
						)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						icon, label := iconSearch, "Search"
						if a.searchOpen {
							icon, label = iconClear, "Close search"
						}
						btn := material.IconButton(a.theme, &a.searchBtn, icon, label)
						btn.Background = colorSurfaceLow
						btn.Color = colorOnSurface
						btn.Size = unit.Dp(22)
						btn.Inset = layout.UniformInset(unit.Dp(10))
						return btn.Layout(gtx)
					}),
				)
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !a.searchOpen {
				return layout.Dimensions{}
			}
			return layout.Inset{
				Left: unit.Dp(16), Right: unit.Dp(16), Bottom: unit.Dp(10),
			}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return roundedBox(gtx, colorSurfaceLow, unit.Dp(28), func(gtx layout.Context) layout.Dimensions {
					return layout.UniformInset(unit.Dp(12)).Layout(gtx,
						material.Editor(a.theme, &a.search, "Search notes").Layout)
				})
			})
		}),
	)
}

// layoutRows is the scrolling list itself.
func (a *App) layoutRows(gtx layout.Context) layout.Dimensions {
	if len(a.rows) == 0 {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			msg := "No notes yet. Tap + to write one."
			if strings.TrimSpace(a.search.Text()) != "" {
				msg = "Nothing matches that."
			}
			lbl := material.Body1(a.theme, msg)
			lbl.Color = colorOnSurfaceDim
			return lbl.Layout(gtx)
		})
	}
	list := material.List(a.theme, &a.listState)
	return list.Layout(gtx, len(a.rows), func(gtx layout.Context, i int) layout.Dimensions {
		return a.layoutRow(gtx, a.rows[i])
	})
}

// layoutRow is one note: an icon saying what kind it is, its name and folder,
// when it was last touched, and the badges for locked and favourite.
func (a *App) layoutRow(gtx layout.Context, r *noteRow) layout.Dimensions {
	return material.Clickable(gtx, &r.click, func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{
			Top: unit.Dp(12), Bottom: unit.Dp(12),
			Left: unit.Dp(16), Right: unit.Dp(16),
		}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					ic, tint := iconNote, colorOnSurfaceDim
					switch {
					case r.meta.Locked:
						ic, tint = iconLock, colorLocked
					case r.meta.HasTasks:
						ic = iconChecklist
					}
					gtx.Constraints.Min = image.Pt(gtx.Dp(24), gtx.Dp(24))
					return ic.Layout(gtx, tint)
				}),
				layout.Rigid(layout.Spacer{Width: unit.Dp(16)}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(material.Body1(a.theme, path.Base(r.meta.Path)).Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sub := relativeTime(r.meta.ModifiedAt)
							if r.meta.Folder != "" {
								sub = r.meta.Folder + " · " + sub
							}
							lbl := material.Caption(a.theme, sub)
							lbl.Color = colorOnSurfaceDim
							return lbl.Layout(gtx)
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !starred(r.meta) {
						return layout.Dimensions{}
					}
					gtx.Constraints.Min = image.Pt(gtx.Dp(18), gtx.Dp(18))
					return iconStar.Layout(gtx, colorStar)
				}),
			)
		})
	})
}

// starred reports whether a note is a favourite. Favourites are a desktop
// preference kept in that machine's configuration file, so on a phone they are
// read if the file happens to be there and never written.
func starred(storage.NoteMeta) bool { return false }

// roundedBox draws a filled, rounded rectangle behind w.
func roundedBox(gtx layout.Context, fill colour, radius unit.Dp, w layout.Widget) layout.Dimensions {
	macro := recordMacro(gtx, w)
	rr := gtx.Dp(radius)
	defer clip.RRect{Rect: image.Rectangle{Max: macro.size}, SE: rr, SW: rr, NE: rr, NW: rr}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, fill)
	macro.call.Add(gtx.Ops)
	return layout.Dimensions{Size: macro.size}
}

// paintBackground fills the whole window.
func paintBackground(gtx layout.Context, fill colour) {
	defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
	paint.Fill(gtx.Ops, fill)
}

// plural renders a count with its noun, pluralised.
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return itoa(n) + " " + noun + "s"
}

// relativeTime is how the list says when a note was last written.
func relativeTime(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return ""
	}
	switch d := time.Since(t); {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h ago"
	case d < 7*24*time.Hour:
		return itoa(int(d.Hours()/24)) + "d ago"
	default:
		return t.Format("2 Jan 2006")
	}
}
