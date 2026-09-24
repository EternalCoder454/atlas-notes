BIN     := bin/atlas-notes
VERSION ?= 0.5.3
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

.PHONY: build run install uninstall clean

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
