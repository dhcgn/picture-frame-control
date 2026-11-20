# Picture-Frame-Control – Anforderungen

Stand: 2026-11-20 · Status: Entwurf zur Abstimmung

## 1. Ziel

Windows-Desktop-App (Go + Wails v3), die

1. Frameo-Bilderrahmen vollständig per UI steuert (Funktionsumfang von unframeo), und
2. Fotos/Videos aus einer entfernten Immich-Instanz auf mehrere Frames spiegelt.
   Die Auswahl erfolgt über mehrere Alben und Tags, N:M zu Frames.

## 2. Begriffe

| Begriff | Bedeutung |
|---|---|
| Frame | Ein gekoppelter Frameo-Bilderrahmen |
| Quelle | Ein Immich-Album oder ein Immich-Tag |
| Zuordnung | Frame ↔ Quellen (N:M) |
| Sollmenge | Vereinigung aller Assets der zugeordneten Quellen eines Frames, dedupliziert |
| Manifest | Lokaler Zustand je Frame: Immich-Asset ↔ Frame-Media-ID, Checksum, gesendete Variante |
| Fremd-Media | Media auf dem Frame, das nicht im Manifest steht (z. B. per Handy-App gesendet) |

## 3. Funktionale Anforderungen

### 3.1 Frame-Verwaltung (Parität zu unframeo)

| ID | Anforderung |
|---|---|
| F-01 | Frames im LAN finden (mDNS) und anzeigen |
| F-02 | Frame koppeln per Code vom Frame-Display. Name, Standort und native Auflösung (aus `info`) speichern. Ohne eigenen Namen zeigt die App den Standort vom Frame an, sonst dessen Namen. Bei Frameo ist der „Name“ der Besitzer |
| F-03 | Gekoppelte Frames auflisten, umbenennen und entfernen ("forget") |
| F-04 | Frame-Info anzeigen: Name, Auflösung, Berechtigungen, Verbindungsweg (lokal/Relay) |
| F-05 | Berechtigung "Ansehen/Verwalten" am Frame anfragen. Status anzeigen |
| F-06 | Medien auf dem Frame auflisten: Vorschau, Typ, sichtbar/versteckt, Aufnahme-/Empfangsdatum |
| F-07 | Einzelne Medien herunterladen (Vorschau/Original) |
| F-08 | Medien verstecken/anzeigen (reversibel) |
| F-09 | Medien löschen (endgültig, mit Bestätigung) |
| F-10 | Manuell Dateien senden, mit Option Zuschnitt vs. Einpassen und Bildunterschrift |
| F-11 | Client-Name, der am Frame erscheint, anzeigen und setzen |
| F-12 | Eigene Identität anzeigen. Identität exportieren/sichern (Verlust bedeutet neu koppeln) |
| F-13 | Verbindungsweg wählbar: auto / lokal / Relay |

### 3.2 Immich-Anbindung

| ID | Anforderung |
|---|---|
| I-01 | Genau ein Immich-Server mit einem API-Key: URL und Key konfigurieren, Verbindung testen (Serverversion ≥ 3.2.0) |
| I-02 | Alle Alben laden (Name, Anzahl, Cover) |
| I-03 | Alle Tags laden (Hierarchie, Anzahl) |
| I-04 | Mehrere Alben und Tags gleichzeitig auswählbar, mit Suche/Filter |
| I-05 | Assets einer Quelle über `POST /search/metadata` (albumIds/tagIds) mit Cursor-Paging laden |
| I-06 | Papierkorb- und archivierte Assets ausschließen (Standard, abschaltbar?) |

### 3.3 Zuordnung und Sync

| ID | Anforderung |
|---|---|
| S-01 | Je Frame beliebig viele Quellen zuordnen. Eine Quelle kann mehreren Frames zugeordnet sein |
| S-02 | Sollmenge je Frame = Vereinigung der Quellen, dedupliziert per Asset-ID |
| S-03 | Sync-Vorschau vor dem Ausführen: neu / unverändert / zu löschen / Fremd-Media, mit Größenabschätzung |
| S-04 | Spiegeln: fehlende Assets senden, nicht mehr enthaltene löschen |
| S-05 | Geänderte Assets (Checksum geändert) ersetzen: löschen und neu senden |
| S-06 | Fremd-Media: Der Client fragt je Sync, ob sie gelöscht werden sollen (alle / auswählen / behalten) |
| S-07 | Fortschritt je Frame und je Asset. Abbrechen möglich. Teil-Sync bleibt konsistent im Manifest |
| S-08 | Fehler je Asset protokollieren, nächstes Asset weiter verarbeiten (kein Abbruch des Gesamtlaufs) |
| S-09 | Fehler 12 "frame limit reached" erkennen. Sync für diesen Frame stoppen und melden |
| S-10 | Optionales Limit je Frame (Anzahl und/oder MB). Bei Überschreitung oder Fehler 12: neueste zuerst (Aufnahmedatum absteigend). Der Rest erscheint in der Vorschau als "nicht gesendet" |
| S-11 | Sync nur manuell auslösen, je Frame oder für alle. Kein automatischer Sync |
| S-12 | Optional je Frame: Albumname als Bildunterschrift. Gemeint ist ein Immich-Album, in dem das Bild liegt, auch wenn es über einen Tag ausgewählt ist. Bei mehreren gewinnt ein dem Frame zugeordnetes Album, sonst das kleinste. Bilder ohne Album bleiben ohne Unterschrift. Eine geänderte Unterschrift sendet das Bild neu |

