<script lang="ts">
  import { onMount } from 'svelte'
  import { FrameService, SettingsService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Discovered } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import FrameArt from '../lib/FrameArt.svelte'
  import PairDialog from '../lib/PairDialog.svelte'
  import { app, errText, isPortrait, loadFrames, notify, probe, probeAll, screenOf, statusLine, type Frame } from '../lib/state.svelte'

  let { onopen }: { onopen: (id: string) => void } = $props()

  let pairDlg: PairDialog
  let found = $state<Discovered[] | null>(null)
  let searching = $state(false)
  let unframeoPath = $state('')

  onMount(async () => {
    unframeoPath = (await SettingsService.Get()).unframeoPath
  })

  const online = $derived(app.frames.filter((f) => app.status[f.id]?.online).length)
  const unpaired = $derived(found?.filter((d) => !d.pairedId) ?? [])

  function subtitle() {
    const n = app.frames.length
    if (n === 0) return 'Noch kein Frame gekoppelt.'
    const frames = n === 1 ? 'Ein gekoppelter Frame' : `${n} gekoppelte Frames`
    return `${frames}, davon ${online} erreichbar.`
  }

  async function search() {
    searching = true
    try {
      found = (await FrameService.Discover()) ?? []
      if (found.length === 0) notify('Im lokalen Netz hat sich kein Frame gemeldet.')
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      searching = false
    }
  }

  async function paired(f: Frame) {
    await loadFrames()
    probe(f.id)
    found = found?.filter((d) => d.pairedId !== f.id) ?? null
    notify(`Mit „${f.name}“ gekoppelt.`)
  }

  async function importUnframeo() {
    try {
      const n = await SettingsService.ImportUnframeo(unframeoPath)
      await loadFrames()
      probeAll()
      notify(n === 1 ? 'Ein Frame aus unframeo übernommen.' : `${n} Frames aus unframeo übernommen.`)
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  function facts(f: Frame) {
    const s = screenOf(f)
    const parts = s.w ? [`${s.w} × ${s.h}`] : []
    if (f.rotate) parts.push('hochkant')
    const c = app.status[f.id]?.count ?? -1
    if (c >= 0) parts.push(c === 1 ? '1 Bild' : `${c} Bilder`)
    return parts.join(' · ')
  }
</script>

<div class="head">
  <div>
    <h1>Frames</h1>
    <p>{subtitle()}</p>
  </div>
  <div class="actions">
    <button class="btn" onclick={probeAll} disabled={app.frames.length === 0}>Status aktualisieren</button>
    <button class="btn" onclick={search} disabled={searching}>{searching ? 'Suche…' : 'Im Netzwerk suchen'}</button>
    <button class="btn primary" onclick={() => pairDlg.open()}>Frame koppeln</button>
  </div>
</div>

<div class="wall">
  {#each app.frames as f (f.id)}
    {@const st = app.status[f.id]}
    {@const line = statusLine(st)}
    <button class="hang" class:portrait={isPortrait(f)} onclick={() => onopen(f.id)} aria-label={`${f.name} öffnen`}>
      <FrameArt frame={f} recent={st?.recent} dim={st !== undefined && !st.online} />
      <div class="label">
        <h3>{f.name}</h3>
        <div class="mono">{facts(f) || 'Auflösung noch unbekannt'}</div>
        <div class="status"><span class="dot {line.dot}"></span>{line.text}</div>
      </div>
    </button>
  {/each}

  {#each unpaired as d (d.instance)}
    <div class="found">
      <strong>Frame im Netzwerk gefunden</strong>
      <span class="mono">{d.host}</span>
      <span>Zum Koppeln den Code vom Display des Frames eingeben.</span>
      <div><button class="btn" onclick={() => pairDlg.open()}>Koppeln</button></div>
    </div>
  {/each}

  {#if app.frames.length === 0}
    <div class="found">
      <strong>Noch kein Frame gekoppelt</strong>
      <span>Am Frame „Freund hinzufügen“ wählen und den angezeigten Code eingeben.</span>
      <div><button class="btn primary" onclick={() => pairDlg.open()}>Frame koppeln</button></div>
      {#if unframeoPath}
        <span class="muted">Oder die mit unframeo gekoppelten Frames übernehmen, ohne neu zu koppeln:</span>
        <div><button class="btn" onclick={importUnframeo}>Aus unframeo übernehmen</button></div>
      {/if}
    </div>
  {/if}
</div>

<PairDialog bind:this={pairDlg} onpaired={paired} />

<style>
  .wall { display: grid; grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 44px 40px; align-items: end; }
  .hang { all: unset; cursor: pointer; display: block; }
  .hang:focus-visible { outline: 2px solid var(--brass); outline-offset: 6px; }
  .hang :global(.frame) { transition: transform .2s ease; }
  .hang:hover :global(.frame) { transform: translateY(-3px); }
  .hang.portrait { max-width: 190px; justify-self: center; }
  .label { margin-top: 16px; padding: 10px 12px; background: var(--mat); border-left: 3px solid var(--brass); box-shadow: 0 1px 0 var(--rule); }
  .label h3 { font-family: var(--display); font-weight: 400; font-size: 20px; margin: 0 0 2px; }
  .label .mono { color: var(--ink-2); }
  .status { font-size: 13px; margin-top: 4px; }
  .found { border: 2px dashed var(--rule); border-radius: 6px; padding: 26px 20px; text-align: center; color: var(--ink-2); align-self: stretch; display: flex; flex-direction: column; justify-content: center; gap: 10px; min-height: 220px; }
  .found strong { color: var(--ink); }
</style>
