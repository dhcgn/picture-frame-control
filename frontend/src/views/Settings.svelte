<script lang="ts">
  import { onMount } from 'svelte'
  import { Browser } from '@wailsio/runtime'
  import { ImmichService, SettingsService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Settings } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import { app, checkUpdate, errText, installUpdate, loadFrames, loadImmich, loadSources, notify, probeAll } from '../lib/state.svelte'
  import { lang, t } from '../lib/i18n.svelte'

  let s = $state<Settings | null>(null)
  let checking = $state(false)
  let name = $state('')
  let server = $state(app.immich.conn.server)
  let apiKey = $state('')
  let connecting = $state(false)

  async function load() {
    s = await SettingsService.Get()
    name = s.clientName
  }
  onMount(load)
  // The stored address may arrive after this page opened.
  $effect(() => {
    if (!server && app.immich.conn.server) server = app.immich.conn.server
  })

  async function setLanguage(choice: string) {
    try {
      lang.current = (await SettingsService.SetLanguage(choice)) as 'de' | 'en'
      await load()
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  let toolBusy = $state(false)
  async function tool(change: () => Promise<void>) {
    toolBusy = true
    try {
      await change()
      await load()
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      toolBusy = false
    }
  }
  const toolText = $derived<Record<string, { name: string; what: string; on: string; off: string; broken: string }>>({
    magick: {
      name: 'ImageMagick',
      what: t('Optional. Bringt jedes Bild auf die Auflösung des Frames, als WebP', 'Optional. Brings every photo down to the frame’s resolution, as WebP'),
      on: t('Neue Bilder gehen verkleinert auf die Frames.', 'New photos go to the frames shrunk.'),
      off: t('Nicht eingerichtet: Die Frames bekommen die Bilder aus Immich unverändert.', 'Not set up: frames get the photos from Immich as they are.'),
      broken: t('Bis das behoben ist, gehen die Bilder unverändert.', 'Until that is fixed, photos go as they are.'),
    },
    ffmpeg: {
      name: 'ffmpeg',
      what: t('Optional. Wandelt Videos so um, wie Frameo sie abspielt', 'Optional. Converts videos the way Frameo plays them'),
      on: t('Videos gehen mit: H.264, höchstens so groß wie der Bildschirm, 30 fps, bis 4:59.', 'Videos go too: H.264, no larger than the screen, 30 fps, up to 4:59.'),
      off: t('Nicht eingerichtet: Videos werden übersprungen.', 'Not set up: videos are skipped.'),
      broken: t('Bis das behoben ist, werden Videos übersprungen.', 'Until that is fixed, videos are skipped.'),
    },
  })

  async function lookForUpdate() {
    checking = true
    await checkUpdate()
    checking = false
    if (app.update?.enabled && !app.update.available)
      notify(t(`${app.update.current} ist die neueste Version.`, `${app.update.current} is the latest version.`))
  }

  async function connect(e: SubmitEvent) {
    e.preventDefault()
    connecting = true
    try {
      const info = await ImmichService.Connect(server, apiKey)
      apiKey = ''
      await loadImmich()
      app.sources = null
      loadSources()
      notify(t(`Mit Immich ${info.version} verbunden.`, `Connected to Immich ${info.version}.`))
    } catch (err) {
      notify(errText(err), 'error')
    } finally {
      connecting = false
    }
  }

  async function saveName(e: SubmitEvent) {
    e.preventDefault()
    try {
      await SettingsService.SetClientName(name)
      await load()
      notify(t('Absendername gespeichert. Frames sehen ihn ab der nächsten Verbindung.', 'Sender name saved. Frames see it from the next connection on.'))
    } catch (err) {
      notify(errText(err), 'error')
    }
  }

  async function backup() {
    try {
      const path = await SettingsService.ExportIdentity()
      if (path) notify(t(`Sicherung in ${path} gespeichert. Bewahre sie verschlüsselt auf.`, `Backup saved in ${path}. Keep it encrypted.`))
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  async function importUnframeo() {
    try {
      const n = await SettingsService.ImportUnframeo('')
      await load()
      await loadFrames()
      probeAll()
      notify(n === 1 ? t('Ein Frame übernommen.', 'Took over one frame.') : t(`${n} Frames übernommen.`, `Took over ${n} frames.`))
    } catch (e) {
      notify(errText(e), 'error')
    }
  }
</script>

<div class="head"><div><h1>{t('Einstellungen', 'Settings')}</h1></div></div>

{#if s}
  <h2 class="group-title">{t('Sprache', 'Language')}</h2>
  <div class="form panel">
    <div class="row">
      <span class="k">{t('Sprache der App', 'App language')}</span>
      <div class="seg" role="group" aria-label={t('Sprache', 'Language')}>
        <button aria-pressed={s.languageChoice === ''} onclick={() => setLanguage('')}>{t('Wie Windows', 'Like Windows')}</button>
        <button aria-pressed={s.languageChoice === 'de'} onclick={() => setLanguage('de')}>Deutsch</button>
        <button aria-pressed={s.languageChoice === 'en'} onclick={() => setLanguage('en')}>English</button>
      </div>
    </div>
  </div>
{/if}

<h2 class="group-title">Immich</h2>
<form class="form panel" onsubmit={connect}>
  <div class="row">
    <label class="k" for="iu">{t('Server-Adresse', 'Server address')}<small>{t('Ohne /api am Ende', 'Without /api at the end')}</small></label>
    <div><input id="iu" type="text" inputmode="url" placeholder="https://photos.example.lan" bind:value={server} required /></div>
  </div>
  <div class="row">
    <label class="k" for="ik">API-Key<small>{t('Verschlüsselt gespeichert', 'Stored encrypted')}</small></label>
    <div>
      <input id="ik" type="password" autocomplete="off" bind:value={apiKey}
        placeholder={app.immich.conn.hasKey ? t('Gespeichert. Leer lassen, um ihn zu behalten', 'Saved. Leave empty to keep it') : t('In Immich: Kontoeinstellungen → API-Schlüssel', 'In Immich: account settings → API keys')} />
      <button class="btn primary" disabled={connecting || !server.trim() || (!apiKey && !app.immich.conn.hasKey)}>
        {connecting ? t('Prüfe…', 'Checking…') : t('Verbinden', 'Connect')}
      </button>
      {#if app.immich.state === 'ok'}
        <p class="hint ok">{t(`Verbunden mit Immich ${app.immich.info?.version} als „${app.immich.info?.user}“.`, `Connected to Immich ${app.immich.info?.version} as “${app.immich.info?.user}”.`)}</p>
      {:else if app.immich.state === 'error'}
        <p class="hint warn">{app.immich.error}</p>
      {/if}
      <p class="hint">{t('Der Key braucht Lesezugriff auf Alben, Tags und Assets. Für den Sync zusätzlich Ansehen und Herunterladen von Assets.',
        'The key needs read access to albums, tags and assets. For the sync, also viewing and downloading assets.')}</p>
    </div>
  </div>
</form>

{#if s}
  <h2 class="group-title">{t('Bilder und Videos aufbereiten', 'Preparing photos and videos')}</h2>
  <div class="form panel">
    {#each s.tools ?? [] as tl (tl.id)}
      {@const d = toolText[tl.id]}
      <div class="row">
        <span class="k">{d.name}<small>{d.what}</small></span>
        <div>
          {#if tl.path}
            <div class="mono path">{tl.path}</div>
            {#if tl.version}
              <p class="hint ok">{d.name} {tl.version}. {d.on}</p>
            {:else}
              <p class="hint warn">{tl.error} {d.broken}</p>
            {/if}
          {:else}
            <p class="muted">{d.off}</p>
          {/if}
          <div class="buttons">
            <button class="btn" disabled={toolBusy} onclick={() => tool(() => SettingsService.FindTool(tl.id))}>{t('Suchen', 'Find')}</button>
            <button class="btn" disabled={toolBusy} onclick={() => tool(() => SettingsService.ChooseTool(tl.id))}>{t('Auswählen…', 'Choose…')}</button>
            {#if tl.path}
              <button class="btn" disabled={toolBusy} onclick={() => tool(() => SettingsService.ClearTool(tl.id))}>{t('Nicht mehr nutzen', 'Stop using it')}</button>
            {/if}
          </div>
        </div>
      </div>
    {/each}
    <p class="hint tools-hint">{t('Gilt für alles, was ab jetzt gesendet wird. Was schon auf einem Frame ist, bringt „Alles neu senden“ in der Sync-Ansicht auf den neuen Stand.',
      'Applies to everything sent from now on. To bring what is already on a frame up to date, use “Resend everything” on the Sync page.')}</p>
  </div>

  <h2 class="group-title">{t('Identität', 'Identity')}</h2>
  <div class="form panel">
    <form class="row" onsubmit={saveName}>
      <label class="k" for="cn">{t('Absendername', 'Sender name')}<small>{t('So erscheinen deine Bilder am Frame', 'How your photos appear on the frame')}</small></label>
      <div>
        <input id="cn" type="text" bind:value={name} required />
        <button class="btn" disabled={name.trim() === s.clientName}>{t('Speichern', 'Save')}</button>
      </div>
    </form>
    <div class="row">
      <span class="k">{t('Schlüssel', 'Key')}<small>{t('Jeder Frame ist mit ihm gekoppelt', 'Every frame is paired with it')}</small></span>
      <div>
        {#if s.hasIdentity}
          <div class="mono key">{s.publicKey}</div>
        {:else}
          <span class="muted">{t('Wird beim ersten Koppeln erzeugt.', 'Created when you first pair a frame.')}</span>
        {/if}
        <div class="buttons">
          <button class="btn" onclick={backup} disabled={!s.hasIdentity}>{t('Sichern…', 'Back up…')}</button>
          <button class="btn" onclick={importUnframeo}>{t('Aus unframeo übernehmen…', 'Take over from unframeo…')}</button>
        </div>
        <p class="hint warn">{t('Geht der Schlüssel verloren, muss jeder Frame vor Ort neu gekoppelt werden. Lege eine Sicherung an. Die Sicherung ist eine unframeo-Konfiguration und enthält den Schlüssel unverschlüsselt.',
          'If the key is lost, every frame has to be paired again in person. Make a backup. The backup is an unframeo configuration and holds the key unencrypted.')}</p>
        {#if s.unframeoPath}
          <p class="hint">{t('unframeo-Konfiguration gefunden:', 'unframeo configuration found:')} <span class="mono">{s.unframeoPath}</span></p>
        {/if}
      </div>
    </div>
    <div class="row">
      <span class="k">{t('Speicherort', 'Location')}<small>{t('Schlüssel per Windows verschlüsselt', 'Keys encrypted by Windows')}</small></span>
      <div class="mono">{s.dataDir}</div>
    </div>
  </div>
{/if}

{#if app.update}
  {@const u = app.update}
  <h2 class="group-title">Version</h2>
  <div class="form panel">
    <div class="row">
      <span class="k">{t('Installiert', 'Installed')}<small>{t('Updates kommen von GitHub, geprüft per SHA-256', 'Updates come from GitHub, verified by SHA-256')}</small></span>
      <div>
        <span class="mono">{u.current}</span>
        {#if !u.enabled}
          <p class="hint">{t('Entwicklungsversion: sucht keine Updates.', 'Development build: does not look for updates.')}</p>
        {:else if u.available}
          <p class="hint ok">{t(`Version ${u.latest} ist verfügbar.`, `Version ${u.latest} is available.`)} <button class="link" onclick={() => Browser.OpenURL(u.url)}>{t('Was ist neu?', 'What’s new?')}</button></p>
          <button class="btn primary" onclick={installUpdate}>{t('Jetzt aktualisieren', 'Update now')}</button>
        {:else}
          <button class="btn" onclick={lookForUpdate} disabled={checking}>{checking ? t('Suche…', 'Searching…') : t('Nach Updates suchen', 'Check for updates')}</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .key, .path { word-break: break-all; margin-bottom: 10px; }
  .tools-hint { grid-column: 1 / -1; padding: 14px 18px; margin: 0; }
  .buttons { display: flex; gap: 8px; flex-wrap: wrap; }
  .row input { margin-right: 6px; }
  .link { all: unset; cursor: pointer; text-decoration: underline; }
  .link:focus-visible { outline: 2px solid var(--brass); }
  input[type='password'] { border: 1px solid var(--rule); background: var(--wall); border-radius: 6px; padding: 7px 10px; width: min(420px, 100%); }
</style>
