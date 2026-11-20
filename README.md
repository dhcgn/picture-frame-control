# Picture-Frame-Control

[![CI](https://github.com/dhcgn/picture-frame-control/actions/workflows/ci.yml/badge.svg)](https://github.com/dhcgn/picture-frame-control/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/dhcgn/picture-frame-control)](https://github.com/dhcgn/picture-frame-control/releases/latest)

Windows-App, die Frameo-Bilderrahmen steuert und Alben und Tags aus [Immich](https://immich.app) auf einen oder mehrere Frames spiegelt.

- Frames koppeln, ansehen, Bilder aus- und einblenden, löschen, herunterladen, senden
- Alben und Tags aus Immich je Frame zuordnen (N:M), mit Vorschau, was ein Sync ändert
- Sync: neue Bilder senden, entfernte löschen, fremde Bilder nur auf Nachfrage anfassen
- optional der Albumname als Bildunterschrift

## Installation

1. Die neueste `picture-frame-control_<version>_windows_amd64.exe` von den [Releases](https://github.com/dhcgn/picture-frame-control/releases/latest) laden und starten. Es gibt keinen Installer.
2. Unter **Einstellungen** Immich-Adresse und API-Key eintragen. Der Key braucht Lesezugriff auf Alben, Tags und Assets.
3. Unter **Frames** einen Frame koppeln (Code vom Frame-Display) oder vorhandene Kopplungen aus [unframeo](https://github.com/borje/unframeo) übernehmen.

Einstellungen liegen in `%APPDATA%\picture-frame-control`. API-Key und Schlüssel sind dort mit Windows (DPAPI) für deinen Benutzer verschlüsselt.

## Updates

Die App fragt beim Start GitHub, ob es eine neuere Version gibt. Ist eine da, erscheint in der Seitenleiste „Update auf vX“. Erst ein Klick lädt sie, prüft sie gegen `SHA256SUMS`, ersetzt das Programm und startet neu. Vorabversionen (`v1.2.0-rc.1`) werden nicht angeboten.

## Entwickeln

Voraussetzungen: Go 1.27, Node 22, `wails3` v3.1.0 (`go install github.com/wailsapp/wails/v3/cmd/wails3@v3.1.0`).

```powershell
wails3 dev      # App mit Hot-Reload
wails3 build    # bin\picture-frame-control.exe, Version "dev"
go test ./...   # Tests; Frame, Grid und Immich laufen als Fakes
```

Live-Tests gegen einen echten, mit unframeo gekoppelten Frame: `go test -tags live ./internal/frameo`

Dokumentation: [Anforderungen](docs/anforderungen.md) · [Umsetzungskonzept](docs/konzept.md) · [Mockups](mockups/index.html)

## Release

Ein Tag `v*` startet [`release.yml`](.github/workflows/release.yml): Build mit der Tag-Version, Release mit der EXE und `SHA256SUMS`.

```powershell
git tag v1.0.0
git push origin v1.0.0
```

Ein Tag mit Zusatz (`v1.1.0-rc.1`) wird als Vorabversion veröffentlicht.

### Release Notes mit KI (optional)

Ist das Repository-Secret `COPILOT_PAT` gesetzt, schreibt GitHub Copilot die Release Notes aus den Commits seit dem letzten Release ([`actions/ai-inference`](https://github.com/actions/ai-inference)). Ohne Secret, oder wenn der Schritt fehlschlägt, bekommt das Release GitHubs automatisch erzeugte Notes.

Einrichten:

1. Ein GitHub-Konto mit Copilot-Zugang.
2. Einen [Fine-grained Personal Access Token](https://github.com/settings/personal-access-tokens/new) anlegen, mit der Berechtigung **Copilot Requests**.
3. Ihn als Secret `COPILOT_PAT` unter *Settings → Secrets and variables → Actions* im Repository hinterlegen.

## Lizenz

GPL-3.0-or-later. Das Frameo-Protokoll stammt aus [unframeo](https://github.com/borje/unframeo), Details in [NOTICE](NOTICE). Frameo ist eine Marke der Frameo ApS, Immich ein Projekt von FUTO. Dieses Projekt ist mit keinem von beiden verbunden.
