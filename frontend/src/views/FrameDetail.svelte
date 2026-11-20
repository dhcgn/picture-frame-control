<script lang="ts">
  import { onMount } from 'svelte'
  import { SvelteSet } from 'svelte/reactivity'
  import { Events } from '@wailsio/runtime'
  import { FrameService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { MediaItem, SendProgress } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import FrameArt from '../lib/FrameArt.svelte'
  import { ImmichService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Selection } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import { app, errText, loadFrames, loadSources, notify, probe, screenOf, sourceIds, statusLine, type Frame } from '../lib/state.svelte'

  let { id, onback, onassign }: { id: string; onback: () => void; onassign: () => void } = $props()

  const frame = $derived(app.frames.find((f) => f.id === id))
  const status = $derived(app.status[id])
  const line = $derived(statusLine(status))

  let tab = $state<'media' | 'sources' | 'settings'>('media')

  // ---- sources ----
  let selection = $state<Selection | null>(null)
  $effect(() => {
    if (tab !== 'sources' || !frame || app.immich.state !== 'ok') return
    loadSources()
    const { albums, tags } = frame.sources
    if (!albums?.length && !tags?.length) return
    ImmichService.Preview(albums ?? [], tags ?? []).then((r) => (selection = r)).catch(() => (selection = null))
  })

  // ---- photos on the frame ----
  let media = $state<MediaItem[] | null>(null)
  let mediaError = $state('')
  let loading = $state(false)
  let busy = $state('')
  let filter = $state<'all' | 'visible' | 'hidden'>('all')
  const selected = new SvelteSet<string>()
  let sending = $state<SendProgress | null>(null)

  const shown = $derived(
    (media ?? []).filter((m) => filter === 'all' || (filter === 'hidden' ? !m.visible : m.visible)),
  )

  async function loadMedia() {
    loading = true
    mediaError = ''
    try {
      media = (await FrameService.Media(id)) ?? []
      for (const s of [...selected]) if (!media.some((m) => m.id === s)) selected.delete(s)
    } catch (e) {
      media = []
      mediaError = errText(e)
    } finally {
      loading = false
    }
  }

  $effect(() => {
    if (status?.canView && media === null && !loading) loadMedia()
  })

  onMount(() => {
    if (!app.status[id]) probe(id)
    return Events.On('send:progress', (ev) => {
      const p = ev.data as SendProgress
      if (p.frame === id) sending = p.done < p.total ? p : null
    })
  })

  function toggle(mid: string) {
    if (selected.has(mid)) selected.delete(mid)
    else selected.add(mid)
  }

  async function act(label: string, run: () => Promise<unknown>, done: string) {
    busy = label
    try {
      await run()
      notify(done)
      selected.clear()
      await Promise.all([loadMedia(), probe(id)])
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      busy = ''
    }
  }

  const ids = () => [...selected]
  const n = () => (selected.size === 1 ? 'ein Bild' : `${selected.size} Bilder`)

  function hide(visible: boolean) {
    const what = n()
    act(visible ? 'show' : 'hide', () => FrameService.SetVisible(id, ids(), visible), visible ? `${what} eingeblendet.` : `${what} ausgeblendet.`)
  }

  function remove() {
    const what = n()
    if (!confirm(`${what} endgültig vom Frame löschen? Das lässt sich nicht rückgängig machen.`)) return
    act('delete', () => FrameService.Delete(id, ids()), `${what} gelöscht.`)
  }

  async function download() {
    busy = 'download'
    try {
      const dir = await FrameService.Download(id, ids())
      if (dir) notify(`In ${dir} gespeichert.`)
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      busy = ''
    }
  }

  async function sendFiles() {
    busy = 'send'
    try {
      const sent = await FrameService.SendFiles(id, frame?.scale === 'fit', '')
      if (sent) notify(sent === 1 ? 'Ein Bild gesendet.' : `${sent} Bilder gesendet.`)
      await Promise.all([loadMedia(), probe(id)])
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      busy = ''
      sending = null
    }
  }

  // ---- settings ----
  // The form starts from the saved values and is not reset while editing,
  // even when the frame list reloads underneath it.
  const formOf = (f?: Frame) => ({ name: f?.name ?? '', limit: f?.limit ?? 0, scale: f?.scale ?? 'crop', rotate: f?.rotate ?? false, mode: f?.mode ?? 'auto' })
  let form = $state(formOf(app.frames.find((f) => f.id === id)))
  const dirty = $derived(
    !!frame &&
      (form.name !== frame.name || form.limit !== frame.limit || form.scale !== frame.scale || form.rotate !== frame.rotate || form.mode !== frame.mode),
  )
  let asking = $state<'' | 'view' | 'manage'>('')

  async function save(e: SubmitEvent) {
    e.preventDefault()
    try {
      form = formOf(await FrameService.UpdateSettings(id, { ...form, limit: Number(form.limit) || 0 }))
      await loadFrames()
      notify('Einstellungen gespeichert.')
    } catch (err) {
      notify(errText(err), 'error')
    }
  }

  async function ask(manage: boolean) {
    asking = manage ? 'manage' : 'view'
    try {
      await FrameService.RequestPermission(id, manage)
      await probe(id)
      notify('Berechtigung erteilt.')
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      asking = ''
    }
  }

  async function forget() {
    if (!frame || !confirm(`„${frame.name}“ aus der App entfernen? Die Bilder bleiben auf dem Frame. Zum erneuten Senden muss er neu gekoppelt werden.`)) return
    try {
      await FrameService.Forget(id)
      await loadFrames()
      notify(`„${frame.name}“ entfernt.`)
      onback()
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  const when = (ms: number) => (ms ? new Date(ms).toLocaleString('de-DE', { dateStyle: 'medium', timeStyle: 'short' }) : 'unbekannt')
</script>

{#if frame}
  {@const s = screenOf(frame)}
  <button class="crumb" onclick={onback}>← Frames</button>
  <div class="detail-head">
    <div class="art"><FrameArt {frame} recent={status?.recent} dim={status !== undefined && !status.online} /></div>
    <div>
      <h1>{frame.name}</h1>
      <p class="mono muted">
        {s.w ? `${s.w} × ${s.h}` : 'Auflösung unbekannt'}
        {#if status && status.count >= 0}· {status.count} Bilder{/if}
        · <span class="dot {line.dot}"></span>{line.text}
      </p>
      {#if status && !status.online}
        <p class="hint warn">{status.error} <button class="link" onclick={() => probe(id)}>Erneut versuchen</button></p>
      {/if}
    </div>
    {#if status?.online}
      <button class="btn primary send" onclick={sendFiles} disabled={!!busy}>
        {sending ? `Sende ${sending.done + 1} von ${sending.total}…` : 'Dateien senden…'}
      </button>
    {/if}
  </div>

  <div class="tabs" role="tablist">
    <button role="tab" aria-selected={tab === 'media'} onclick={() => (tab = 'media')}>Bilder auf dem Frame</button>
    <button role="tab" aria-selected={tab === 'sources'} onclick={() => (tab = 'sources')}>Quellen</button>
    <button role="tab" aria-selected={tab === 'settings'} onclick={() => (tab = 'settings')}>Einstellungen</button>
  </div>

  {#if tab === 'media'}
    {#if !status}
      <p class="muted">Verbinde mit dem Frame…</p>
    {:else if !status.online}
      <p class="muted">Der Frame ist gerade nicht erreichbar.</p>
    {:else if !status.canView}
      <div class="panel empty">
        <p><strong>Die App darf die Bilder auf diesem Frame noch nicht sehen.</strong></p>
        <p class="muted">Nach dem Anfragen am Frame auf „Erlauben“ tippen. Ohne diese Berechtigung kann die App nur senden.</p>
        <button class="btn primary" onclick={() => ask(true)} disabled={!!asking}>
          {asking ? 'Warte auf Antwort am Frame…' : 'Ansehen und Verwalten anfragen'}
        </button>
      </div>
    {:else}
      <div class="toolbar">
        <span class="sel">{selected.size ? `${selected.size} ausgewählt` : 'Keine Auswahl'}</span>
        <button class="btn" disabled={!selected.size || !!busy || !status.canManage} onclick={() => hide(false)}>Ausblenden</button>
        <button class="btn" disabled={!selected.size || !!busy || !status.canManage} onclick={() => hide(true)}>Einblenden</button>
        <button class="btn" disabled={!selected.size || !!busy} onclick={download}>{busy === 'download' ? 'Lade…' : 'Herunterladen'}</button>
        <button class="btn danger" disabled={!selected.size || !!busy || !status.canManage} onclick={remove}>Löschen</button>
        <span class="spacer"></span>
        <div class="seg" role="group" aria-label="Filter">
          <button aria-pressed={filter === 'all'} onclick={() => (filter = 'all')}>Alle</button>
          <button aria-pressed={filter === 'visible'} onclick={() => (filter = 'visible')}>Sichtbar</button>
          <button aria-pressed={filter === 'hidden'} onclick={() => (filter = 'hidden')}>Ausgeblendet</button>
        </div>
      </div>
      {#if !status.canManage}
        <p class="hint">Ausblenden und Löschen brauchen die Berechtigung „Verwalten“. <button class="link" onclick={() => (tab = 'settings')}>Anfragen</button></p>
      {/if}
      {#if mediaError}
        <p class="warn">{mediaError} <button class="link" onclick={loadMedia}>Erneut laden</button></p>
      {:else if loading && media === null}
        <p class="muted">Lade die Liste der Bilder…</p>
      {:else if shown.length === 0}
        <p class="muted">{filter === 'all' ? 'Auf diesem Frame sind keine Bilder.' : 'Keine Bilder für diesen Filter.'}</p>
      {:else}
        <div class="grid">
          {#each shown as m (m.id)}
            <button class="tile" class:hidden-media={!m.visible} aria-pressed={selected.has(m.id)} onclick={() => toggle(m.id)}
              title={`Aufgenommen ${when(m.captured)} · empfangen ${when(m.received)}`}>
              <img src={`/thumb/${id}/${m.id}`} alt="" loading="lazy" onerror={(e) => ((e.currentTarget as HTMLImageElement).style.visibility = 'hidden')} />
              {#if !m.visible}<span class="badge">Ausgeblendet</span>{/if}
              {#if m.type === 'video'}<span class="badge">▶ Video</span>{/if}
              {#if m.type === 'greeting'}<span class="badge">Gruß</span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    {/if}
  {:else if tab === 'sources'}
    {@const ids = sourceIds(frame)}
    <div class="toolbar">
      <span class="muted">Diese Alben und Tags bilden zusammen die Bilderauswahl des Frames.</span>
      <span class="spacer"></span>
      <button class="btn" onclick={onassign}>In der Zuordnung bearbeiten</button>
    </div>
    {#if ids.length === 0}
      <div class="panel empty">
        <p><strong>Diesem Frame sind noch keine Alben oder Tags zugeordnet.</strong></p>
        <p class="muted">Ohne Zuordnung spiegelt der Sync nichts auf diesen Frame.</p>
      </div>
    {:else}
      <ul class="list panel">
        {#each ids as sid (sid)}
          {@const src = app.sources?.find((x) => x.id === sid)}
          <li>
            <span class="kind">{frame.sources.tags?.includes(sid) ? 'Tag' : 'Album'}</span>
            {#if src}{src.name}{:else if app.sources}<span class="warn">In Immich nicht mehr vorhanden</span>{:else}<span class="mono muted">{sid}</span>{/if}
            <span class="n mono">{src && src.count >= 0 ? src.count.toLocaleString('de-DE') : ''}</span>
          </li>
        {/each}
      </ul>
      {#if selection}
        <p class="hint">
          Zusammen {selection.images.toLocaleString('de-DE')} Bilder{#if selection.videos}
            und {selection.videos.toLocaleString('de-DE')} Videos (vorerst übersprungen){/if}, Doppelte einmal gezählt.
          {#if frame.limit && selection.images > frame.limit}
            <span class="warn">Das Limit von {frame.limit} Bildern schneidet {selection.images - frame.limit} ältere ab.</span>
          {/if}
        </p>
      {/if}
    {/if}
  {:else}
    <form class="form panel" onsubmit={save}>
      <div class="row"><label class="k" for="fn">Name</label><div><input id="fn" type="text" bind:value={form.name} required /></div></div>
      <div class="row">
        <label class="k" for="lim">Höchstens senden<small>0 = kein Limit. Darüber zählen die neuesten Bilder</small></label>
        <div><input id="lim" type="number" min="0" bind:value={form.limit} /> <span class="muted">Bilder</span></div>
      </div>
      <div class="row">
        <span class="k">Darstellung</span>
        <div>
          <div class="seg" role="group" aria-label="Darstellung">
            <button type="button" aria-pressed={form.scale === 'crop'} onclick={() => (form.scale = 'crop')}>Zuschneiden</button>
            <button type="button" aria-pressed={form.scale === 'fit'} onclick={() => (form.scale = 'fit')}>Einpassen</button>
          </div>
          <p class="hint">Zuschneiden füllt den ganzen Bildschirm, Ränder des Bildes gehen verloren. Einpassen zeigt das ganze Bild, mit Balken.</p>
        </div>
      </div>
      <div class="row">
        <label class="k" for="rot">Hochkant aufgestellt<small>Tauscht Breite und Höhe</small></label>
        <div><input id="rot" type="checkbox" bind:checked={form.rotate} /> <label for="rot">Ziel: {frame.screen.w ? (form.rotate ? `${frame.screen.h} × ${frame.screen.w}` : `${frame.screen.w} × ${frame.screen.h}`) : 'unbekannt'}</label></div>
      </div>
      <div class="row">
        <span class="k">Verbindung</span>
        <div class="seg" role="group" aria-label="Verbindung">
          <button type="button" aria-pressed={form.mode === 'auto'} onclick={() => (form.mode = 'auto')}>Automatisch</button>
          <button type="button" aria-pressed={form.mode === 'local'} onclick={() => (form.mode = 'local')}>Nur lokal</button>
          <button type="button" aria-pressed={form.mode === 'relay'} onclick={() => (form.mode = 'relay')}>Nur Relay</button>
        </div>
      </div>
      <div class="row">
        <span class="k">Berechtigungen<small>Erteilt am Frame selbst</small></span>
        <div>
          {#if status?.online}
            <div class="perm">
              <span class="y">Senden</span>
              <span class={status.canView ? 'y' : 'n'}>Ansehen</span>
              <span class={status.canManage ? 'y' : 'n'}>Verwalten</span>
              {#if !status.canManage}
                <button type="button" class="btn" onclick={() => ask(true)} disabled={!!asking}>
                  {asking ? 'Warte auf Antwort am Frame…' : 'Verwalten anfragen'}
                </button>
              {/if}
            </div>
            {#if asking}
              <p class="hint">Am Frame auf „Erlauben“ tippen. Die App wartet bis zu drei Minuten.</p>
            {:else if !status.canManage}
              <p class="hint warn">Ohne „Verwalten“ kann der Sync nichts löschen.</p>
            {/if}
          {:else}
            <span class="muted">Erst sichtbar, wenn der Frame erreichbar ist.</span>
          {/if}
        </div>
      </div>
      <div class="row">
        <span class="k">Frame entfernen</span>
        <div>
          <button type="button" class="btn danger" onclick={forget}>Frame vergessen</button>
          <p class="hint">Die Bilder bleiben auf dem Frame. Zum erneuten Senden muss er neu gekoppelt werden.</p>
        </div>
      </div>
      <div class="row">
        <span></span>
        <div><button class="btn primary" disabled={!dirty}>Änderungen speichern</button></div>
      </div>
    </form>
  {/if}
{/if}

<style>
  .crumb, .link { all: unset; cursor: pointer; color: var(--ink-2); }
  .crumb { font-size: 14px; margin-bottom: 14px; display: inline-block; }
  .link { text-decoration: underline; }
  .crumb:hover, .link:hover { color: var(--ink); }
  .crumb:focus-visible, .link:focus-visible { outline: 2px solid var(--brass); }
  .detail-head { display: flex; gap: 22px; align-items: center; margin-bottom: 22px; }
  .art { width: 132px; flex: none; }
  h1 { font-family: var(--display); font-weight: 400; font-size: 38px; line-height: 1; margin: 0 0 6px; }
  .detail-head p { margin: 0; }
  .send { margin-left: auto; align-self: flex-start; }
  .tabs { display: flex; gap: 2px; border-bottom: 1px solid var(--rule); margin-bottom: 18px; }
  .tabs button { all: unset; cursor: pointer; padding: 10px 16px; color: var(--ink-2); border-bottom: 2px solid transparent; margin-bottom: -1px; }
  .tabs button[aria-selected='true'] { color: var(--ink); border-color: var(--brass); }
  .tabs button:focus-visible { outline: 2px solid var(--brass); }
  .toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 14px; }
  .sel { font-weight: 700; margin-right: 8px; min-width: 110px; }
  .spacer { flex: 1; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(118px, 1fr)); gap: 8px; }
  .tile { all: unset; cursor: pointer; aspect-ratio: 16 / 10; border-radius: 3px; position: relative; overflow: hidden; background: var(--wall-2); }
  .tile img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .tile.hidden-media img { opacity: .35; }
  .tile:focus-visible { outline: 2px solid var(--brass); outline-offset: 2px; }
  .tile[aria-pressed='true'] { outline: 3px solid var(--brass); outline-offset: -3px; }
  .tile[aria-pressed='true']::after { content: '✓'; position: absolute; top: 5px; left: 6px; width: 20px; height: 20px; border-radius: 50%; background: var(--brass); color: var(--brass-ink); font: 700 13px/20px var(--ui); text-align: center; }
  .badge { position: absolute; right: 5px; bottom: 5px; font: 11px var(--mono); padding: 1px 6px; border-radius: 3px; background: rgb(0 0 0 / .62); color: #fff; }
  .empty { padding: 22px 24px; }
  .empty p { margin: 0 0 10px; }
  .list { list-style: none; margin: 0; padding: 0; }
  .list li { display: flex; align-items: center; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--rule); }
  .list li:last-child { border-bottom: none; }
  .kind { font: 11px var(--mono); text-transform: uppercase; letter-spacing: .06em; color: var(--ink-2); width: 48px; }
  .list .n { margin-left: auto; }
  .perm { display: flex; gap: 18px; flex-wrap: wrap; align-items: center; }
  .perm .y::before { content: '✓ '; color: var(--send); }
  .perm .n::before { content: '✕ '; color: var(--del); }
</style>
