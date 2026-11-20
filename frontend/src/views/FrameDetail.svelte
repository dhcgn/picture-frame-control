<script lang="ts">
  import { onMount } from 'svelte'
  import { SvelteSet } from 'svelte/reactivity'
  import { Events } from '@wailsio/runtime'
  import { FrameService, ImmichService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { MediaItem, Selection, SendProgress } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import FrameArt from '../lib/FrameArt.svelte'
  import { aboutOf, app, errText, loadFrames, loadSources, nameOf, notify, probe, screenOf, sourceIds, statusLine, type Frame } from '../lib/state.svelte'
  import { locale, num, photos, t } from '../lib/i18n.svelte'

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
  let filter = $state<'all' | 'synced' | 'foreign' | 'hidden'>('all')
  const selected = new SvelteSet<string>()
  let sending = $state<SendProgress | null>(null)

  const shown = $derived(
    (media ?? []).filter((m) =>
      filter === 'all' ? true : filter === 'synced' ? m.synced : filter === 'foreign' ? !m.synced : !m.visible,
    ),
  )
  const counts = $derived({
    synced: (media ?? []).filter((m) => m.synced).length,
    foreign: (media ?? []).filter((m) => !m.synced).length,
    hidden: (media ?? []).filter((m) => !m.visible).length,
  })

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

  function hide(visible: boolean) {
    const what = photos(selected.size)
    act(visible ? 'show' : 'hide', () => FrameService.SetVisible(id, ids(), visible),
      visible ? t(`${what} eingeblendet.`, `${what} shown.`) : t(`${what} ausgeblendet.`, `${what} hidden.`))
  }

  function remove() {
    const what = photos(selected.size)
    if (!confirm(t(`${what} endgültig vom Frame löschen? Das lässt sich nicht rückgängig machen.`,
      `Delete ${what} from the frame for good? This cannot be undone.`))) return
    act('delete', () => FrameService.Delete(id, ids()), t(`${what} gelöscht.`, `${what} deleted.`))
  }

  async function download() {
    busy = 'download'
    try {
      const dir = await FrameService.Download(id, ids())
      if (dir) notify(t(`In ${dir} gespeichert.`, `Saved in ${dir}.`))
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
      if (sent) notify(t(`${photos(sent)} gesendet.`, `${photos(sent)} sent.`))
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
  const formOf = (f?: Frame) => ({
    name: f?.name ?? '', limit: f?.limit ?? 0, scale: f?.scale ?? 'crop', rotate: f?.rotate ?? false, mode: f?.mode ?? 'auto', caption: f?.caption ?? '',
  })
  let form = $state(formOf(app.frames.find((f) => f.id === id)))
  const fromFrame = $derived(frame ? frame.place || frame.frameName : '') // what an empty name falls back to
  const dirty = $derived(
    !!frame &&
      (form.name.trim() !== frame.name || form.limit !== frame.limit || form.scale !== frame.scale ||
        form.rotate !== frame.rotate || form.mode !== frame.mode || form.caption !== frame.caption),
  )
  let asking = $state<'' | 'view' | 'manage'>('')

  async function save(e: SubmitEvent) {
    e.preventDefault()
    try {
      form = formOf(await FrameService.UpdateSettings(id, { ...form, limit: Number(form.limit) || 0 }))
      await loadFrames()
      notify(t('Einstellungen gespeichert.', 'Settings saved.'))
    } catch (err) {
      notify(errText(err), 'error')
    }
  }

  async function ask(manage: boolean) {
    asking = manage ? 'manage' : 'view'
    try {
      await FrameService.RequestPermission(id, manage)
      await probe(id)
      notify(t('Berechtigung erteilt.', 'Permission granted.'))
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      asking = ''
    }
  }

  async function forget() {
    if (!frame) return
    const name = nameOf(frame)
    if (!confirm(t(`„${name}“ aus der App entfernen? Die Bilder bleiben auf dem Frame. Zum erneuten Senden muss er neu gekoppelt werden.`,
      `Remove “${name}” from the app? The photos stay on the frame. To send again, it has to be paired again.`))) return
    try {
      await FrameService.Forget(id)
      await loadFrames()
      notify(t(`„${name}“ entfernt.`, `“${name}” removed.`))
      onback()
    } catch (e) {
      notify(errText(e), 'error')
    }
  }

  const when = (ms: number) => (ms ? new Date(ms).toLocaleString(locale(), { dateStyle: 'medium', timeStyle: 'short' }) : t('unbekannt', 'unknown'))
  const tip = (m: MediaItem) =>
    [
      t(`Aufgenommen ${when(m.captured)}`, `Taken ${when(m.captured)}`),
      t(`empfangen ${when(m.received)}`, `received ${when(m.received)}`),
      m.synced ? t('aus Immich', 'from Immich') : t('fremd', 'foreign'),
      m.caption ? t(`„${m.caption}“`, `“${m.caption}”`) : '',
    ].filter(Boolean).join(' · ')
</script>

{#if frame}
  {@const s = screenOf(frame)}
  <button class="crumb" onclick={onback}>← Frames</button>
  <div class="detail-head">
    <div class="art"><FrameArt {frame} recent={status?.recent} dim={status !== undefined && !status.online} /></div>
    <div>
      <h1>{nameOf(frame)}</h1>
      {#if aboutOf(frame)}<p class="place muted">{aboutOf(frame)}</p>{/if}
      <p class="mono muted">
        {s.w ? `${s.w} × ${s.h}` : t('Auflösung unbekannt', 'Resolution unknown')}
        {#if status && status.count >= 0}· {photos(status.count)}{/if}
        · <span class="dot {line.dot}"></span>{line.text}
      </p>
      {#if status && !status.online}
        <p class="hint warn">{status.error} <button class="link" onclick={() => probe(id)}>{t('Erneut versuchen', 'Try again')}</button></p>
      {/if}
    </div>
    {#if status?.online}
      <button class="btn primary send" onclick={sendFiles} disabled={!!busy}>
        {sending ? t(`Sende ${sending.done + 1} von ${sending.total}…`, `Sending ${sending.done + 1} of ${sending.total}…`) : t('Dateien senden…', 'Send files…')}
      </button>
    {/if}
  </div>

  <div class="tabs" role="tablist">
    <button role="tab" aria-selected={tab === 'media'} onclick={() => (tab = 'media')}>{t('Bilder auf dem Frame', 'Photos on the frame')}</button>
    <button role="tab" aria-selected={tab === 'sources'} onclick={() => (tab = 'sources')}>{t('Quellen', 'Sources')}</button>
    <button role="tab" aria-selected={tab === 'settings'} onclick={() => (tab = 'settings')}>{t('Einstellungen', 'Settings')}</button>
  </div>

  {#if tab === 'media'}
    {#if !status}
      <p class="muted">{t('Verbinde mit dem Frame…', 'Connecting to the frame…')}</p>
    {:else if !status.online}
      <p class="muted">{t('Der Frame ist gerade nicht erreichbar.', 'The frame is not reachable right now.')}</p>
    {:else if !status.canView}
      <div class="panel empty">
        <p><strong>{t('Die App darf die Bilder auf diesem Frame noch nicht sehen.', 'The app may not see the photos on this frame yet.')}</strong></p>
        <p class="muted">{t('Nach dem Anfragen am Frame auf „Erlauben“ tippen. Ohne diese Berechtigung kann die App nur senden.',
          'After requesting, tap “Allow” on the frame. Without this permission the app can only send.')}</p>
        <button class="btn primary" onclick={() => ask(true)} disabled={!!asking}>
          {asking ? t('Warte auf Antwort am Frame…', 'Waiting for an answer on the frame…') : t('Ansehen und Verwalten anfragen', 'Request view and manage')}
        </button>
      </div>
    {:else}
      <div class="toolbar">
        <span class="sel">{selected.size ? t(`${selected.size} ausgewählt`, `${selected.size} selected`) : t('Keine Auswahl', 'Nothing selected')}</span>
        <button class="btn" disabled={!selected.size || !!busy || !status.canManage} onclick={() => hide(false)}>{t('Ausblenden', 'Hide')}</button>
        <button class="btn" disabled={!selected.size || !!busy || !status.canManage} onclick={() => hide(true)}>{t('Einblenden', 'Show')}</button>
        <button class="btn" disabled={!selected.size || !!busy} onclick={download}>{busy === 'download' ? t('Lade…', 'Loading…') : t('Herunterladen', 'Download')}</button>
        <button class="btn danger" disabled={!selected.size || !!busy || !status.canManage} onclick={remove}>{t('Löschen', 'Delete')}</button>
        <span class="spacer"></span>
        <div class="seg" role="group" aria-label="Filter">
          <button aria-pressed={filter === 'all'} onclick={() => (filter = 'all')}>{t('Alle', 'All')}</button>
          <button aria-pressed={filter === 'synced'} onclick={() => (filter = 'synced')}>{t('Aus Immich', 'From Immich')} <span class="c">{counts.synced}</span></button>
          <button aria-pressed={filter === 'foreign'} onclick={() => (filter = 'foreign')}>{t('Fremd', 'Foreign')} <span class="c">{counts.foreign}</span></button>
          <button aria-pressed={filter === 'hidden'} onclick={() => (filter = 'hidden')}>{t('Ausgeblendet', 'Hidden')} <span class="c">{counts.hidden}</span></button>
        </div>
      </div>
      {#if !status.canManage}
        <p class="hint">{t('Ausblenden und Löschen brauchen die Berechtigung „Verwalten“.', 'Hiding and deleting need the “manage” permission.')} <button class="link" onclick={() => (tab = 'settings')}>{t('Anfragen', 'Request')}</button></p>
      {/if}
      {#if mediaError}
        <p class="warn">{mediaError} <button class="link" onclick={loadMedia}>{t('Erneut laden', 'Reload')}</button></p>
      {:else if loading && media === null}
        <p class="muted">{t('Lade die Liste der Bilder…', 'Loading the list of photos…')}</p>
      {:else if shown.length === 0}
        <p class="muted">{filter === 'all' ? t('Auf diesem Frame sind keine Bilder.', 'There are no photos on this frame.') : t('Keine Bilder für diesen Filter.', 'No photos for this filter.')}</p>
      {:else}
        <div class="grid">
          {#each shown as m (m.id)}
            <button class="tile" class:hidden-media={!m.visible} aria-pressed={selected.has(m.id)} onclick={() => toggle(m.id)} title={tip(m)}>
              <img src={`/thumb/${id}/${m.id}`} alt="" loading="lazy" onerror={(e) => ((e.currentTarget as HTMLImageElement).style.visibility = 'hidden')} />
              <span class="badges">
                {#if !m.synced}<span class="badge foreign">{t('Fremd', 'Foreign')}</span>{/if}
                {#if !m.visible}<span class="badge">{t('Ausgeblendet', 'Hidden')}</span>{/if}
                {#if m.type === 'video'}<span class="badge">▶ Video</span>{/if}
                {#if m.type === 'greeting'}<span class="badge">{t('Gruß', 'Greeting')}</span>{/if}
              </span>
            </button>
          {/each}
        </div>
        <p class="legend">
          <span><i class="foreign"></i>{t('Fremd: nicht vom Sync dieser App gesendet, etwa per Frameo-App vom Handy', 'Foreign: not sent by this app’s sync, e.g. from a phone with the Frameo app')}</span>
        </p>
      {/if}
    {/if}
  {:else if tab === 'sources'}
    {@const sids = sourceIds(frame)}
    <div class="toolbar">
      <span class="muted">{t('Diese Alben und Tags bilden zusammen die Bilderauswahl des Frames.', 'Together these albums and tags make up the frame’s selection of photos.')}</span>
      <span class="spacer"></span>
      <button class="btn" onclick={onassign}>{t('In der Zuordnung bearbeiten', 'Edit in the assignment')}</button>
    </div>
    {#if sids.length === 0}
      <div class="panel empty">
        <p><strong>{t('Diesem Frame sind noch keine Alben oder Tags zugeordnet.', 'No albums or tags are assigned to this frame yet.')}</strong></p>
        <p class="muted">{t('Ohne Zuordnung spiegelt der Sync nichts auf diesen Frame.', 'Without an assignment, the sync mirrors nothing onto this frame.')}</p>
      </div>
    {:else}
      <ul class="list panel">
        {#each sids as sid (sid)}
          {@const src = app.sources?.find((x) => x.id === sid)}
          <li>
            <span class="kind">{frame.sources.tags?.includes(sid) ? 'Tag' : 'Album'}</span>
            {#if src}{src.name}{:else if app.sources}<span class="warn">{t('In Immich nicht mehr vorhanden', 'No longer in Immich')}</span>{:else}<span class="mono muted">{sid}</span>{/if}
            <span class="n mono">{src && src.count >= 0 ? num(src.count) : ''}</span>
          </li>
        {/each}
      </ul>
      {#if selection}
        <p class="hint">
          {#if selection.videos}
            {t(`Zusammen ${num(selection.images)} Bilder und ${num(selection.videos)} Videos (vorerst übersprungen), Doppelte einmal gezählt.`,
              `Together ${num(selection.images)} photos and ${num(selection.videos)} videos (skipped for now), duplicates counted once.`)}
          {:else}
            {t(`Zusammen ${num(selection.images)} Bilder, Doppelte einmal gezählt.`, `Together ${num(selection.images)} photos, duplicates counted once.`)}
          {/if}
          {#if frame.limit && selection.images > frame.limit}
            <span class="warn">{t(`Das Limit von ${frame.limit} Bildern schneidet ${selection.images - frame.limit} ältere ab.`,
              `The limit of ${frame.limit} photos leaves out ${selection.images - frame.limit} older ones.`)}</span>
          {/if}
        </p>
      {/if}
    {/if}
  {:else}
    <form class="form panel" onsubmit={save}>
      <div class="row">
        <label class="k" for="fn">Name<small>{t('Leer: der Standort vom Frame', 'Empty: the frame’s location')}</small></label>
        <div>
          <input id="fn" type="text" bind:value={form.name} placeholder={fromFrame || t('Vom Frame noch unbekannt', 'Not known from the frame yet')} />
          {#if form.name && fromFrame}
            <button type="button" class="link" onclick={() => (form.name = '')}>{t(`Vom Frame übernehmen („${fromFrame}“)`, `Use the frame’s (“${fromFrame}”)`)}</button>
          {/if}
          {#if frame.place || frame.frameName}
            <p class="hint">{t('Laut Frame:', 'According to the frame:')} {[frame.place && t(`Standort ${frame.place}`, `location ${frame.place}`), frame.frameName && t(`Besitzer ${frame.frameName}`, `owner ${frame.frameName}`)].filter(Boolean).join(' · ')}</p>
          {/if}
        </div>
      </div>
      <div class="row">
        <span class="k">{t('Bildunterschrift', 'Caption')}<small>{t('Wird mit jedem Bild gesendet', 'Sent with every photo')}</small></span>
        <div>
          <div class="seg" role="group" aria-label={t('Bildunterschrift', 'Caption')}>
            <button type="button" aria-pressed={form.caption === ''} onclick={() => (form.caption = '')}>{t('Keine', 'None')}</button>
            <button type="button" aria-pressed={form.caption === 'album'} onclick={() => (form.caption = 'album')}>{t('Albumname', 'Album name')}</button>
          </div>
          <p class="hint">
            {t('Der Name eines Immich-Albums, in dem das Bild liegt, auch wenn es über einen Tag ausgewählt ist. Bei mehreren gewinnt ein zugeordnetes Album, sonst das kleinste. Bilder ohne Album bleiben ohne Unterschrift.',
              'The name of an Immich album the photo is in, even when it is selected by a tag. With several, an assigned album wins, otherwise the smallest. Photos in no album get no caption.')}
            {#if form.caption !== frame.caption}<br /><span class="warn">{t('Nach dem Speichern sendet der nächste Sync die betroffenen Bilder neu.', 'After saving, the next sync sends the affected photos again.')}</span>{/if}
          </p>
        </div>
      </div>
      <div class="row">
        <label class="k" for="lim">{t('Höchstens senden', 'Send at most')}<small>{t('0 = kein Limit. Darüber zählen die neuesten Bilder', '0 = no limit. Beyond it, the newest photos count')}</small></label>
        <div><input id="lim" type="number" min="0" bind:value={form.limit} /> <span class="muted">{t('Bilder', 'photos')}</span></div>
      </div>
      <div class="row">
        <span class="k">{t('Darstellung', 'Display')}</span>
        <div>
          <div class="seg" role="group" aria-label={t('Darstellung', 'Display')}>
            <button type="button" aria-pressed={form.scale === 'crop'} onclick={() => (form.scale = 'crop')}>{t('Zuschneiden', 'Crop')}</button>
            <button type="button" aria-pressed={form.scale === 'fit'} onclick={() => (form.scale = 'fit')}>{t('Einpassen', 'Fit')}</button>
          </div>
          <p class="hint">{t('Zuschneiden füllt den ganzen Bildschirm, Ränder des Bildes gehen verloren. Einpassen zeigt das ganze Bild, mit Balken.',
            'Crop fills the whole screen and loses the photo’s edges. Fit shows the whole photo, with bars.')}</p>
        </div>
      </div>
      <div class="row">
        <label class="k" for="rot">{t('Hochkant aufgestellt', 'Standing upright')}<small>{t('Tauscht Breite und Höhe', 'Swaps width and height')}</small></label>
        <div><input id="rot" type="checkbox" bind:checked={form.rotate} /> <label for="rot">{t('Ziel:', 'Target:')} {frame.screen.w ? (form.rotate ? `${frame.screen.h} × ${frame.screen.w}` : `${frame.screen.w} × ${frame.screen.h}`) : t('unbekannt', 'unknown')}</label></div>
      </div>
      <div class="row">
        <span class="k">{t('Verbindung', 'Connection')}</span>
        <div class="seg" role="group" aria-label={t('Verbindung', 'Connection')}>
          <button type="button" aria-pressed={form.mode === 'auto'} onclick={() => (form.mode = 'auto')}>{t('Automatisch', 'Automatic')}</button>
          <button type="button" aria-pressed={form.mode === 'local'} onclick={() => (form.mode = 'local')}>{t('Nur lokal', 'Local only')}</button>
          <button type="button" aria-pressed={form.mode === 'relay'} onclick={() => (form.mode = 'relay')}>{t('Nur Relay', 'Relay only')}</button>
        </div>
      </div>
      <div class="row">
        <span class="k">{t('Berechtigungen', 'Permissions')}<small>{t('Erteilt am Frame selbst', 'Granted on the frame itself')}</small></span>
        <div>
          {#if status?.online}
            <div class="perm">
              <span class="y">{t('Senden', 'Send')}</span>
              <span class={status.canView ? 'y' : 'n'}>{t('Ansehen', 'View')}</span>
              <span class={status.canManage ? 'y' : 'n'}>{t('Verwalten', 'Manage')}</span>
              {#if !status.canManage}
                <button type="button" class="btn" onclick={() => ask(true)} disabled={!!asking}>
                  {asking ? t('Warte auf Antwort am Frame…', 'Waiting for an answer on the frame…') : t('Verwalten anfragen', 'Request manage')}
                </button>
              {/if}
            </div>
            {#if asking}
              <p class="hint">{t('Am Frame auf „Erlauben“ tippen. Die App wartet bis zu drei Minuten.', 'Tap “Allow” on the frame. The app waits up to three minutes.')}</p>
            {:else if !status.canManage}
              <p class="hint warn">{t('Ohne „Verwalten“ kann der Sync nichts löschen.', 'Without “manage”, the sync cannot delete anything.')}</p>
            {/if}
          {:else}
            <span class="muted">{t('Erst sichtbar, wenn der Frame erreichbar ist.', 'Shown once the frame is reachable.')}</span>
          {/if}
        </div>
      </div>
      <div class="row">
        <span class="k">{t('Frame entfernen', 'Remove frame')}</span>
        <div>
          <button type="button" class="btn danger" onclick={forget}>{t('Frame vergessen', 'Forget frame')}</button>
          <p class="hint">{t('Die Bilder bleiben auf dem Frame. Zum erneuten Senden muss er neu gekoppelt werden.', 'The photos stay on the frame. To send again, it has to be paired again.')}</p>
        </div>
      </div>
      <div class="row">
        <span></span>
        <div><button class="btn primary" disabled={!dirty}>{t('Änderungen speichern', 'Save changes')}</button></div>
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
  .detail-head .place { margin: -2px 0 6px; }
  .send { margin-left: auto; align-self: flex-start; }
  .tabs { display: flex; gap: 2px; border-bottom: 1px solid var(--rule); margin-bottom: 18px; }
  .tabs button { all: unset; cursor: pointer; padding: 10px 16px; color: var(--ink-2); border-bottom: 2px solid transparent; margin-bottom: -1px; }
  .tabs button[aria-selected='true'] { color: var(--ink); border-color: var(--brass); }
  .tabs button:focus-visible { outline: 2px solid var(--brass); }
  .toolbar { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 14px; }
  .sel { font-weight: 700; margin-right: 8px; min-width: 110px; }
  .spacer { flex: 1; }
  .seg .c { font: 11px var(--mono); opacity: .7; margin-left: 2px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(118px, 1fr)); gap: 8px; }
  .tile { all: unset; cursor: pointer; aspect-ratio: 16 / 10; border-radius: 3px; position: relative; overflow: hidden; background: var(--wall-2); }
  .tile img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .tile.hidden-media img { opacity: .35; }
  .tile:focus-visible { outline: 2px solid var(--brass); outline-offset: 2px; }
  .tile[aria-pressed='true'] { outline: 3px solid var(--brass); outline-offset: -3px; }
  .tile[aria-pressed='true']::after { content: '✓'; position: absolute; top: 5px; left: 6px; width: 20px; height: 20px; border-radius: 50%; background: var(--brass); color: var(--brass-ink); font: 700 13px/20px var(--ui); text-align: center; }
  .badges { position: absolute; right: 5px; bottom: 5px; display: flex; gap: 3px; }
  .badge { font: 11px var(--mono); padding: 1px 6px; border-radius: 3px; background: rgb(0 0 0 / .62); color: #fff; }
  .badge.foreign { background: var(--foreign); }
  .legend { font-size: 13px; color: var(--ink-2); margin-top: 14px; }
  .legend i { display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: 6px; vertical-align: -1px; background: var(--foreign); }
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
