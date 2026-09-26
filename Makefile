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

.PHONY: build run install uninstall clean aar apk

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

# The phone build.
#
# It shares everything below the interface with the desktop app and none of the
# interface itself: GTK does not run on Android. The phone interface is Kotlin
# and Jetpack Compose, in packaging/android, and it reaches the Go core through
# bindings gomobile generates from ./mobile.
#
# aar builds those bindings. It needs the Android NDK, which gomobile finds
# through ANDROID_NDK_HOME, and gomobile itself:
#
#	go install golang.org/x/mobile/cmd/gomobile@$(MOBILE_VERSION)
#	go install golang.org/x/mobile/cmd/gobind@$(MOBILE_VERSION)
#	gomobile init
MOBILE_VERSION := $(shell go list -m -f '{{.Version}}' golang.org/x/mobile)
AAR := packaging/android/app/libs/atlasbridge.aar

aar:
	mkdir -p $(dir $(AAR))
	gomobile bind -target=android/arm64,android/arm -androidapi 24 \
		-javapkg io.github.atlasnotes.core -o $(AAR) ./mobile

# apk builds the app around those bindings. CI does this on every tag; this
# target is for building one by hand, and needs the Android SDK and Gradle.
apk: aar
	cd packaging/android && gradle --no-daemon assembleRelease
	@mkdir -p bin
	cp packaging/android/app/build/outputs/apk/release/app-release.apk \
		bin/atlas-notes-$(VERSION).apk
	@echo "bin/atlas-notes-$(VERSION).apk"