### 3.4 Medienaufbereitung

| ID | Anforderung |
|---|---|
| M-01 | Bilder ohne ImageMagick: Immich-Preview (JPEG) unverändert senden. Füllt sie den Bildschirm nicht (z. B. Hochformat auf Full HD), das Fullsize-Bild. Kein Bild soll unter der nativen Auflösung des Frames ankommen |
| M-02 | Bilder mit ImageMagick (`magick.exe`, Pfad in Config): die Immich-Preview auf die native Frame-Auflösung verkleinern, als WebP. Das Bild deckt den Bildschirm ab („Zuschneiden“) oder passt hinein („Einpassen“). Den Zuschnitt selbst macht der Frame |
| M-03 | Videos mit ffmpeg (Pfad in Config): auf native Frame-Auflösung konvertieren |
| M-04 | Videos ohne ffmpeg: überspringen und in der Vorschau als "übersprungen" ausweisen |
| M-05 | Tool-Erkennung in den Einstellungen: Pfad prüfen, Version anzeigen |
| M-06 | Konvertierte Dateien zwischenspeichern (Cache-Verzeichnis), damit derselbe Asset für mehrere Frames mit gleicher Auflösung nur einmal konvertiert wird |

### 3.5 Einstellungen

| ID | Anforderung |
|---|---|
| E-01 | Immich: URL, API-Key |
| E-02 | Tools: Pfade zu magick.exe und ffmpeg.exe |
| E-03 | WebP-Qualität, Video-Bitrate/CRF (vorerst fest: WebP-Qualität 82) |
| E-04 | Cache-Verzeichnis und Cache leeren |

## 4. Nicht-funktionale Anforderungen

| ID | Anforderung |
|---|---|
| N-01 | Windows 11, Wails v3, Go (aktuelle Version) |
| N-02 | Frameo-Protokoll und SecureDeviceGrid-Transport als eigene Pakete in der App. Code aus unframeo darf übernommen und angepasst werden. Die App steht unter **GPL-3.0-or-later** (inkl. NOTICE zu opensdg) |
| N-03 | API-Key und Frame-Identität geschützt ablegen (Windows Credential Manager / DPAPI statt Klartext) |
| N-04 | Immich-Zugriffe mit Retry/Backoff (429, 5xx, Timeouts) |
| N-05 | Manifest-Updates atomar nach jedem bestätigten Asset (Absturz verliert höchstens ein Asset) |
| N-06 | Keine gefährlichen Protokollnachrichten: Schutz gegen die Kollisionen GetMedia/DeleteMedia und hide/delete, kein Senden von Msg 46 |
| N-07 | Offline-Tests gegen einen Fake-Frame und einen Fake-Immich. Live-Tests per Build-Tag |
| N-08 | UI-Sprache Deutsch und Englisch, umschaltbar. Standard: die Windows-Anzeigesprache |

## 5. Randbedingungen aus der Analyse

- Der Frame kennt keine Alben. Die Gruppierung existiert nur im Client.
- Der Frame meldet weder Speicherkapazität noch Bildmaße. Einzige Grenze ist Fehler 12.
- Pairing allein erlaubt nur Senden. Listen, Löschen und Verstecken brauchen "Allow" am Frame. Spiegeln (S-04) setzt das voraus.
- Der Frame speichert Original plus eine Vorschau mit ~570 px. Eine kleinere Datei spart also nur beim Original.
- Der Sync läuft nur, solange die App läuft und der Frame erreichbar ist (LAN oder Relay).
- unframeo ist GPL-3, die App übernimmt die Lizenz. Der Autor von opensdg bittet, es nicht kommerziell zu nutzen (Bitte, keine Lizenzbedingung).

## 6. Offene Punkte

| # | Frage | Klärung durch |
|---|---|---|
| O-2 | ~~Liefert `list` dieselbe Media-ID, die der Client beim Senden vergibt?~~ **Ja** (2026-11-20): Alle 131 gesendeten Bilder eines echten Frames stehen unter der gesendeten ID in der Liste. Das Bildraster zeigt sie als „Aus Immich“ | geklärt |
| O-3 | Zeigt der Frame WebP an? Sonst JPEG als Ziel (M-02) | Live-Test |
| O-4 | Welche Videoformate, Längen und Größen akzeptiert der Frame? Typ "video+thumb" braucht vermutlich ein separates Vorschaubild | Live-Test + Protokollanalyse |
| O-5 | Papierkorb/Archiv (I-06): immer ausschließen oder abschaltbar? | Entscheidung Nutzer |

**Entschieden (2026-11-20):** Lizenz GPL-3 · Frame voll: neueste zuerst · Sync nur manuell · ein Immich-Server mit einem API-Key.

## 7. Benötigte Live-Tests (vor der Implementierung)

Mit vorhandenem unframeo und einem echten Frame:

1. `send` mit einem Testbild, danach `list`: ID und captureDate vergleichen (O-2).
2. `send` einer WebP-Datei: wird sie angezeigt? (O-3)
3. `send` eines kurzen MP4: Schlägt es fehl, oder braucht es den Video-Typ? (O-4)
