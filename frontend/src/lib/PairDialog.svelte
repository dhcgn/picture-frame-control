<script lang="ts">
  import { FrameService } from '../../bindings/github.com/dhcgn/picture-frame-control'
  import { errText, type Frame } from './state.svelte'

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
      <h2 id="pair-title">Frame koppeln</h2>
      <p class="muted">Am Frame „Freund hinzufügen“ wählen und den angezeigten Code hier eingeben.</p>
      <label for="pair-code">Code</label>
      <input id="pair-code" class="code" inputmode="numeric" autocomplete="off" required bind:value={code} disabled={busy} />
      <label for="pair-name">Name <span class="muted" style="font-weight:400">(optional)</span></label>
      <input id="pair-name" placeholder="Sonst der Standort, den der Frame meldet" bind:value={name} disabled={busy} />
      {#if busy}
        <p class="hint">Kopple… Das dauert einige Sekunden.</p>
      {:else if error}
        <p class="hint warn">{error}</p>
      {:else}
        <p class="hint">Danach übernimmt die App Auflösung und Berechtigungen vom Frame.</p>
      {/if}
    </div>
    <footer>
      <button type="button" class="btn" onclick={() => dlg.close()} disabled={busy}>Abbrechen</button>
      <button class="btn primary" disabled={busy || !code.trim()}>Koppeln</button>
    </footer>
  </form>
</dialog>
