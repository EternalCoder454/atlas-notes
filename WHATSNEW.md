# What's new in Atlas Notes

This file is what the app shows you when an update is available. One version
per heading, a few plain lines each. The detailed, technical history lives in
CHANGELOG.md.

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
