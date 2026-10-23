<script lang="ts">
  import { sessions as api } from "../lib/client";
  import { features } from "../lib/features.svelte";
  import { live } from "../lib/live.svelte";

  let { onpick }: { onpick: (id: string) => void } = $props();

  let text = $state("");
  let at = $state(0);
  let loading = $state(false);
  let error = $state("");
  let windowMin = $state(60);

  async function run() {
    loading = true;
    error = "";
    try {
      const r = await api.overview({ windowMinutes: windowMin });
      text = r.text;
      at = Date.now();
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  // Link "NAME:" prefixes back to sessions.
  const lines = $derived(
    text
      .split("\n")
      .map((l) => l.trim())
      .filter(Boolean)
      .map((l) => {
        const m = /^([A-Z]\d{1,2})\s*:\s*(.*)$/.exec(l);
        const s = m ? live.sessions.find((x) => x.name === m[1]) : undefined;
        return { name: m?.[1], id: s?.id, body: m ? m[2] : l };
      }),
  );
</script>

<div class="ov">
  <div class="bar">
    <select bind:value={windowMin} aria-label="Time window">
      <option value={15}>Last 15 minutes</option>
      <option value={60}>Last hour</option>
      <option value={240}>Last 4 hours</option>
      <option value={1440}>Last day</option>
    </select>
    <button class="go" onclick={run} disabled={loading}>{loading ? "Summarizing" : text ? "Refresh" : "Summarize"}</button>
  </div>
  {#if error}
    <p class="err">{error}</p>
  {:else if lines.length}
    <ul>
      {#each lines as l, i (i)}
        <li>
          {#if l.id}
            <button class="name" onclick={() => onpick(l.id!)}>{l.name}</button>
          {:else if l.name}
            <span class="name">{l.name}</span>
          {/if}
          <span>{l.body}</span>
        </li>
      {/each}
    </ul>
    <p class="meta">{features.summaryModel}, {new Date(at).toLocaleTimeString()}</p>
  {:else if !loading}
    <p class="hint">Get a short digest of what every session did in the chosen window.</p>
  {/if}
</div>

<style>
  .ov {
    padding: 0.75rem 1rem;
    border-bottom: 1px solid var(--line);
    font-size: var(--s-1);
  }
  .bar {
    display: flex;
    gap: 0.5rem;
  }
  select {
    flex: 1;
    background: var(--ink);
    color: var(--fg);
    border: 1px solid var(--line);
    border-radius: 6px;
    padding: 0.25rem 0.4rem;
    font: inherit;
  }
  .go {
    padding: 0.25rem 0.75rem;
    border-radius: 6px;
    background: var(--fg);
    color: var(--ink);
    font-weight: 500;
  }
  .go:disabled {
    opacity: 0.5;
  }
  ul {
    list-style: none;
    margin: 0.625rem 0 0;
    padding: 0;
    display: grid;
    gap: 0.375rem;
  }
  li {
    display: flex;
    gap: 0.5rem;
    color: var(--fg-2);
    line-height: 1.4;
  }
  .name {
    flex: none;
    min-width: 1.75rem;
    font-weight: 600;
    color: var(--fg);
    text-align: left;
  }
  button.name:hover {
    color: var(--focus);
  }
  .meta,
  .hint {
    margin: 0.5rem 0 0;
    color: var(--fg-3);
  }
  .err {
    margin: 0.5rem 0 0;
    color: var(--failed);
  }
</style>
