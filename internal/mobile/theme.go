//go:build android || gio

package mobile

// The theme, and the icon set. Material Design throughout: Gio implements it,
// so the phone interface gets the real thing rather than an approximation of
// it drawn by hand.

import (
	"image/color"

	"gioui.org/widget/material"
	"golang.org/x/exp/shiny/materialdesign/icons"

	"gioui.org/widget"
)

// Material 3 roles, in the app's own purple. Only the roles the interface
// actually uses are named: a palette is easier to keep coherent when every
// entry in it is on screen somewhere.
var (
	colorPrimary      = rgb(0x6D4AA6) // the app's purple, at Material's tone 40
	colorOnPrimary    = rgb(0xFFFFFF)
	colorSurface      = rgb(0xFDFBFF)
	colorOnSurface    = rgb(0x1C1B1F)
	colorSurfaceLow   = rgb(0xF3EFF7) // cards and the search field
	colorOutline      = rgb(0xCFC8D4)
	colorOnSurfaceDim = rgb(0x625B6B) // secondary text
	colorLocked       = rgb(0x7A757F)
	colorStar         = rgb(0xE3A008)
	colorDanger       = rgb(0xB3261E)
)

func rgb(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xFF}
}

// newTheme builds the Material theme the whole interface draws through.
func newTheme() *material.Theme {
	th := material.NewTheme()
	th.Palette = material.Palette{
		Bg:         colorSurface,
		Fg:         colorOnSurface,
		ContrastBg: colorPrimary,
		ContrastFg: colorOnPrimary,
	}
	return th
}

// The icon set is Material Symbols, the same family the desktop app installs,
// so the two do not disagree about what a note or a padlock looks like.
var (
	iconNote      = mustIcon(icons.ActionDescription)
	iconChecklist = mustIcon(icons.ActionAssignmentTurnedIn)
	iconFolder    = mustIcon(icons.FileFolder)
	iconLock      = mustIcon(icons.ActionLock)
	iconStar      = mustIcon(icons.ToggleStar)
	iconSearch    = mustIcon(icons.ActionSearch)
	iconBack      = mustIcon(icons.NavigationArrowBack)
	iconAdd       = mustIcon(icons.ContentAdd)
	iconClear     = mustIcon(icons.ContentClear)
	iconDelete    = mustIcon(icons.ActionDelete)
)

// mustIcon decodes one of the built-in Material Symbols. They are compiled
// into the binary as vector data, so a failure here is a programming mistake
// rather than anything a device can cause.
func mustIcon(data []byte) *widget.Icon {
	ic, err := widget.NewIcon(data)
	if err != nil {
		panic("mobile: bad built-in icon: " + err.Error())
	}
	return ic
}
