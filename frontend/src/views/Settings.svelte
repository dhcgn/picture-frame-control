<script lang="ts">
  import { onMount } from 'svelte'
  import { SettingsService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Settings } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import { errText, loadFrames, notify, probeAll } from '../lib/state.svelte'

  let s = $state<Settings | null>(null)
  let name = $state('')

  async function load() {
    s = await SettingsService.Get()
    name = s.clientName
  }
  onMount(load)

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

<div class="head"><div><h1>Einstellungen</h1><p>Immich und Werkzeuge folgen mit den nächsten Meilensteinen.</p></div></div>

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

<style>
  .key { word-break: break-all; margin-bottom: 10px; }
  .buttons { display: flex; gap: 8px; flex-wrap: wrap; }
  .row input { margin-right: 6px; }
</style>
