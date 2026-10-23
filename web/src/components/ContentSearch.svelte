<script lang="ts">
  import type { Session, SearchIndexStatus } from "../gen/jcodeweb/v1/jcodeweb_pb";
  import { sessions as api } from "../lib/client";
  import { live } from "../lib/live.svelte";
  import { shortDir } from "../lib/format";

  let { query, onpick }: { query: string; onpick: (id: string, e?: MouseEvent) => void } = $props();
  let result = $state<Awaited<ReturnType<typeof api.searchSessions>>>();
  let status = $state<SearchIndexStatus>();
  let loading = $state(false);
  let error = $state("");
  let refresh = $state(0);
  const valid = $derived(Array.from(query.trim()).length >= 3);

  function open(session: Session, event: MouseEvent) {
    live.remember(session);
    onpick(session.id, event);
  }

  $effect(() => {
    const needle = query.trim();
    const searchable = valid;
    void refresh;
    const controller = new AbortController();
    let timer: ReturnType<typeof setTimeout>;
    result = undefined;
    error = "";
    loading = searchable;
    async function search() {
      try {
        const options = { signal: controller.signal, timeoutMs: 5000 };
        if (searchable) {
          const response = await api.searchSessions({ query: needle, limit: 50 }, options);
          if (controller.signal.aborted) return;
          result = response;
          status = response.status;
        } else {
          const response = await api.getSearchIndexStatus({}, options);
          if (controller.signal.aborted) return;
          status = response.status;
        }
        error = "";
        if (status?.indexing) timer = setTimeout(search, 3000);
      } catch (e) {
        if (!controller.signal.aborted) error = e instanceof Error ? e.message : String(e);
      } finally {
        if (!controller.signal.aborted) loading = false;
      }
    }
    timer = setTimeout(search, searchable ? 200 : 0);
    return () => { controller.abort(); clearTimeout(timer); };
  });
</script>

<div class="content-search" aria-busy={loading}>
  <div class="search-status" aria-live="polite">
    {#if status}
      <span>{status.indexedSessions}/{status.totalSessions} sessions indexed{status.indexing ? " · Indexing…" : ""}{status.failedSessions ? ` · ${status.failedSessions} failed` : ""}</span>
    {:else if loading}<span>Searching…</span>{/if}
    <button onclick={() => refresh++} aria-label="Refresh content search" title="Refresh content search">↻</button>
  </div>
  {#if error || status?.error}<p class="error" role="alert">{error || status?.error}</p>{/if}
  {#if !valid}
    <p class="hint">Search messages and tool activity with at least 3 characters. Matches are literal substrings, not just whole words.</p>
  {:else if result && !result.results.length && !error}
    <p class="hint">No matching sessions{status?.indexing ? " indexed yet. Results update as indexing progresses." : "."}</p>
  {/if}
  {#if result?.results.length}
    <div class="results">
      {#each result.results as match (match.session?.id)}
        {@const session = match.session}
        {#if session}
          <button class="content-result" data-search-session-id={session.id} onclick={(e) => open(session, e)}>
            <span class="result-heading"><strong>{session.name || session.id}</strong><span>{match.excerpts[0]?.role}</span></span>
            <span class="result-dir" title={session.workingDir}>{shortDir(session.workingDir)}{session.parentSessionId ? " · Sub-session" : ""}</span>
            {#each match.excerpts as excerpt}
              <span class="excerpt">{excerpt.text}</span>
            {/each}
          </button>
        {/if}
      {/each}
    </div>
    {#if result.truncated}<p class="hint">Showing the first 50 matching sessions. Narrow your search for more specific results.</p>{/if}
  {/if}
</div>

<style>
  .content-search { flex: 1; min-height: 0; overflow-y: auto; }
  .search-status { display: flex; justify-content: space-between; align-items: baseline; gap: 0.5rem; padding: 0 0.75rem 0.35rem; color: var(--fg-3); font-size: var(--s-1); }
  .search-status button { margin-left: auto; padding: 0 0.25rem; }
  .hint, .error { margin: 0; padding: 0.5rem 0.75rem; font-size: var(--s-1); color: var(--fg-3); }
  .error { color: var(--failed); overflow-wrap: anywhere; }
  .content-result { display: block; width: 100%; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); text-align: left; }
  .content-result:hover, .content-result:focus-visible { background: var(--ink-3); }
  .result-heading { display: flex; gap: 0.5rem; justify-content: space-between; align-items: baseline; font-size: var(--s-1); }
  .result-heading > span, .result-dir { color: var(--fg-3); font-size: 11px; }
  .result-heading strong { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .result-dir { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; margin-top: 2px; }
  .excerpt { display: -webkit-box; -webkit-line-clamp: 4; line-clamp: 4; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; white-space: pre-wrap; font-size: 12px; color: var(--fg-2); margin-top: 0.3rem; }
</style>
