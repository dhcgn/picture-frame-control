<script lang="ts">
  import { onMount } from 'svelte'
  import { Browser } from '@wailsio/runtime'
  import { ImmichService, SettingsService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Settings } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import { app, checkUpdate, errText, installUpdate, loadFrames, loadImmich, loadSources, notify, probeAll } from '../lib/state.svelte'

  let s = $state<Settings | null>(null)
  let checking = $state(false)

  async function lookForUpdate() {
    checking = true
    await checkUpdate()
    checking = false
    if (app.update?.enabled && !app.update.available) notify(`${app.update.current} ist die neueste Version.`)
  }
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

  async function connect(e: SubmitEvent) {
    e.preventDefault()
    connecting = true
    try {
      const info = await ImmichService.Connect(server, apiKey)
      apiKey = ''
      await loadImmich()
      app.sources = null
      loadSources()
      notify(`Mit Immich ${info.version} verbunden.`)
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
      notify('Absendername gespeichert. Frames sehen ihn ab der nächsten Verbindung.')
    } catch (err) {
      notify(errText(err), 'error')
    }
  }

  async function backup() {
    try {
      const path = await SettingsService.ExportIdentity()
      if (path) notify(`Sicherung in ${path} gespeichert. Bewahre sie verschlüsselt auf.`)
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
      notify(n === 1 ? 'Ein Frame übernommen.' : `${n} Frames übernommen.`)
    } catch (e) {
      notify(errText(e), 'error')
    }
  }
</script>

<div class="head"><div><h1>Einstellungen</h1><p>Werkzeuge für WebP und Videos folgen mit den nächsten Meilensteinen.</p></div></div>

<h2 class="group-title">Immich</h2>
<form class="form panel" onsubmit={connect}>
  <div class="row">
    <label class="k" for="iu">Server-Adresse<small>Ohne /api am Ende</small></label>
    <div><input id="iu" type="text" inputmode="url" placeholder="https://photos.example.lan" bind:value={server} required /></div>
  </div>
  <div class="row">
    <label class="k" for="ik">API-Key<small>Verschlüsselt gespeichert</small></label>
    <div>
      <input id="ik" type="password" autocomplete="off" bind:value={apiKey}
        placeholder={app.immich.conn.hasKey ? 'Gespeichert. Leer lassen, um ihn zu behalten' : 'In Immich: Kontoeinstellungen → API-Schlüssel'} />
      <button class="btn primary" disabled={connecting || !server.trim() || (!apiKey && !app.immich.conn.hasKey)}>
        {connecting ? 'Prüfe…' : 'Verbinden'}
      </button>
      {#if app.immich.state === 'ok'}
        <p class="hint ok">Verbunden mit Immich {app.immich.info?.version} als „{app.immich.info?.user}“.</p>
      {:else if app.immich.state === 'error'}
        <p class="hint warn">{app.immich.error}</p>
      {/if}
      <p class="hint">Der Key braucht Lesezugriff auf Alben, Tags und Assets. Für den Sync zusätzlich Ansehen und Herunterladen von Assets.</p>
    </div>
  </div>
</form>

{#if s}
  <h2 class="group-title">Identität</h2>
  <div class="form panel">
    <form class="row" onsubmit={saveName}>
      <label class="k" for="cn">Absendername<small>So erscheinen deine Bilder am Frame</small></label>
      <div>
        <input id="cn" type="text" bind:value={name} required />
        <button class="btn" disabled={name.trim() === s.clientName}>Speichern</button>
      </div>
    </form>
    <div class="row">
      <span class="k">Schlüssel<small>Jeder Frame ist mit ihm gekoppelt</small></span>
      <div>
        {#if s.hasIdentity}
          <div class="mono key">{s.publicKey}</div>
        {:else}
          <span class="muted">Wird beim ersten Koppeln erzeugt.</span>
        {/if}
        <div class="buttons">
          <button class="btn" onclick={backup} disabled={!s.hasIdentity}>Sichern…</button>
          <button class="btn" onclick={importUnframeo}>Aus unframeo übernehmen…</button>
        </div>
        <p class="hint warn">Geht der Schlüssel verloren, muss jeder Frame vor Ort neu gekoppelt werden. Lege eine Sicherung an. Die Sicherung ist eine unframeo-Konfiguration und enthält den Schlüssel unverschlüsselt.</p>
        {#if s.unframeoPath}
          <p class="hint">unframeo-Konfiguration gefunden: <span class="mono">{s.unframeoPath}</span></p>
        {/if}
      </div>
    </div>
    <div class="row">
      <span class="k">Speicherort<small>Schlüssel per Windows verschlüsselt</small></span>
      <div class="mono">{s.dataDir}</div>
    </div>
  </div>
{/if}

{#if app.update}
  {@const u = app.update}
  <h2 class="group-title">Version</h2>
  <div class="form panel">
    <div class="row">
      <span class="k">Installiert<small>Updates kommen von GitHub, geprüft per SHA-256</small></span>
      <div>
        <span class="mono">{u.current}</span>
        {#if !u.enabled}
          <p class="hint">Entwicklungsversion: sucht keine Updates.</p>
        {:else if u.available}
          <p class="hint ok">Version {u.latest} ist verfügbar. <button class="link" onclick={() => Browser.OpenURL(u.url)}>Was ist neu?</button></p>
          <button class="btn primary" onclick={installUpdate}>Jetzt aktualisieren</button>
        {:else}
          <button class="btn" onclick={lookForUpdate} disabled={checking}>{checking ? 'Suche…' : 'Nach Updates suchen'}</button>
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .key { word-break: break-all; margin-bottom: 10px; }
  .buttons { display: flex; gap: 8px; flex-wrap: wrap; }
  .row input { margin-right: 6px; }
  .link { all: unset; cursor: pointer; text-decoration: underline; }
  .link:focus-visible { outline: 2px solid var(--brass); }
  input[type='password'] { border: 1px solid var(--rule); background: var(--wall); border-radius: 6px; padding: 7px 10px; width: min(420px, 100%); }
</style>
