//go:build android || gio

package mobile

import (
	"context"
	"log"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget/material"

	"atlas-notes/internal/update"
)

// Checking for a new version, on the phone.
//
// The check itself is the desktop app's, unchanged: one anonymous read of a
// text file from the project's repository, nothing about the device or its
// notes sent, and silence when there is nothing new or the network is not
// there. What differs is what happens when there is something: a desktop
// Linux build rebuilds itself, and a phone cannot. Android installs packages
// through the system installer and will not let an application hand itself a
// new version without going through it, so this says what is available and
// where it is, and the install is the person's to start.

// updateCheckDelay lets the interface settle before the check runs. A launch
// is never waiting on the network.
const updateCheckDelay = 2 * time.Second

// releasePage is where a phone build is downloaded from.
const releasePage = "github.com/EternalCoder454/atlas-notes/releases/latest"

// updateState is what the check found, if anything.
type updateState struct {
	rel     *update.Release
	checked bool
}

// startUpdateCheck runs the check on a background goroutine and wakes the
// interface when it has an answer.
func (a *App) startUpdateCheck(version string) {
	go func() {
		time.Sleep(updateCheckDelay)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		rel, err := update.New().Check(ctx, "main", version)
		if err != nil {
			// Offline behaves exactly like up to date: silently.
			log.Printf("atlas-notes: update check: %v", err)
			return
		}
		a.mu.Lock()
		a.update = updateState{rel: rel, checked: true}
		a.mu.Unlock()
		if rel != nil && a.win != nil {
			a.win.Invalidate()
		}
	}()
}

// layoutUpdateBanner is the strip above the note list, drawn only when there
// is a newer version. It is dismissible, because a note-taking app should not
// keep interrupting someone who is writing.
func (a *App) layoutUpdateBanner(gtx layout.Context) layout.Dimensions {
	a.mu.Lock()
	rel := a.update.rel
	a.mu.Unlock()
	if rel == nil || a.updateDismissed {
		return layout.Dimensions{}
	}
	if a.updateClose.Clicked(gtx) {
		a.updateDismissed = true
		return layout.Dimensions{}
	}

	return layout.Inset{
		Left: unit.Dp(12), Right: unit.Dp(12), Bottom: unit.Dp(8),
	}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return roundedBox(gtx, colorSurfaceLow, unit.Dp(12), func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(unit.Dp(14)).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
							layout.Flexed(1, material.Subtitle2(a.theme,
								"Version "+rel.Version+" is available").Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								btn := material.IconButton(a.theme, &a.updateClose, iconClear, "Dismiss")
								btn.Background = colorSurfaceLow
								btn.Color = colorOnSurfaceDim
								btn.Size = unit.Dp(16)
								btn.Inset = layout.UniformInset(unit.Dp(6))
								return btn.Layout(gtx)
							}),
						)
					}),
					layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						notes := rel.Notes
						if len(notes) > 3 {
							notes = notes[:3] // a banner, not the changelog
						}
						children := make([]layout.FlexChild, 0, len(notes)+2)
						for _, n := range notes {
							children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								lbl := material.Body2(a.theme, "• "+n)
								lbl.Color = colorOnSurfaceDim
								return layout.Inset{Bottom: unit.Dp(2)}.Layout(gtx, lbl.Layout)
							}))
						}
						children = append(children,
							layout.Rigid(layout.Spacer{Height: unit.Dp(6)}.Layout),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								// The address rather than a button: Android
								// installs packages through the system
								// installer, and an app cannot hand itself a
								// new version without going through it.
								lbl := material.Caption(a.theme, "Download it from "+releasePage)
								lbl.Color = colorOnSurfaceDim
								return lbl.Layout(gtx)
							}),
						)
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
					}),
				)
			})
		})
	})
}
