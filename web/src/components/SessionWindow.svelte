<script lang="ts">
  import { live } from "../lib/live.svelte";
  import Pane from "./Pane.svelte";

  let { id }: { id: string } = $props();

  // Sub-sessions render as subtabs. Clicking the active one returns to the main session.
  let view = $state("");
  const children = $derived(live.sessions.filter(child => child.parentSessionId === id));
  const shown = $derived(view && children.some(c => c.id === view) ? view : id);
  const main = $derived(live.byId(id));

  function pick(child: string) {
    view = view === child ? "" : child;
  }
</script>

<div class="window">
  {#if children.length}
    <div class="subtabs" role="tablist">
      <button role="tab" aria-selected={shown === id} class:active={shown === id} onclick={() => (view = "")}>{main?.name ?? "main"}</button>
      {#each children as child (child.id)}
        <button role="tab" aria-selected={shown === child.id} class:active={shown === child.id} title={child.shortSummary || child.detail || child.model} onclick={() => pick(child.id)}>
          <span class="dot" class:busy={child.isProcessing}></span>{child.name}
        </button>
      {/each}
    </div>
  {/if}
  {#key shown}
    <Pane id={shown} />
  {/key}
</div>

<style>
  .window { display: flex; flex-direction: column; height: 100%; min-height: 0; min-width: 0; flex: 1; }
  .window > :global(:last-child) { flex: 1; min-height: 0; }
  .subtabs { display: flex; gap: 0.25rem; padding: 0.25rem 0.5rem; border-bottom: 1px solid var(--line); overflow-x: auto; font-size: var(--s-1); flex: none; }
  .subtabs button { display: flex; align-items: center; gap: 0.3rem; padding: 0.15rem 0.5rem; border-radius: 4px; color: var(--fg-2); white-space: nowrap; }
  .subtabs button:hover { background: var(--ink-3); }
  .subtabs button.active { background: var(--ink-3); color: var(--fg); font-weight: 600; }
  .dot { width: 0.45rem; height: 0.45rem; border-radius: 50%; background: var(--fg-3, #777); }
  .dot.busy { background: var(--accent, #4c8); }
</style>
