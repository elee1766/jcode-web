<script lang="ts">
  import { tick } from "svelte";
  import { live } from "../lib/live.svelte";
  import { ago } from "../lib/format";
  import { features } from "../lib/features.svelte";
  import Overview from "./Overview.svelte";

  let { onclose, onpick }: { onclose: () => void; onpick: (id: string) => void } = $props();

  let only = $state<"all" | "status" | "files">("all");
  let box: HTMLElement;

  const shown = $derived(
    live.events.filter((e) =>
      only === "all" ? true : only === "status" ? e.type === "status_change" || e.type === "member_change" : e.type === "file_touch",
    ),
  );

  let first = true;
  $effect(() => {
    void shown.length;
    tick().then(() => {
      if (!box) return;
      if (first || box.scrollHeight - box.scrollTop - box.clientHeight < 120) box.scrollTop = box.scrollHeight;
      first = false;
    });
  });
</script>

<div class="feed">
  <header>
    <span class="title">Activity</span>
    <div class="filters" role="radiogroup">
      <button class:on={only === "all"} onclick={() => (only = "all")}>All</button>
      <button class:on={only === "status"} onclick={() => (only = "status")}>Status</button>
      <button class:on={only === "files"} onclick={() => (only = "files")}>Files</button>
    </div>
    <button class="x" onclick={onclose} aria-label="Close activity">✕</button>
  </header>
  {#if features.summaries}<Overview {onpick} />{/if}
  <ol bind:this={box}>
    {#each shown as e (e.id)}
      <li data-type={e.type}>
        <button onclick={() => onpick(e.sessionId)}>
          <span class="who">{e.sessionName || e.sessionId.slice(8, 11)}</span>
          <span class="what">{e.summary}</span>
          <span class="when">{ago(e.timestampUnix)}</span>
        </button>
      </li>
    {:else}
      <li class="none">No activity yet. Edits, status changes and swarm messages from every session show up here.</li>
    {/each}
  </ol>
</div>

<style>
  .feed {
    height: 100%;
    display: flex;
    flex-direction: column;
  }
  header {
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--line);
  }
  .title {
    font-weight: 600;
  }
  .filters {
    display: flex;
    gap: 0.25rem;
    flex: 1;
  }
  .filters button {
    padding: 0.15rem 0.5rem;
    border-radius: 4px;
    font-size: var(--s-1);
    color: var(--fg-3);
  }
  .filters button.on {
    background: var(--ink-3);
    color: var(--fg);
  }
  .x {
    color: var(--fg-3);
  }
  ol {
    list-style: none;
    margin: 0;
    padding: 0.25rem 0;
    overflow-y: auto;
    flex: 1;
  }
  li button {
    display: grid;
    grid-template-columns: 2.5rem 1fr auto;
    gap: 0.5rem;
    width: 100%;
    padding: 0.25rem 1rem;
    text-align: left;
    font-size: var(--s-1);
  }
  li button:hover {
    background: var(--ink-3);
  }
  .who {
    font-weight: 600;
    color: var(--fg-2);
  }
  .what {
    color: var(--fg-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  [data-type="status_change"] .what {
    color: var(--fg);
  }
  .when {
    color: var(--fg-3);
    font-variant-numeric: tabular-nums;
  }
  .none {
    padding: 1rem;
    color: var(--fg-3);
    font-size: var(--s-1);
  }
</style>
