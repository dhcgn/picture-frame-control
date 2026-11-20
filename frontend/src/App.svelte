<script lang="ts">
  import { onMount } from 'svelte'
  import { app, loadFrames, loadImmich, probeAll } from './lib/state.svelte'
  import Frames from './views/Frames.svelte'
  import FrameDetail from './views/FrameDetail.svelte'
  import Assignment from './views/Assignment.svelte'
  import Settings from './views/Settings.svelte'

  type View = 'frames' | 'detail' | 'assignment' | 'settings'
  let view = $state<View>('frames')
  let current = $state('')

  onMount(async () => {
    loadImmich()
    await loadFrames()
    probeAll()
  })

  function go(v: View) {
    if (app.dirty && view === 'assignment' && v !== 'assignment') {
      if (!confirm('Die Zuordnung hat ungespeicherte Änderungen. Verwerfen?')) return
      app.dirty = false
    }
    view = v
  }

  function open(id: string) {
    current = id
    go('detail')
  }

  const immichLine = $derived.by(() => {
    const im = app.immich
    if (im.state === 'none') return { dot: '', text: 'Immich nicht verbunden' }
    if (im.state === 'checking') return { dot: 'busy', text: 'Immich…' }
    if (im.state === 'error') return { dot: 'error', text: 'Immich-Fehler' }
    return { dot: 'local', text: `Immich ${im.info?.version}` }
  })
</script>

<div class="shell">
  <nav class="side" aria-label="Hauptnavigation">
    <div class="brand">Picture-Frame-Control<small>Immich → Frameo</small></div>
    <button class="nav" aria-current={view === 'frames' || view === 'detail' ? 'page' : undefined} onclick={() => go('frames')}>
      Frames <span class="count">{app.frames.length}</span>
    </button>
    <button class="nav" aria-current={view === 'assignment' ? 'page' : undefined} onclick={() => go('assignment')}>Zuordnung</button>
    <button class="nav" disabled title="Kommt mit Meilenstein M3">Sync <span class="count">bald</span></button>
    <button class="nav" aria-current={view === 'settings' ? 'page' : undefined} onclick={() => go('settings')}>Einstellungen</button>
    <button class="foot" onclick={() => go('settings')} title={app.immich.error || app.immich.conn.server}>
      <span class="dot {immichLine.dot}"></span><b>{immichLine.text}</b>
      {#if app.immich.conn.server}<br /><span class="host">{app.immich.conn.server.replace(/^https?:\/\//, '')}</span>{/if}
    </button>
  </nav>

  <main>
    {#if view === 'frames'}
      <Frames onopen={open} />
    {:else if view === 'detail'}
      {#key current}
        <FrameDetail id={current} onback={() => go('frames')} onassign={() => go('assignment')} />
      {/key}
    {:else if view === 'assignment'}
      <Assignment onsettings={() => go('settings')} onframes={() => go('frames')} />
    {:else}
      <Settings />
    {/if}
  </main>
</div>

{#if app.notice}
  <div class="notice {app.notice.kind}" role={app.notice.kind === 'error' ? 'alert' : 'status'}>
    <span>{app.notice.text}</span>
    <button aria-label="Hinweis schließen" onclick={() => (app.notice = null)}>×</button>
  </div>
{/if}

<style>
  .shell { display: grid; grid-template-columns: 228px 1fr; min-height: 100vh; }
  .side {
    background: var(--side); color: var(--side-ink); padding: 22px 14px;
    display: flex; flex-direction: column; gap: 4px; position: sticky; top: 0; height: 100vh;
    box-shadow: inset -6px 0 10px -6px rgb(0 0 0 / .6);
  }
  .brand { font-family: var(--display); font-size: 21px; line-height: 1.1; padding: 0 10px 22px; }
  .brand small { display: block; font: 12px var(--mono); color: var(--side-ink-2); margin-top: 6px; }
  .nav { all: unset; cursor: pointer; display: flex; align-items: center; gap: 10px; padding: 9px 10px; border-radius: 6px; color: var(--side-ink-2); }
  .nav:hover:not(:disabled) { color: var(--side-ink); background: rgb(255 255 255 / .05); }
  .nav:focus-visible, .foot:focus-visible { outline: 2px solid var(--brass); }
  .nav[aria-current='page'] { color: var(--side-ink); background: rgb(255 255 255 / .08); box-shadow: inset 3px 0 0 var(--brass); }
  .nav:disabled { cursor: default; opacity: .5; }
  .count { margin-left: auto; font: 12px var(--mono); color: var(--side-ink-2); }
  .foot { all: unset; cursor: pointer; margin-top: auto; font-size: 13px; color: var(--side-ink-2); padding: 12px 10px 0; border-top: 1px solid rgb(255 255 255 / .1); }
  .foot b { color: var(--side-ink); font-weight: 400; }
  .foot .host { font: 12px var(--mono); word-break: break-all; }
  .foot :global(.dot.error) { background: var(--del); }
  main { padding: 30px 40px 80px; min-width: 0; }

  .notice {
    position: fixed; left: 50%; bottom: 20px; transform: translateX(-50%); z-index: 10;
    display: flex; gap: 14px; align-items: center; max-width: min(640px, 90vw);
    padding: 12px 14px 12px 18px; border-radius: 8px; background: var(--ink); color: var(--mat);
    box-shadow: 0 14px 30px -12px rgb(0 0 0 / .5);
  }
  .notice.error { background: var(--del); color: #fff; }
  .notice button { all: unset; cursor: pointer; font-size: 20px; line-height: 1; padding: 0 4px; }
</style>
