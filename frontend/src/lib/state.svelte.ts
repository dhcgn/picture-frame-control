import { Events } from '@wailsio/runtime'
import { FrameService, ImmichService, SyncService, UpdateService } from '../../bindings/github.com/dhcgn/picture-frame-control'
import type {
  Connection, FramePlan, FrameResult, FrameStatus, ServerInfo, Source, SyncLog, SyncProgress, UpdateInfo,
} from '../../bindings/github.com/dhcgn/picture-frame-control/models'
import type { Frame } from '../../bindings/github.com/dhcgn/picture-frame-control/internal/store/models'
import { t } from './i18n.svelte'

export type { Frame, FramePlan, FrameStatus, Source }

/** Shared app state. */
export const app = $state({
  frames: [] as Frame[],
  status: {} as Record<string, FrameStatus | undefined>, // undefined while connecting
  notice: null as null | { text: string; kind: 'error' | 'ok' },
  immich: {
    state: 'checking' as 'checking' | 'none' | 'ok' | 'error',
    conn: { server: '', hasKey: false } as Connection,
    info: null as ServerInfo | null,
    error: '',
  },
  sources: null as Source[] | null, // albums and tags, loaded once per session
  sourcesLoading: false,
  sourcesError: '',
  dirty: false, // unsaved edits in the assignment matrix
  update: null as UpdateInfo | null,
  // Kept here, not in the view, so a running sync survives switching views.
  sync: {
    plans: null as FramePlan[] | null,
    planning: false,
    running: false,
    progress: {} as Record<string, SyncProgress>,
    log: [] as SyncLog[],
    results: null as FrameResult[] | null,
  },
})

/** Shows the running version at once, then asks GitHub for a newer one. */
export async function checkUpdate() {
  app.update ??= await UpdateService.Version()
  app.update = await UpdateService.Check()
}

/** Opens the update window; it downloads, verifies and restarts. */
export async function installUpdate() {
  try {
    await UpdateService.Install()
  } catch (e) {
    notify(errText(e), 'error')
  }
}

/** Subscribes to the sync events; call once. */
export function listenSync() {
  Events.On('sync:progress', (ev) => {
    const p = ev.data as SyncProgress
    app.sync.progress[p.frame] = p
  })
  Events.On('sync:log', (ev) => {
    app.sync.log.push(ev.data as SyncLog)
  })
}

export async function planSync() {
  app.sync.planning = true
  try {
    app.sync.plans = (await SyncService.Preview(app.frames.map((f) => f.id))) ?? []
  } catch (e) {
    notify(errText(e), 'error')
  } finally {
    app.sync.planning = false
  }
}

export async function loadImmich() {
  app.immich.conn = await ImmichService.Connection()
  if (!app.immich.conn.server) {
    app.immich.state = 'none'
    return
  }
  app.immich.state = 'checking'
  try {
    app.immich.info = await ImmichService.Status()
    app.immich.state = 'ok'
    app.immich.error = ''
  } catch (e) {
    app.immich.state = 'error'
    app.immich.error = errText(e)
  }
}

export async function loadSources(force = false) {
  if ((app.sources && !force) || app.sourcesLoading) return
  app.sourcesLoading = true
  app.sourcesError = ''
  try {
    app.sources = (await ImmichService.Sources()) ?? []
  } catch (e) {
    app.sourcesError = errText(e)
  } finally {
    app.sourcesLoading = false
  }
}

/**
 * The user's name for a frame, else the place it reports, else its own name
 * (in Frameo, the owner's), else one from its id. Same order as Go's
 * store.Frame.DisplayName.
 */
export function nameOf(f: Frame): string {
  return f.name || f.place || f.frameName || `Frame ${f.id.slice(0, 6)}`
}

/** What the frame reports about itself that the title does not already say. */
export function aboutOf(f: Frame): string {
  const title = nameOf(f)
  return [
    f.place && f.place !== title ? t(`Standort ${f.place}`, `Location ${f.place}`) : '',
    f.frameName && f.frameName !== title ? t(`Besitzer ${f.frameName}`, `Owner ${f.frameName}`) : '',
  ]
    .filter(Boolean)
    .join(' · ')
}

/** Album and tag ids assigned to a frame. */
export function sourceIds(f: Frame): string[] {
  return [...(f.sources.albums ?? []), ...(f.sources.tags ?? [])]
}

let noticeTimer: ReturnType<typeof setTimeout>
export function notify(text: string, kind: 'error' | 'ok' = 'ok') {
  app.notice = { text, kind }
  clearTimeout(noticeTimer)
  noticeTimer = setTimeout(() => (app.notice = null), kind === 'error' ? 9000 : 4000)
}

/** Message of an error thrown by a Go call. */
export function errText(e: unknown): string {
  if (e && typeof e === 'object' && 'message' in e) return String((e as Error).message)
  return String(e)
}

export async function loadFrames() {
  app.frames = (await FrameService.List()) ?? []
}

export async function probe(id: string) {
  app.status[id] = undefined
  try {
    app.status[id] = await FrameService.Probe(id)
    await loadFrames() // Probe may have learned the screen size
  } catch (e) {
    app.status[id] = { online: false, via: '', error: errText(e), canView: false, canManage: false, count: -1, recent: null }
  }
}

export function probeAll() {
  return Promise.all(app.frames.map((f) => probe(f.id)))
}

/** Screen size as the frame stands: rotated frames swap width and height. */
export function screenOf(f: Frame) {
  const { w, h } = f.screen
  return f.rotate ? { w: h, h: w } : { w, h }
}

export function isPortrait(f: Frame) {
  const s = screenOf(f)
  return s.h > s.w
}

export function statusLine(s: FrameStatus | undefined): { dot: string; text: string } {
  if (!s) return { dot: 'busy', text: t('Verbinde…', 'Connecting…') }
  if (!s.online) return { dot: '', text: t('Nicht erreichbar', 'Not reachable') }
  return s.via === 'local'
    ? { dot: 'local', text: t('Lokal verbunden', 'Connected locally') }
    : { dot: 'relay', text: t('Über Relay verbunden', 'Connected via relay') }
}
