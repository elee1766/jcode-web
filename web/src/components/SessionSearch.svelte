<script lang="ts">
  import { sessions as api } from "../lib/client";
  let { id, onclose }: { id: string; onclose: () => void } = $props();
  let query = $state("");
  let result = $state<Awaited<ReturnType<typeof api.searchSessions>>>();
  let error = $state("");
  let loading = $state(false);
  $effect(() => {
    const needle = query.trim();
    const sessionId = id;
    const controller = new AbortController();
    result = undefined;
    error = "";
    loading = false;
    if (Array.from(needle).length < 3) return;
    loading = true;
    let timer: ReturnType<typeof setTimeout>;
    async function search() {
      try {
        const response = await api.searchSessions({ query: needle, sessionId, limit: 1 }, { signal: controller.signal, timeoutMs: 5000 });
        if (!controller.signal.aborted) {
          result = response;
          if (response.status?.indexing) timer = setTimeout(search, 3000);
        }
      } catch (e) {
        if (!controller.signal.aborted) error = e instanceof Error ? e.message : String(e);
      } finally { if (!controller.signal.aborted) loading = false; }
    }
    timer = setTimeout(search, 200);
    return () => { clearTimeout(timer); controller.abort(); };
  });
</script>

<div class="session-search" role="search" aria-label="Search this session">
  <div class="controls">
    <input aria-label="Search session history" placeholder="Search full saved history (3+ characters)" bind:value={query} use:focus onkeydown={(e) => { if (e.key === "Escape") onclose(); }} />
    <button onclick={onclose} aria-label="Close session search">✕</button>
  </div>
  {#if loading}<p aria-live="polite">Searching…</p>{/if}
  {#if error || result?.status?.error}<p role="alert">{error || result?.status?.error}</p>{/if}
  {#if result}
    {#if !result.results.length}<p>No matches{result.status?.indexing ? " indexed yet. Indexing is still running." : "."}</p>{/if}
    {#each result.results as match}
      {#each match.excerpts as excerpt}
        <div class="match"><small>{excerpt.role} · Message {excerpt.messageIndex + 1}</small><pre>{excerpt.text}</pre></div>
      {/each}
    {/each}
    {#if result.results.length}<p>Showing matching excerpts from saved history, including messages not loaded in this pane.</p>{/if}
  {/if}
</div>

<script lang="ts" module>
  function focus(node: HTMLInputElement) { node.focus(); }
</script>

<style>
  .session-search { padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); max-height: 40%; overflow-y: auto; }
  .controls { display: flex; gap: 0.5rem; }
  input { flex: 1; min-width: 0; padding: 0.4rem; }
  p, small { color: var(--fg-3); font-size: var(--s-1); }
  p { margin: 0.4rem 0; }
  .match { margin-top: 0.5rem; }
  pre { white-space: pre-wrap; overflow-wrap: anywhere; font: inherit; font-size: var(--s-1); margin: 0.2rem 0; }
</style>
