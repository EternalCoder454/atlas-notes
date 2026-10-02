# What's new in Atlas Notes

This file is what the app shows you when an update is available. One version
per heading, a few plain lines each. The detailed, technical history lives in
CHANGELOG.md.

## 0.11.1
- A new icon: a page on the purple Atlas tile, matching Atlas Updater

## 0.11.0
- Claude Code can read, search, create and edit your notes, once you turn it on in Settings
- You choose what it may do, and password-protected notes stay out of its reach
- Every change it makes is kept in Version History, and the lines it changed light up
- A diagnostic log you can turn on in Settings, About, to help track down bugs
- The intro at startup appears faster

## 0.10.1
- Notes with tables and diagrams open about 40% faster
- Clicking folders open and shut quickly no longer crashes the app

## 0.10.0
- Settings is now a page of the window, with each setting on a card of its own
- Folders slide open and closed, side panels slide, and pages ease in
- The assistant writes what you ask for: Better title gives three titles
- Summarise, Open tasks and Explain simply ask the assistant more precisely

## 0.9.0
- A new icon: the Atlas mark, shared with Atlas Monitor
- The app opens with a short intro of its logo; a click skips it, and Settings can turn it off
- Select several notes and folders and delete them together
- The outline sits beside the page, in the margin
- A click on a table puts the cursor in the cell you clicked
- A due date no longer shows over another note, and new folders open once notes are in them

## 0.8.2
- Draw flowcharts, Gantt charts and sequence diagrams right in your notes
- Build a flowchart by dragging boxes and connecting them, with no typing needed
- Install, update or remove Atlas Notes on any Linux distro with one command
- On older distros such as Ubuntu 24.04 and Linux Mint 22 it installs as a Flatpak
- Typing is about seven times faster, and a large vault indexes faster
- The Update button knows whether a package manager or Flatpak installed the app

## 0.8.1
- On your phone, opening a note no longer shows an error at the bottom of the screen

## 0.8.0
- Notes read as they will print, and Obsidian's callouts, highlights and embeds look as they should
- Link notes with [[Name]], tag them with #tag, and paste or drop pictures straight in
- Ctrl+P finds any note, command or setting, and typing / in a note offers blocks and the assistant
- A Tasks page, daily notes, templates, and reminders for what is due
- Version history shows what changed, and notes can be split, merged or opened side by side
- A properties card for front matter, and an outline beside the page
- A new look after Windows 11, with ten colour themes and a simpler Settings page
- Notes are plain Markdown files that Obsidian and any other app can open

## 0.7.1
- Built with Go 1.27.1 and its latest security fixes
- A newer version of the library that connects Atlas Notes to GTK

## 0.7.0
- Search finds notes by what they say, not just by their names
- Export any note as a Word document, OpenDocument, Markdown, a web page or plain text
- Deleted notes and folders go to the Trash, so you can get them back
- On your phone, keep your notes in a folder a sync app like Syncthing shares with your computer
- The assistant tucks itself away again when the window is narrow
- Opening a large vault for the first time no longer freezes the window

## 0.6.0
- Atlas Notes runs on Windows now. Unpack the .zip from the releases page and run atlas-notes.exe
- There is an Android app. Install the .apk and your notes come with you, locked ones included
- The phone app tells you when a new version is out, and installs it for you
- The settings gear is back in the top right of the window
- Atlas Notes can check for updates on its own. Turn it off in Settings, under App
- Opening a large vault for the first time is about a third faster, and searching one is twice as quick

## 0.5.10
- Really fixes Atlas Notes closing itself when you select or copy text. 0.5.9 did not

## 0.5.9
- Fixes Atlas Notes closing itself while you were typing

## 0.5.8
- Text is much easier to read in light mode
- Notes and checklists now have their own icons in the vault, so you can tell them apart
- The bar along the bottom shows less, and only what's useful
- The note you have open is easier to pick out in the vault list
- You can hide the formatting toolbar in Settings, under Editor
- The assistant panel tucks itself away when the window is narrow, and comes back when it isn't

## 0.5.7
- You can now password-protect a note, or a whole folder. Right-click it in the vault
- Protected notes are properly encrypted, so nothing else on your computer can read them
- If you forget the password those notes can't be opened again, by anything, and there's no way round it
- Star a note or folder to mark it a favourite
- Lock your protected notes again without quitting, with Ctrl+Shift+L
- Change your password in Settings, under App

## 0.5.6
- Every icon comes from one set now, so nothing looks out of place
- Recent notes on the home screen show a preview of what's in them
- The assistant's suggested questions fit side by side instead of stacking up
- Word count, task progress and whether a note is saved all sit in the bottom bar
- Settings uses plainer wording, and tucks away the file paths you rarely need
- The message about installing Ollama is much shorter

## 0.5.5
- Atlas Notes now tells you when a new version is out, and can install it for you
- You can turn that check off in Settings, under App
- The toolbar icons match each other, and checkboxes line up neatly with their text
- Big vaults open much faster, and searching them is instant
- Typing in a long note no longer lags
- Atlas Notes uses far less memory, even after a long session of reading notes
- Fixed a crash when a note contained certain punctuation
- Your notes can no longer end up outside your vault folder by accident

## 0.5.0
- A proper home screen: start a note, start a checklist, find a note, or pick up where you left off
- Search your whole vault by name with Ctrl+K
- A formatting toolbar above every note, and a keyboard shortcut for everything (Ctrl+? shows the list)
- Tasks show their due date, and finished ones are struck through
- The assistant now uses the qwen3.5:9b model. Run "ollama pull qwen3.5:9b" once to get it
- Text is sharper on 1080p screens
