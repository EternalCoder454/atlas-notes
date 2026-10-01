# RPM spec for Atlas Notes (Fedora, openSUSE, RHEL-alikes). Suitable for COPR: the
# build needs network access for the Go module cache, so enable it on the COPR
# project (`copr-cli modify --enable-net on`) or pre-fetch the modules.
#
#   rpmbuild -ba packaging/atlas-notes.spec      (Source0 is fetched by spectool
#                                                 -g -R, or drop the tarball in
#                                                 ~/rpmbuild/SOURCES)
#
# Removing the package (dnf remove atlas-notes) leaves your notes and settings in
# ~/.config/atlas-notes and ~/.local/share/atlas-notes.

# No debuginfo subpackage. The binary is linked with -s -w, which leaves no debug
# information to split out, and rpmbuild then fails the whole build on an empty
# debugsourcefiles.list rather than skipping the subpackage.
%global debug_package %{nil}

Name:           atlas-notes
Version:        %{?_version}%{!?_version:0.10.1}
Release:        1%{?dist}
Summary:        Fast local-first notes and checklists with an optional local AI assistant

License:        MIT
URL:            https://github.com/EternalCoder454/atlas-notes
Source0:        %{url}/archive/v%{version}/%{name}-%{version}.tar.gz

BuildRequires:  golang >= 1.26
BuildRequires:  gcc
BuildRequires:  pkgconfig(gtk4) >= 4.22
BuildRequires:  pkgconfig(libadwaita-1) >= 1.9
BuildRequires:  pkgconfig(glib-2.0) >= 2.88
BuildRequires:  pkgconfig(gobject-introspection-1.0)
BuildRequires:  desktop-file-utils

Requires:       gtk4 >= 4.22
Requires:       libadwaita >= 1.9
Requires:       glib2 >= 2.88
# The optional assistant talks to a local Ollama server; it is not packaged here.
Suggests:       ollama

%description
A notes and checklist app for GNOME written in Go with GTK4 and libadwaita.
Notes are plain Markdown files in a folder on your machine, with live preview,
real checklists, links and tags, full-text search and password protection. An
optional assistant runs on a local Ollama server; nothing is uploaded.

%prep
%autosetup -n %{name}-%{version}

%build
export CGO_ENABLED=1
# Position-independent, as Fedora's packaging guidelines expect of every
# executable. The version is stamped in as the package's own. The source path is not baked into the binary (the Makefile does
# that for source installs), so a packaged copy is never mistaken for one the
# app can rebuild.
go build -buildmode=pie -trimpath -ldflags="-s -w -X atlas-notes/internal/app.version=%{version}" -o %{name} .

%install
install -Dm755 %{name} %{buildroot}%{_bindir}/%{name}
install -Dm644 assets/atlas-notes.svg %{buildroot}%{_datadir}/icons/hicolor/scalable/apps/atlas-notes.svg
install -Dm644 assets/atlas-notes-16.svg %{buildroot}%{_datadir}/icons/hicolor/16x16/apps/atlas-notes.svg
install -Dm644 assets/atlas-notes-symbolic.svg %{buildroot}%{_datadir}/icons/hicolor/symbolic/apps/atlas-notes-symbolic.svg
install -d %{buildroot}%{_datadir}/applications
sed 's|@BIN@|%{_bindir}/%{name}|g' packaging/io.github.atlasnotes.desktop \
    > %{buildroot}%{_datadir}/applications/io.github.atlasnotes.desktop

%check
desktop-file-validate %{buildroot}%{_datadir}/applications/io.github.atlasnotes.desktop

%files
%license LICENSE NOTICE
%doc README.md
%{_bindir}/%{name}
%{_datadir}/applications/io.github.atlasnotes.desktop
%{_datadir}/icons/hicolor/scalable/apps/atlas-notes.svg
%{_datadir}/icons/hicolor/16x16/apps/atlas-notes.svg
%{_datadir}/icons/hicolor/symbolic/apps/atlas-notes-symbolic.svg

%changelog
* Wed Sep 30 2026 EternalHell <77252745+EternalCoder454@users.noreply.github.com> - 0.8.1-1
- Packaged build of Atlas Notes 0.8.1
