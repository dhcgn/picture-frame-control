import { FrameService } from '../../bindings/github.com/dhcgn/picture-frame-control'
import type { FrameStatus } from '../../bindings/github.com/dhcgn/picture-frame-control/models'
import type { Frame } from '../../bindings/github.com/dhcgn/picture-frame-control/internal/store/models'

export type { Frame, FrameStatus }

/** Shared app state: the paired frames and what each last reported. */
export const app = $state({
  frames: [] as Frame[],
  status: {} as Record<string, FrameStatus | undefined>, // undefined while connecting
  notice: null as null | { text: string; kind: 'error' | 'ok' },
})

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
