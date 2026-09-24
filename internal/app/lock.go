package app

import (
	"log"
	"slices"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"

	"atlas-notes/internal/storage"
)

// Password-protected notes and folders, from the interface's side.
//
// The storage layer does the encryption and knows nothing about dialogs; this
// file is the dialogs, and the one place that decides when to ask. Two rules
// shape all of it:
//
//   - A password is asked for, used, and forgotten. It is never stored, never
//     logged, and never put in a message.
//   - What cannot be undone is said before it is done, in the dialog that does
//     it, not in a tooltip somebody might not read.

// askPassword presents a password prompt and calls onOK with what was typed.
// confirm adds a second field that has to match, for setting a password rather
// than giving one.
func (a *App) askPassword(title, body, action string, confirm bool, onOK func(password string)) {
	dialog := adw.NewAlertDialog(title, body)

	box := gtk.NewBox(gtk.OrientationVertical, 8)
	first := gtk.NewPasswordEntry()
	first.SetShowPeekIcon(true)
	first.SetHExpand(true)
	box.Append(first)

	var second *gtk.PasswordEntry
	if confirm {
		second = gtk.NewPasswordEntry()
		second.SetShowPeekIcon(true)
		second.SetHExpand(true)
		box.Append(second)
	}

	dialog.SetExtraChild(box)
	dialog.AddResponse("cancel", "Cancel")
	dialog.AddResponse("ok", action)
	dialog.SetResponseAppearance("ok", adw.ResponseSuggested)
	dialog.SetDefaultResponse("ok")
	dialog.SetCloseResponse("cancel")

	dialog.ConnectResponse(func(response string) {
		if response != "ok" {
			return
		}
		password := first.Text()
		switch {
		case password == "":
			a.toast("Enter a password.")
			return
		case confirm && password != second.Text():
			a.toast("Those two passwords don't match.")
			return
		}
		onOK(password)
	})
	dialog.Present(a.win)
	first.GrabFocus()
}

// ensureUnlocked runs next once the vault is unlocked, asking for the password
// first if it has to. A vault with no password set asks for a new one, with
// what that means spelled out.
func (a *App) ensureUnlocked(next func()) {
	if a.store == nil {
		return
	}
	if a.store.IsUnlocked() {
		next()
		return
	}
	if !a.store.HasPassword() {
		a.askPassword(
			"Set a Password",
			"This password protects the notes you choose to lock. It is not stored "+
				"anywhere and cannot be recovered — if you forget it, those notes "+
				"cannot be opened again, by this app or any other.",
			"Set Password", true,
			func(password string) {
				if err := a.store.SetPassword(password); err != nil {
					a.toast("Couldn't set the password: " + err.Error())
					return
				}
				next()
			})
		return
	}
	a.askPassword(
		"Enter Your Password",
		"This unlocks your protected notes until you close Atlas Notes.",
		"Unlock", false,
		func(password string) {
			if err := a.store.Unlock(password); err != nil {
				a.toast("That password didn't work.")
				return
			}
			next()
		})
}

// lockItem protects a note or folder, asking for the password first. Locking a
// folder locks what is in it and what is put in it later, which the dialog says
// before it happens.
func (a *App) lockItem(rel string, isFolder bool) {
	a.ensureUnlocked(func() {
		if !isFolder {
			a.flushDirty() // the note's latest text has to be what gets sealed
			a.applyLock(rel, isFolder, true)
			return
		}
		confirm := adw.NewAlertDialog(
			"Protect “"+rel+"”?",
			"Every note in this folder will be encrypted, and notes added to it "+
				"later will be too. You'll need your password to read them.")
		confirm.AddResponse("cancel", "Cancel")
		confirm.AddResponse("lock", "Protect Folder")
		confirm.SetResponseAppearance("lock", adw.ResponseSuggested)
		confirm.SetDefaultResponse("lock")
		confirm.SetCloseResponse("cancel")
		confirm.ConnectResponse(func(response string) {
			if response == "lock" {
				a.flushDirty()
				a.applyLock(rel, isFolder, true)
			}
		})
		confirm.Present(a.win)
	})
}

