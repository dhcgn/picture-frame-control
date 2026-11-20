<script lang="ts">
  // The signature element: a frame drawn as a walnut moulding and a mat, in
  // the frame's real orientation, showing its newest photos.
  import { isPortrait, type Frame } from './state.svelte'

  let { frame, recent = [], dim = false, mini = false }: {
    frame: Frame
    recent?: string[] | null
    dim?: boolean
    mini?: boolean
  } = $props()

  const slots = $derived(Array.from({ length: mini ? 1 : 5 }, (_, i) => recent?.[i]))
</script>

<div class="frame" class:portrait={isPortrait(frame)} class:dim class:mini>
  <div class="mat">
    <div class="pic">
      {#each slots as id, i (i)}
        {#if id}
          <img src={`/thumb/${frame.id}/${id}`} alt="" loading="lazy" />
        {:else}
          <div class="ph" style:--h={(frame.id.charCodeAt(i) * 37 + i * 67) % 360}></div>
        {/if}
      {/each}
    </div>
  </div>
</div>

<style>
  .frame { padding: 9px; border-radius: 2px; box-shadow: var(--shadow); background: linear-gradient(145deg, var(--wood-hi), var(--wood-lo) 70%); }
  .mat { background: #fbfbf8; padding: 7%; box-shadow: inset 0 1px 4px rgb(0 0 0 / .35); }
  .pic { aspect-ratio: 16 / 10; display: grid; grid-template: 1fr 1fr / 2fr 1fr 1fr; gap: 2px; box-shadow: 0 0 0 1px rgb(0 0 0 / .2); overflow: hidden; background: #ddd; }
  .portrait .pic { aspect-ratio: 10 / 16; grid-template: 2fr 1fr 1fr / 1fr 1fr; }
  .pic > :first-child { grid-row: span 2; }
  .portrait .pic > :first-child { grid-column: span 2; grid-row: auto; }
  .pic img, .ph { width: 100%; height: 100%; object-fit: cover; display: block; min-height: 0; }
  .ph { background: linear-gradient(135deg, hsl(var(--h) 18% 74%), hsl(calc(var(--h) + 35) 16% 52%)); }
  .dim .pic { filter: grayscale(1) brightness(.85); }
  .mini { padding: 3px; box-shadow: none; }
  .mini .mat { padding: 3px; }
  .mini .pic { display: block; }
</style>
