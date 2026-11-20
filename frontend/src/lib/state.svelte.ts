import { FrameService, ImmichService } from '../../bindings/github.com/dhcgn/picture-frame-control'
import type { Connection, FrameStatus, ServerInfo, Source } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
import type { Frame } from '../../bindings/github.com/dhcgn/picture-frame-control/internal/store/models'

export type { Frame, FrameStatus, Source }

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
})

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
  if (!s) return { dot: 'busy', text: 'Verbinde…' }
  if (!s.online) return { dot: '', text: 'Nicht erreichbar' }
  return s.via === 'local' ? { dot: 'local', text: 'Lokal verbunden' } : { dot: 'relay', text: 'Über Relay verbunden' }
}