// unlockItem removes the protection, which needs the password too: otherwise
// anyone at an unlocked machine could strip it off.
func (a *App) unlockItem(rel string, isFolder bool) {
	a.ensureUnlocked(func() {
		a.flushDirty()
		a.applyLock(rel, isFolder, false)
	})
}

// applyLock performs the encryption or decryption and refreshes what shows it.
func (a *App) applyLock(rel string, isFolder, lock bool) {
	var err error
	switch {
	case isFolder && lock:
		err = a.store.LockFolder(rel)
	case isFolder:
		err = a.store.UnlockFolder(rel)
	case lock:
		err = a.store.LockNote(rel)
	default:
		err = a.store.UnlockNote(rel)
	}
	if err != nil {
		log.Printf("atlas-notes: lock %q: %v", rel, err)
		a.toast("Couldn't change the password protection: " + err.Error())
		return
	}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.refreshHeader()
	a.refreshWelcome()
	if lock {
		a.toast("Protected. Your password is needed to read this.")
	} else {
		a.toast("Password protection removed.")
	}
}

// isStarred reports whether a note or folder is a favourite.
func (a *App) isStarred(rel string, isFolder bool) bool {
	return slices.Contains(a.favouriteList(isFolder), rel)
}

// favouriteList returns the configured favourites of one kind.
func (a *App) favouriteList(isFolder bool) []string {
	if isFolder {
		return a.cfg.FavouriteFolders
	}
	return a.cfg.FavouriteNotes
}

// toggleStar adds or removes a favourite and saves it.
func (a *App) toggleStar(rel string, isFolder bool) {
	list := a.favouriteList(isFolder)
	if i := slices.Index(list, rel); i >= 0 {
		list = slices.Delete(list, i, i+1)
	} else {
		list = append(list, rel)
	}
	if isFolder {
		a.cfg.FavouriteFolders = list
	} else {
		a.cfg.FavouriteNotes = list
	}
	if err := storage.SaveConfig(a.cfg); err != nil {
		log.Printf("atlas-notes: save favourites: %v", err)
	}
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
}

// forgetFavourite drops a favourite that no longer exists, so a deleted note
// does not keep a row in the configuration for the rest of time.
func (a *App) forgetFavourite(rel string, isFolder bool) {
	if !a.isStarred(rel, isFolder) {
		return
	}
	a.toggleStar(rel, isFolder)
}

// promptChangePassword re-seals every protected note under a new password. The
// current one is asked for first, so someone at an unlocked machine cannot
// change it out from under the owner.
func (a *App) promptChangePassword() {
	if a.store == nil || !a.store.HasPassword() {
		return
	}
	a.askPassword("Change Password", "Enter your current password.", "Continue", false,
		func(current string) {
			a.askPassword("New Password",
				"Every protected note will be re-encrypted with this. It cannot be "+
					"recovered if you forget it.",
				"Change Password", true,
				func(next string) {
					if err := a.store.ChangePassword(current, next); err != nil {
						a.toast("Couldn't change the password: " + err.Error())
						return
					}
					a.toast("Password changed.")
				})
		})
}

// lockNow forgets the password for this session, so protected notes need it
// again. Without this the only way to re-lock would be to quit, which is not
// something to ask of someone stepping away from their desk.
func (a *App) lockNow() {
	if a.store == nil || !a.store.HasPassword() {
		a.toast("Nothing is password-protected yet.")
		return
	}
	if !a.store.IsUnlocked() {
		a.toast("Your protected notes are already locked.")
		return
	}
	a.flushDirty() // a protected note open right now still needs saving
	if a.store.IsNoteLocked(a.currentNote) {
		a.showWelcome() // don't leave its text on screen
	}
	a.store.Lock()
	if a.tree != nil {
		a.tree.ForceRefresh()
	}
	a.refreshWelcome()
	a.toast("Locked. Your password is needed to read protected notes again.")
}
