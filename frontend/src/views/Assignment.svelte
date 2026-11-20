<script lang="ts">
  import { onMount } from 'svelte'
  import { SvelteSet } from 'svelte/reactivity'
  import { ImmichService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import type { Selection } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
  import FrameArt from '../lib/FrameArt.svelte'
  import { app, errText, isPortrait, loadFrames, loadSources, notify, sourceIds, type Frame, type Source } from '../lib/state.svelte'

  let { onsettings, onframes }: { onsettings: () => void; onframes: () => void } = $props()

  // Working copy: per frame, the ids of the albums and tags ticked for it.
  let picks = $state<Record<string, SvelteSet<string>>>({})
  function reset() {
    picks = Object.fromEntries(app.frames.map((f) => [f.id, new SvelteSet(sourceIds(f))]))
  }
  reset()

  let query = $state('')
  let onlyAssigned = $state(false)
  let saving = $state(false)

  onMount(() => {
    if (app.immich.state === 'ok' || app.immich.state === 'checking') loadSources()
  })

  const kindOf = $derived(new Map((app.sources ?? []).map((s) => [s.id, s.kind])))
  const keyOf = (ids: Iterable<string>) => [...ids].sort().join(',')
  const changed = (f: Frame) => keyOf(picks[f.id] ?? []) !== keyOf(sourceIds(f))
  const dirty = $derived(app.frames.some(changed))
  $effect(() => {
    app.dirty = dirty
  })

  const visible = $derived(
    (app.sources ?? []).filter((s) => {
      if (query && !s.path.toLowerCase().includes(query.toLowerCase())) return false
      if (onlyAssigned && !app.frames.some((f) => picks[f.id]?.has(s.id))) return false
      return true
    }),
  )
  const albums = $derived(visible.filter((s) => s.kind === 'album'))
  const tags = $derived(visible.filter((s) => s.kind === 'tag'))

  // What each frame's selection adds up to, recounted shortly after a change.
  let results = $state<Record<string, Selection | null | string>>({}) // null while counting, string on error
  const counted: Record<string, string> = {}
  $effect(() => {
    if (!app.sources) return
    const keys = Object.fromEntries(app.frames.map((f) => [f.id, keyOf(picks[f.id] ?? [])]))
    const t = setTimeout(() => {
      for (const [id, key] of Object.entries(keys)) {
        if (counted[id] === key) continue
        counted[id] = key
        count(id, key)
      }
    }, 350)
    return () => clearTimeout(t)
  })

  function split(ids: Iterable<string>) {
    const a: string[] = []
    const t: string[] = []
    for (const id of ids) (kindOf.get(id) === 'tag' ? t : a).push(id)
    return { albums: a, tags: t }
  }

  async function count(id: string, key: string) {
    results[id] = null
    const { albums, tags } = split(key ? key.split(',') : [])
    try {
      const r = await ImmichService.Preview(albums, tags)
      if (counted[id] === key) results[id] = r
    } catch (e) {
      if (counted[id] === key) results[id] = errText(e)
    }
  }

  function toggle(frameID: string, sourceID: string) {
    const set = picks[frameID]
    if (set.has(sourceID)) set.delete(sourceID)
    else set.add(sourceID)
  }

  async function save() {
    saving = true
    try {
      for (const f of app.frames.filter(changed)) {
        const { albums, tags } = split(picks[f.id])
        await ImmichService.SetSources(f.id, albums, tags)
      }
      await loadFrames()
      reset()
      notify('Zuordnung gespeichert. Der Sync folgt mit dem nächsten Meilenstein.')
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      saving = false
    }
  }

  const n = (x: number) => x.toLocaleString('de-DE')
</script>

<div class="head">
  <div>
    <h1>Zuordnung</h1>
    <p>Welche Alben und Tags auf welchen Frame kommen.</p>
  </div>
  {#if app.immich.state === 'ok' && app.frames.length}
    <div class="actions">
      <button class="btn" onclick={() => loadSources(true)} disabled={app.sourcesLoading}>Aus Immich neu laden</button>
      <button class="btn" onclick={reset} disabled={!dirty || saving}>Verwerfen</button>
      <button class="btn primary" onclick={save} disabled={!dirty || saving}>{saving ? 'Speichere…' : 'Speichern'}</button>
    </div>
  {/if}
</div>

{#if app.immich.state === 'none' || app.immich.state === 'error'}
  <div class="panel empty">
    <p><strong>{app.immich.state === 'none' ? 'Immich ist noch nicht verbunden.' : 'Immich ist gerade nicht erreichbar.'}</strong></p>
    {#if app.immich.error}<p class="warn">{app.immich.error}</p>{/if}
    <p class="muted">Server-Adresse und API-Key trägst du in den Einstellungen ein.</p>
    <button class="btn primary" onclick={onsettings}>Zu den Einstellungen</button>
  </div>
{:else if app.frames.length === 0}
  <div class="panel empty">
    <p><strong>Noch kein Frame gekoppelt.</strong></p>
    <p class="muted">Erst wenn ein Frame gekoppelt ist, lässt sich ihm etwas zuordnen.</p>
    <button class="btn primary" onclick={onframes}>Zu den Frames</button>
  </div>
{:else if app.sourcesError}
  <div class="panel empty">
    <p class="warn">{app.sourcesError}</p>
    <button class="btn" onclick={() => loadSources(true)}>Erneut laden</button>
  </div>
{:else if !app.sources}
  <p class="muted">Lade Alben und Tags aus Immich und zähle ihre Bilder…</p>
{:else}
  <div class="filterbar">
    <input type="search" placeholder="Alben und Tags durchsuchen" aria-label="Alben und Tags durchsuchen" bind:value={query} />
    <label><input type="checkbox" bind:checked={onlyAssigned} /> Nur zugeordnete zeigen</label>
    <span class="muted count">
      {app.sources.filter((s) => s.kind === 'album').length} Alben · {app.sources.filter((s) => s.kind === 'tag').length} Tags
    </span>
  </div>

  <div class="wrap panel">
    <table>
      <thead>
        <tr>
          <th>Quelle</th>
          <th class="num">Bilder und Videos</th>
          {#each app.frames as f (f.id)}
            <th class="fr">
              <div class="mini" class:portrait={isPortrait(f)}><FrameArt frame={f} mini /></div>
              <span class="fname">{f.name}</span>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#snippet rows(list: Source[], title: string)}
          {#if list.length}
            <tr class="grp"><th colspan={2 + app.frames.length}>{title}</th></tr>
            {#each list as s (s.id)}
              <tr>
                <!-- While searching, parents may be filtered out, so show the whole path. -->
                <td style:padding-left={`${14 + (query ? 0 : s.depth * 20)}px`} title={s.path}>
                  {#if query && s.depth}<span class="muted">{s.path.slice(0, s.path.length - s.name.length)}</span>{s.name}
                  {:else}{#if s.depth}<span class="branch">└</span>{/if}{s.name}{/if}
                  {#if s.shared}<span class="muted"> · geteilt</span>{/if}
                </td>
                <td class="num mono">{s.count >= 0 ? n(s.count) : '–'}</td>
                {#each app.frames as f (f.id)}
                  <td class="c">
                    <input type="checkbox" aria-label={`${s.name} auf ${f.name}`} checked={picks[f.id]?.has(s.id)} onchange={() => toggle(f.id, s.id)} />
                  </td>
                {/each}
              </tr>
            {/each}
          {/if}
        {/snippet}
        {@render rows(albums, 'Alben')}
        {@render rows(tags, 'Tags')}
        {#if visible.length === 0}
          <tr><td colspan={2 + app.frames.length} class="muted">Keine Alben oder Tags passen zur Suche.</td></tr>
        {/if}
      </tbody>
      <tfoot>
        <tr>
          <td colspan="2"><b>Ergebnis je Frame</b><br /><span class="muted">Doppelte nur einmal gezählt</span></td>
          {#each app.frames as f (f.id)}
            {@const r = results[f.id]}
            <td class="c">
              {#if !picks[f.id]?.size}
                <span class="muted">Nichts zugeordnet</span>
              {:else if r === null || r === undefined}
                <span class="muted">zähle…</span>
              {:else if typeof r === 'string'}
                <span class="warn" title={r}>Fehler</span>
              {:else}
                <span class="big">{n(r.images)}</span>
                {f.limit ? `von ${n(f.limit)}` : 'kein Limit'}
                {#if f.limit && r.images > f.limit}<br /><span class="warn">{n(r.images - f.limit)} über dem Limit</span>{/if}
                {#if r.videos}<br /><span class="muted">{n(r.videos)} Videos, vorerst übersprungen</span>{/if}
              {/if}
            </td>
          {/each}
        </tr>
      </tfoot>
    </table>
  </div>
{/if}

<style>
  .empty { padding: 22px 24px; }
  .empty p { margin: 0 0 10px; }
  .filterbar { display: flex; gap: 14px; align-items: center; margin-bottom: 14px; flex-wrap: wrap; }
  .filterbar input[type='search'] { border: 1px solid var(--rule); background: var(--mat); border-radius: 6px; padding: 8px 12px; width: 280px; }
  .filterbar .count { margin-left: auto; }
  .wrap { overflow: auto; max-height: calc(100vh - 230px); }
  table { border-collapse: separate; border-spacing: 0; width: 100%; }
  th, td { padding: 9px 14px; border-bottom: 1px solid var(--rule); text-align: left; background: var(--mat); }
  thead th { position: sticky; top: 0; z-index: 1; vertical-align: bottom; font-weight: 400; }
  .num { text-align: right; white-space: nowrap; color: var(--ink-2); }
  .fr { text-align: center; width: 130px; }
  .mini { width: 56px; margin: 0 auto 8px; }
  .mini.portrait { width: 34px; }
  .fname { font-family: var(--display); font-size: 16px; }
  .c { text-align: center; }
  input[type='checkbox'] { width: 18px; height: 18px; accent-color: var(--brass); cursor: pointer; }
  .filterbar input[type='checkbox'] { width: auto; height: auto; }
  tr.grp th { background: var(--wall-2); font: 12px var(--mono); text-transform: uppercase; letter-spacing: .08em; color: var(--ink-2); }
  .branch { color: var(--rule); margin-right: 6px; }
  tfoot td { position: sticky; bottom: 0; background: var(--wall-2); border-top: 2px solid var(--ink); font-size: 13px; vertical-align: top; }
  .big { font-family: var(--display); font-size: 22px; display: block; }
</style>
