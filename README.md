# Picture-Frame-Control

Windows-Desktop-App (Go + Wails v3 + Svelte), die Frameo-Bilderrahmen steuert und Immich-Alben und -Tags auf mehrere Frames spiegelt.

- Anforderungen: [docs/anforderungen.md](docs/anforderungen.md)
- Umsetzungskonzept: [docs/konzept.md](docs/konzept.md)
- Mockups: [mockups/index.html](mockups/index.html)

## Entwickeln

Voraussetzungen: Go 1.27, Node 22, `wails3` (v3.1.0).

```powershell
wails3 dev      # App mit Hot-Reload
wails3 build    # bin\picture-frame-control.exe
go test ./...   # Tests (Frame und Grid laufen als Fakes)
```

Live-Tests gegen einen echten, mit unframeo gekoppelten Frame: `go test -tags live ./internal/frameo`

## Lizenz

GPL-3.0-or-later. Das Frameo-Protokoll stammt aus [unframeo](https://github.com/borje/unframeo), Details in [NOTICE](NOTICE).
