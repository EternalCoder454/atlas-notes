BIN     := bin/atlas-notes
# The version lives in internal/app/version.go, so a plain `go build .` and a
# `make install` can never report different numbers at each other.
VERSION ?= $(shell sed -n 's/^var version = "\(.*\)"/\1/p' internal/app/version.go)
PREFIX  := $(HOME)/.local

# Injected into the binary: the source dir (for the in-app updater) and version.
# -s -w drop the symbol table and DWARF data: the binary is a third smaller, so
# there is less to read off disk at every launch. Panics still carry full Go
# stack traces; only external debuggers lose information. (-trimpath is
# deliberately left out: it invalidates every cached gotk4 object, which would
# turn the next build — including the in-app update — back into a five-minute
# one.)
LDFLAGS := -s -w -X 'atlas-notes/internal/app.buildDir=$(CURDIR)' -X 'atlas-notes/internal/app.version=$(VERSION)'
BINDIR  := $(PREFIX)/bin
APPDIR  := $(PREFIX)/share/applications
ICONDIR := $(PREFIX)/share/icons/hicolor/scalable/apps

.PHONY: build run install uninstall clean mobile apk

build:
	go build -ldflags "$(LDFLAGS)" -o $(BIN) .

run: build
	./$(BIN)

# install builds the binary and installs the desktop entry + icon so Atlas Notes
# shows up in the GNOME app search (Super key) with a proper name and icon.
install: build
	mkdir -p $(BINDIR) $(APPDIR) $(ICONDIR)
	cp $(BIN) $(BINDIR)/.atlas-notes.new && mv -f $(BINDIR)/.atlas-notes.new $(BINDIR)/atlas-notes
	cp assets/atlas-notes.svg $(ICONDIR)/atlas-notes.svg
	rm -f $(APPDIR)/atlas-notes.desktop
	sed 's|@BIN@|$(BINDIR)/atlas-notes|' packaging/io.github.atlasnotes.desktop > $(APPDIR)/io.github.atlasnotes.desktop
	-update-desktop-database $(APPDIR) 2>/dev/null || true
	-gtk-update-icon-cache -f -t $(PREFIX)/share/icons/hicolor 2>/dev/null || true
	@echo "Atlas Notes installed — search 'Atlas Notes' from the Super/Activities menu."

uninstall:
	rm -f $(BINDIR)/atlas-notes $(APPDIR)/atlas-notes.desktop $(APPDIR)/io.github.atlasnotes.desktop $(ICONDIR)/atlas-notes.svg
	-update-desktop-database $(APPDIR) 2>/dev/null || true

clean:
	rm -rf bin/

# The phone build. It shares everything below the interface with the desktop
# app and none of the interface itself: GTK does not run on Android, so the
# touch interface is Gio, which draws its own widgets.
#
# mobile builds it for this machine, which is how to look at it without a
# phone. It needs Gio's desktop dependencies: on Fedora,
#   sudo dnf install libxkbcommon-devel libxkbcommon-x11-devel mesa-libEGL-devel \
#                    mesa-libGLES-devel wayland-devel libX11-devel libXcursor-devel \
#                    libXfixes-devel libxcb-devel vulkan-loader-devel
mobile:
	go build -o bin/atlas-mobile ./cmd/atlas-mobile

# apk needs the Android SDK and NDK. CI builds this on every tag; this target
# is for building one by hand.
apk:
	go run gioui.org/cmd/gogio -target android -arch arm64,arm \
		-appid io.github.atlasnotes -version $(shell echo $(VERSION) | awk -F. '{printf "%d", $$1*10000 + $$2*100 + $$3}') \
		-o bin/atlas-notes-$(VERSION).apk ./cmd/atlas-mobile
