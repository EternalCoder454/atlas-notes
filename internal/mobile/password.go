//go:build android || gio

package mobile

import (
	"image"
	"image/color"
	"path"
	"strconv"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
	"gioui.org/widget/material"
)

// The password prompt, and the few helpers the rest of the interface shares.

func (a *App) layoutPassword(gtx layout.Context) layout.Dimensions {
	if a.unlockBtn.Clicked(gtx) {
		a.submitPassword()
	}
	if a.cancelBtn.Clicked(gtx) {
		a.afterUnlock = nil
		a.screen = screenList
		a.err = ""
	}

	setting := !a.store.HasPassword()
	heading, body, action := "Enter your password", "", "Unlock"
	if setting {
		heading = "Set a password"
		body = "This protects the notes you choose to lock. It is not stored " +
			"anywhere and cannot be recovered: if you forget it, those notes " +
			"cannot be opened again, by this app or any other."
		action = "Set password"
	} else {
		body = "This unlocks your protected notes until you close Atlas Notes."
	}

	return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return layout.UniformInset(unit.Dp(24)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min = image.Pt(gtx.Dp(40), gtx.Dp(40))
					return iconLock.Layout(gtx, colorPrimary)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(16)}.Layout),
				layout.Rigid(material.H6(a.theme, heading).Layout),
				layout.Rigid(layout.Spacer{Height: unit.Dp(8)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					lbl := material.Body2(a.theme, body)
					lbl.Color = colorOnSurfaceDim
					return lbl.Layout(gtx)
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return roundedBox(gtx, colorSurfaceLow, unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
						return layout.UniformInset(unit.Dp(14)).Layout(gtx,
							material.Editor(a.theme, &a.password, "Password").Layout)
					})
				}),
				layout.Rigid(layout.Spacer{Height: unit.Dp(20)}.Layout),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							btn := material.Button(a.theme, &a.cancelBtn, "Cancel")
							btn.Background = colorSurfaceLow
							btn.Color = colorOnSurface
							return btn.Layout(gtx)
						}),
						layout.Rigid(layout.Spacer{Width: unit.Dp(12)}.Layout),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							btn := material.Button(a.theme, &a.unlockBtn, action)
							btn.Background = colorPrimary
							btn.Color = colorOnPrimary
							return btn.Layout(gtx)
						}),
					)
				}),
			)
		})
	})
}

// recorded is a widget that has been drawn into a macro, with the size it
// turned out to be, so something can be painted behind it.
type recorded struct {
	call op.CallOp
	size image.Point
}

// recordMacro draws w without emitting it, so its size is known first.
func recordMacro(gtx layout.Context, w layout.Widget) recorded {
	macro := op.Record(gtx.Ops)
	dims := w(gtx)
	return recorded{call: macro.Stop(), size: dims.Size}
}

// noteName is what the bar at the top of an open note shows.
func noteName(rel string) string {
	if rel == "" {
		return "Untitled"
	}
	return path.Base(rel)
}

func itoa(n int) string { return strconv.Itoa(n) }

// colour is the colour type the rest of this package passes around, named so
// the helpers below read as English rather than as image/color.
type colour = color.NRGBA
