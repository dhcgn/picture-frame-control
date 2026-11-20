<script lang="ts">
  import { onMount } from 'svelte'
  import { FrameService, SettingsService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Discovered } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import FrameArt from '../lib/FrameArt.svelte'
  import PairDialog from '../lib/PairDialog.svelte'
  import { aboutOf, app, errText, isPortrait, loadFrames, nameOf, notify, probe, probeAll, screenOf, statusLine, type Frame } from '../lib/state.svelte'
  import { photos, t } from '../lib/i18n.svelte'

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
    if (n === 0) return t('Noch kein Frame gekoppelt.', 'No frame paired yet.')
    if (n === 1) return t(`Ein gekoppelter Frame, davon ${online} erreichbar.`, `One paired frame, ${online} reachable.`)
    return t(`${n} gekoppelte Frames, davon ${online} erreichbar.`, `${n} paired frames, ${online} reachable.`)
  }

  async function search() {
    searching = true
    try {
      found = (await FrameService.Discover()) ?? []
      if (found.length === 0) notify(t('Im lokalen Netz hat sich kein Frame gemeldet.', 'No frame answered on the local network.'))
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
    const name = nameOf(app.frames.find((x) => x.id === f.id) ?? f)
    notify(t(`Mit „${name}“ gekoppelt.`, `Paired with “${name}”.`))
  }

  async function importUnframeo() {
    try {
      const n = await SettingsService.ImportUnframeo(unframeoPath)
      await loadFrames()
      probeAll()
      notify(n === 1 ? t('Ein Frame aus unframeo übernommen.', 'Took over one frame from unframeo.') : t(`${n} Frames aus unframeo übernommen.`, `Took over ${n} frames from unframeo.`))
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  function facts(f: Frame) {
    const s = screenOf(f)
    const parts = s.w ? [`${s.w} × ${s.h}`] : []
    if (f.rotate) parts.push(t('hochkant', 'portrait'))
    const c = app.status[f.id]?.count ?? -1
    if (c >= 0) parts.push(photos(c))
    return parts.join(' · ')
  }
</script>

<div class="head">
  <div>
    <h1>Frames</h1>
    <p>{subtitle()}</p>
  </div>
  <div class="actions">
    <button class="btn" onclick={probeAll} disabled={app.frames.length === 0}>{t('Status aktualisieren', 'Refresh status')}</button>
    <button class="btn" onclick={search} disabled={searching}>{searching ? t('Suche…', 'Searching…') : t('Im Netzwerk suchen', 'Search the network')}</button>
    <button class="btn primary" onclick={() => pairDlg.open()}>{t('Frame koppeln', 'Pair a frame')}</button>
  </div>
</div>

<div class="wall">
  {#each app.frames as f (f.id)}
    {@const st = app.status[f.id]}
    {@const line = statusLine(st)}
    <button class="hang" class:portrait={isPortrait(f)} onclick={() => onopen(f.id)} aria-label={t(`${nameOf(f)} öffnen`, `Open ${nameOf(f)}`)}>
      <FrameArt frame={f} recent={st?.recent} dim={st !== undefined && !st.online} />
      <div class="label">
        <h3>{nameOf(f)}</h3>
        {#if aboutOf(f)}<div class="place">{aboutOf(f)}</div>{/if}
        <div class="mono">{facts(f) || t('Auflösung noch unbekannt', 'Resolution not known yet')}</div>
        <div class="status"><span class="dot {line.dot}"></span>{line.text}</div>
      </div>
    </button>
  {/each}

  {#each unpaired as d (d.instance)}
    <div class="found">
      <strong>{t('Frame im Netzwerk gefunden', 'Frame found on the network')}</strong>
      <span class="mono">{d.host}</span>
      <span>{t('Zum Koppeln den Code vom Display des Frames eingeben.', 'To pair, enter the code shown on the frame.')}</span>
      <div><button class="btn" onclick={() => pairDlg.open()}>{t('Koppeln', 'Pair')}</button></div>
    </div>
  {/each}

  {#if app.frames.length === 0}
    <div class="found">
      <strong>{t('Noch kein Frame gekoppelt', 'No frame paired yet')}</strong>
      <span>{t('Am Frame „Freund hinzufügen“ wählen und den angezeigten Code eingeben.', 'On the frame, choose “Add friend” and enter the code it shows.')}</span>
      <div><button class="btn primary" onclick={() => pairDlg.open()}>{t('Frame koppeln', 'Pair a frame')}</button></div>
      {#if unframeoPath}
        <span class="muted">{t('Oder die mit unframeo gekoppelten Frames übernehmen, ohne neu zu koppeln:', 'Or take over the frames paired with unframeo, without pairing again:')}</span>
        <div><button class="btn" onclick={importUnframeo}>{t('Aus unframeo übernehmen', 'Take over from unframeo')}</button></div>
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
  .label .place { font-size: 13px; color: var(--ink-2); margin: -2px 0 2px; }
  .status { font-size: 13px; margin-top: 4px; }
  .found { border: 2px dashed var(--rule); border-radius: 6px; padding: 26px 20px; text-align: center; color: var(--ink-2); align-self: stretch; display: flex; flex-direction: column; justify-content: center; gap: 10px; min-height: 220px; }
  .found strong { color: var(--ink); }
</style>
