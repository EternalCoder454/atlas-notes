# Atlas Notes as a Flatpak

For distributions too old to build Atlas Notes natively (Debian 13, Ubuntu 24.04,
Linux Mint 22). The bundle runs on the GNOME 50 runtime, which carries the GTK,
libadwaita and GLib versions the app needs. There is no hosted Flatpak
repository: you install a `.flatpak` file from the GitHub release.

## Install

Flatpak itself must be installed (`sudo apt install flatpak` on Debian, Ubuntu
and Mint). The bundle needs the GNOME runtime, which comes from Flathub:

```sh
flatpak remote-add --user --if-not-exists flathub https://dl.flathub.org/repo/flathub.flatpakrepo
flatpak install --user ./atlas-notes-X.flatpak
```

Flatpak fetches the runtime (about 400 MB, once) from the Flathub remote. Start
it from the application menu, or with `flatpak run io.github.atlasnotes`.

## Update

There is no repository to pull from. Download the newer `atlas-notes-X.flatpak`
from <https://github.com/EternalCoder454/atlas-notes/releases/latest> and
install it over the old one:

```sh
flatpak install --user ./atlas-notes-X.flatpak
```

Your notes and settings are kept. The Update button in the app shows this
instruction, because a Flatpak cannot rebuild itself.

## Remove

```sh
flatpak uninstall --user io.github.atlasnotes
```

This keeps your notes. To delete them as well, which cannot be undone:

```sh
flatpak uninstall --user --delete-data io.github.atlasnotes
```

## Where things are

Inside the sandbox the app's data folder is
`~/.var/app/io.github.atlasnotes/data/atlas-notes`, and the vault (your notes)
is the `vault` folder in it. Settings are in
`~/.var/app/io.github.atlasnotes/config/atlas-notes/config.json`.

## Permissions

| Permission | Why |
| --- | --- |
| Wayland, X11 fallback, IPC | The window. |
| GPU (dri) | GTK rendering. |
| Network | The local Ollama server at 127.0.0.1:11434, and the check for new versions. |

No filesystem access is granted. Choosing a picture to embed and choosing where
to export a note go through the file chooser portal, which gives the app only
the file you pick. Reminders use the notification portal.

To keep your vault in another folder, for example one a sync client manages,
allow that one folder and point `vault_path` at it in `config.json`:

```sh
flatpak override --user --filesystem=$HOME/Notes io.github.atlasnotes
```

## Building the bundle

From the repository root:

```sh
flatpak-builder --user --install-deps-from=flathub --force-clean \
  --repo=repo build-dir packaging/flatpak/io.github.atlasnotes.yml
flatpak build-bundle repo atlas-notes-X.flatpak io.github.atlasnotes
```

The build downloads Go modules, so it needs network access. That is fine for our
own release bundle; a Flathub submission would have to list the modules as
sources instead.
