<script lang="ts">
  import { onMount } from 'svelte'
  import { SvelteSet } from 'svelte/reactivity'
  import { SyncService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import FrameArt from '../lib/FrameArt.svelte'
  import { app, errText, isPortrait, nameOf, notify, planSync, probe, type FramePlan } from '../lib/state.svelte'

  let { onassign }: { onassign: () => void } = $props()

  const sync = app.sync
  // Per frame: take part in the next run, and what to do with foreign photos.
  let include = $state<Record<string, boolean>>({})
  let foreign = $state<Record<string, 'keep' | 'all' | 'pick'>>({})
  let picked = $state<Record<string, SvelteSet<string>>>({})

  onMount(() => {
    if (!sync.running && app.immich.state !== 'none') planSync()
  })

  // New plans: default every frame that can sync to taking part. This must
  // run before rendering: a bind:checked on an undefined value writes the
  // checkbox's own false back, and ??= would then keep it.
  $effect.pre(() => {
    for (const p of sync.plans ?? []) {
      include[p.frameId] ??= !p.error
      foreign[p.frameId] ??= 'keep'
      picked[p.frameId] ??= new SvelteSet()
    }
  })

  const frameOf = (id: string) => app.frames.find((f) => f.id === id)
  const chosen = $derived((sync.plans ?? []).filter((p) => !p.error && include[p.frameId]))
  const foreignToDelete = (p: FramePlan) =>
    foreign[p.frameId] === 'all' ? (p.foreign ?? []) : foreign[p.frameId] === 'pick' ? [...(picked[p.frameId] ?? [])] : []
  const totals = $derived(
    chosen.reduce(
      (t, p) => ({ send: t.send + p.send, del: t.del + (p.canDelete ? p.delete + foreignToDelete(p).length : 0) }),
      { send: 0, del: 0 },
    ),
  )

  async function start() {
    if (totals.del > 0 && !confirm(`${totals.del} Bilder werden endgültig von den Frames gelöscht. Sync starten?`)) return
    sync.running = true
    sync.results = null
    sync.log = []
    sync.progress = {}
    try {
      sync.results = (await SyncService.Run(chosen.map((p) => ({ frameId: p.frameId, deleteForeign: foreignToDelete(p) })))) ?? []
      const sent = sync.results.reduce((n, r) => n + r.sent, 0)
      const failed = sync.results.reduce((n, r) => n + r.failed + (r.error ? 1 : 0), 0)
      notify(failed ? `Sync beendet mit ${failed} Fehlern. ${sent} Bilder gesendet.` : `Sync fertig. ${sent} Bilder gesendet.`, failed ? 'error' : 'ok')
    } catch (e) {
      notify(errText(e), 'error')
    } finally {
      sync.running = false
      for (const p of chosen) probe(p.frameId)
      for (const id of Object.keys(foreign)) foreign[id] = 'keep'
      planSync()
    }
  }

  const n = (x: number) => x.toLocaleString('de-DE')
  const phaseText: Record<string, string> = {
    prepare: 'Bereite vor…', delete: 'Lösche…', send: 'Sende', check: 'Prüfe…', done: 'Fertig',
  }
</script>

<div class="head">
  <div>
    <h1>Sync</h1>
    <p>
      {#if sync.running}Sync läuft. Der Stand wird nach jedem Bild gespeichert, Abbrechen ist jederzeit möglich.
      {:else}Vorschau: Das passiert beim nächsten Sync. Noch wurde nichts geändert.{/if}
    </p>
  </div>
</div>

{#if app.immich.state === 'none'}
  <div class="panel empty"><p><strong>Immich ist noch nicht verbunden.</strong></p><p class="muted">Server und API-Key in den Einstellungen eintragen.</p></div>
{:else if app.frames.length === 0}
  <div class="panel empty"><p><strong>Noch kein Frame gekoppelt.</strong></p></div>
{:else if !sync.plans}
  <p class="muted">{sync.planning ? 'Frage Immich und die Frames ab…' : ''}</p>
{:else}
  <div class="plans">
    {#each sync.plans as p (p.frameId)}
      {@const f = frameOf(p.frameId)}
      {@const prog = sync.progress[p.frameId]}
      {@const fd = foreignToDelete(p)}
      {#if f}
        <article class="plan panel" class:off={!!p.error}>
          <div class="art" class:portrait={isPortrait(f)}><FrameArt frame={f} mini /></div>
          <h3>{nameOf(f)}</h3>
          {#if sync.running}
            <span class="mono muted">{prog ? phaseText[prog.phase] : include[p.frameId] && !p.error ? 'Wartet' : ''}</span>
          {:else}
            <label class="pick"><input type="checkbox" bind:checked={include[p.frameId]} disabled={!!p.error} /> Mitsyncen</label>
          {/if}

          {#if p.error}
            <div class="note warn">{p.error}</div>
          {:else if sync.running && prog}
            <div class="prog">
              <div class="track"><span style:width={prog.total ? `${(100 * prog.done) / prog.total}%` : prog.phase === 'done' ? '100%' : '0'}></span></div>
              {#if prog.phase === 'send'}<div class="mono">{prog.done + 1} von {prog.total} · {prog.item}</div>{/if}
            </div>
          {:else}
            {@const del = p.canDelete ? p.delete + fd.length : 0}
            <div class="bar">
              <span style:flex={p.keep} style:background="var(--keep)"></span>
              <span style:flex={p.send} style:background="var(--send)"></span>
              <span style:flex={del} style:background="var(--del)"></span>
              <span style:flex={(p.foreign?.length ?? 0) - fd.length} style:background="var(--foreign)"></span>
            </div>
            <div class="facts">
              <span><i style:background="var(--keep)"></i>Bleibt <b>{n(p.keep)}</b></span>
              <span><i style:background="var(--send)"></i>Senden <b>{n(p.send)}</b></span>
              <span><i style:background="var(--del)"></i>Löschen <b>{n(del)}</b>{#if p.delete && p.canDelete}<span class="muted"> nicht mehr ausgewählt oder geändert</span>{/if}</span>
              {#if p.foreign?.length}<span><i style:background="var(--foreign)"></i>Fremd <b>{n(p.foreign.length)}</b></span>{/if}
              {#if p.skipped}<span><i style:background="var(--skip)"></i>Übersprungen <b>{n(p.skipped)}</b><span class="muted"> Videos</span></span>{/if}
              {#if p.overLimit}<span class="warn">Über dem Limit <b>{n(p.overLimit)}</b><span class="muted"> ältere werden nicht gesendet</span></span>{/if}
            </div>
            {#if !f.sources.albums?.length && !f.sources.tags?.length}
              <div class="note">Diesem Frame sind keine Alben oder Tags zugeordnet. <button class="link" onclick={onassign}>Zuordnen</button></div>
            {:else if p.send === 0 && del === 0}
              <div class="note ok">Alles aktuell. Auf diesem Frame gibt es nichts zu senden oder zu löschen.</div>
            {/if}
            {#if p.delete && !p.canDelete}
              <div class="note warn">{p.delete} Bilder müssten gelöscht werden, aber der App fehlt die Berechtigung „Verwalten“.</div>
            {/if}
            {#if p.foreign?.length}
              <div class="decide">
                <h4>{n(p.foreign.length)} Bilder auf dem Frame stammen nicht aus dieser App</h4>
                <span class="muted">Zum Beispiel per Frameo-App vom Handy gesendet.</span>
                {#if foreign[p.frameId] === 'pick'}
                  <div class="strip">
                    {#each p.foreign as m (m)}
                      <button class="th" aria-pressed={picked[p.frameId]?.has(m)} aria-label="Bild zum Löschen wählen"
                        onclick={() => (picked[p.frameId].has(m) ? picked[p.frameId].delete(m) : picked[p.frameId].add(m))}>
                        <img src={`/thumb/${p.frameId}/${m}`} alt="" loading="lazy" />
                      </button>
                    {/each}
                  </div>
                {/if}
                <div class="choices">
                  <label><input type="radio" bind:group={foreign[p.frameId]} value="keep" /> Behalten</label>
                  <label><input type="radio" bind:group={foreign[p.frameId]} value="all" disabled={!p.canDelete} /> Alle {n(p.foreign.length)} löschen</label>
                  <label><input type="radio" bind:group={foreign[p.frameId]} value="pick" disabled={!p.canDelete} /> Einzeln auswählen{foreign[p.frameId] === 'pick' ? ` (${picked[p.frameId]?.size ?? 0})` : '…'}</label>
                </div>
              </div>
            {/if}
          {/if}
          {#each sync.results?.filter((r) => r.frameId === p.frameId) ?? [] as r (r.frameId)}
            <div class="note" class:warn={!!r.error || r.failed > 0}>
              Letzter Sync: {r.sent} gesendet, {r.deleted} gelöscht{#if r.failed}, {r.failed} fehlgeschlagen{/if}{#if r.full}, Frame voll{/if}{#if r.cancelled}, abgebrochen{/if}{#if r.error}. {r.error}{/if}.
            </div>
          {/each}
        </article>
      {/if}
    {/each}
  </div>

  {#if sync.log.length}
    <div class="log panel" aria-label="Protokoll">
      {#each sync.log as l, i (i)}
{@const lf = frameOf(l.frame)}
        <div class={l.level}>{l.time} {lf ? nameOf(lf) : ''} · {l.text}</div>
      {/each}
    </div>
  {/if}

  <div class="go">
    {#if sync.running}
      <span class="sum"><b>Sync läuft</b></span>
      <button class="btn danger" onclick={() => SyncService.Cancel()}>Sync abbrechen</button>
    {:else}
      <span class="sum"><b>{chosen.length === 1 ? '1 Frame' : `${chosen.length} Frames`}</b> · {n(totals.send)} senden · {n(totals.del)} löschen</span>
      <button class="btn" onclick={planSync} disabled={sync.planning}>{sync.planning ? 'Berechne…' : 'Vorschau aktualisieren'}</button>
      <button class="btn primary" onclick={start} disabled={sync.planning || chosen.length === 0 || totals.send + totals.del === 0}>Sync starten</button>
    {/if}
  </div>
{/if}

<style>
  .empty { padding: 22px 24px; }
  .empty p { margin: 0 0 10px; }
  .plans { display: grid; gap: 18px; padding-bottom: 70px; }
  .plan { padding: 20px 22px; display: grid; grid-template-columns: 70px 1fr auto; gap: 8px 20px; align-items: start; }
  .plan.off { opacity: .7; }
  .art { width: 70px; grid-row: span 6; }
  .art.portrait { width: 46px; justify-self: center; }
  h3 { font-family: var(--display); font-weight: 400; font-size: 22px; margin: 0; }
  .pick { display: flex; gap: 8px; align-items: center; font-size: 14px; }
  .pick input { width: 18px; height: 18px; accent-color: var(--brass); }
  .bar { display: flex; height: 12px; border-radius: 3px; overflow: hidden; background: var(--wall-2); grid-column: 2 / 4; }
  .facts { grid-column: 2 / 4; display: flex; flex-wrap: wrap; gap: 6px 22px; font-size: 14px; }
  .facts b { font-family: var(--mono); font-weight: 500; }
  .facts i { display: inline-block; width: 10px; height: 10px; border-radius: 2px; margin-right: 6px; vertical-align: -1px; }
  .note { grid-column: 2 / 4; font-size: 14px; padding: 10px 12px; border-radius: 6px; background: var(--wall); }
  .decide { grid-column: 2 / 4; border: 1px solid var(--foreign); border-radius: 8px; padding: 14px 16px; }
  .decide h4 { margin: 0 0 4px; font-size: 15px; }
  .strip { display: flex; gap: 4px; margin: 12px 0; flex-wrap: wrap; max-height: 180px; overflow: auto; }
  .th { all: unset; cursor: pointer; width: 72px; height: 45px; border-radius: 2px; overflow: hidden; background: var(--wall-2); position: relative; }
  .th img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .th[aria-pressed='true'] { outline: 3px solid var(--del); outline-offset: -3px; }
  .th[aria-pressed='true'] img { opacity: .6; }
  .th:focus-visible { outline: 2px solid var(--brass); }
  .choices { display: flex; gap: 18px; flex-wrap: wrap; margin-top: 10px; font-size: 14px; }
  .choices label { display: inline-flex; gap: 6px; align-items: center; cursor: pointer; }
  .choices input { accent-color: var(--foreign); }
  .prog { grid-column: 2 / 4; }
  .track { height: 8px; background: var(--wall-2); border-radius: 4px; overflow: hidden; margin: 6px 0; }
  .track span { display: block; height: 100%; background: var(--send); transition: width .3s; }
  .log { margin: 22px 0 70px; padding: 14px 16px; font: 12.5px/1.7 var(--mono); max-height: 260px; overflow: auto; }
  .log .warn { color: var(--skip); }
  .log .error { color: var(--del); }
  .go { position: fixed; left: 228px; right: 0; bottom: 0; display: flex; gap: 12px; align-items: center; padding: 14px 40px; background: var(--mat); border-top: 1px solid var(--rule); }
  .sum { margin-right: auto; }
  .link { all: unset; cursor: pointer; text-decoration: underline; }
</style>
