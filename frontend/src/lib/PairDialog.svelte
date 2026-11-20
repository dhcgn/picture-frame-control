<script lang="ts">
  import { FrameService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import { errText, type Frame } from './state.svelte'
  import { t } from './i18n.svelte'

  let { onpaired }: { onpaired: (f: Frame) => void } = $props()

  let dlg: HTMLDialogElement
  let code = $state('')
  let name = $state('')
  let busy = $state(false)
  let error = $state('')

  export function open(hint = '') {
    code = ''
    name = hint
    error = ''
    dlg.showModal()
  }

  async function pair(e: SubmitEvent) {
    e.preventDefault()
    busy = true
    error = ''
    try {
      const f = await FrameService.Pair(code, name)
      dlg.close()
      onpaired(f)
    } catch (err) {
      error = errText(err)
    } finally {
      busy = false
    }
  }
</script>

<dialog bind:this={dlg} aria-labelledby="pair-title">
  <form onsubmit={pair}>
    <div class="in">
      <h2 id="pair-title">{t('Frame koppeln', 'Pair a frame')}</h2>
      <p class="muted">{t('Am Frame „Freund hinzufügen“ wählen und den angezeigten Code hier eingeben.', 'On the frame, choose “Add friend” and enter the code it shows here.')}</p>
      <label for="pair-code">Code</label>
      <input id="pair-code" class="code" inputmode="numeric" autocomplete="off" required bind:value={code} disabled={busy} />
      <label for="pair-name">Name <span class="muted" style="font-weight:400">(optional)</span></label>
      <input id="pair-name" placeholder={t('Sonst der Standort, den der Frame meldet', 'Otherwise the location the frame reports')} bind:value={name} disabled={busy} />
      {#if busy}
        <p class="hint">{t('Kopple… Das dauert einige Sekunden.', 'Pairing… This takes a few seconds.')}</p>
      {:else if error}
        <p class="hint warn">{error}</p>
      {:else}
        <p class="hint">{t('Danach übernimmt die App Auflösung und Berechtigungen vom Frame.', 'The app then takes the resolution and permissions from the frame.')}</p>
      {/if}
    </div>
    <footer>
      <button type="button" class="btn" onclick={() => dlg.close()} disabled={busy}>{t('Abbrechen', 'Cancel')}</button>
      <button class="btn primary" disabled={busy || !code.trim()}>{t('Koppeln', 'Pair')}</button>
    </footer>
  </form>
</dialog>
