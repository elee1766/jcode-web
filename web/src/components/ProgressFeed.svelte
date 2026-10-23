<script lang="ts">
  import { onMount, untrack } from "svelte";
  import type { Event } from "../gen/jcodeweb/v1/jcodeweb_pb";
  import { sessions as api } from "../lib/client";
  import { live } from "../lib/live.svelte";
  import { layout, PROGRESS_WINDOW } from "../lib/layout.svelte";
  import { features } from "../lib/features.svelte";
  import { md, shortDir } from "../lib/format";

  let { windowed = false, onwindowkey, onmaximize, maximized = false }: {
    windowed?: boolean; onwindowkey?: (e: KeyboardEvent) => void; onmaximize?: () => void; maximized?: boolean;
  } = $props();
  let entries = $state<Event[]>([]);
  let loading = $state(false);
  let error = $state("");
  let older = $state(true);
  let attention = $state(false);
  let now = $state(Date.now());
  const relativeTime = new Intl.RelativeTimeFormat(undefined, { numeric: "always" });
  function age(unix: bigint) {
    const seconds = Math.max(0, Math.floor(now / 1000 - Number(unix)));
    if (seconds < 60) return relativeTime.format(-seconds, "second");
    if (seconds < 3600) return relativeTime.format(-Math.floor(seconds / 60), "minute");
    if (seconds < 86400) return relativeTime.format(-Math.floor(seconds / 3600), "hour");
    return relativeTime.format(-Math.floor(seconds / 86400), "day");
  }
  const shown = $derived(attention ? entries.filter(e => meta(e).reason === "needs_attention") : entries);

  function meta(event: Event): { reason?: string; model?: string; working_dir?: string; title?: string } {
    try { return JSON.parse(event.payloadJson); } catch { return {}; }
  }
  function label(reason?: string) {
    return ({ turn_complete: "Turn complete", needs_attention: "Needs attention", plan_updated: "Plan updated", progress: "Progress checkpoint", manual: "Summary refreshed" } as Record<string, string>)[reason ?? ""] ?? "Progress update";
  }
  function merge(events: Event[]) {
    const byId = new Map(entries.map(e => [e.id, e]));
    for (const e of events) if (e.type === "session_summary") byId.set(e.id, e);
    entries = [...byId.values()].sort((a, b) => a.id > b.id ? -1 : a.id < b.id ? 1 : 0);
  }
  const controller = new AbortController();
  async function load(beforeId = 0n) {
    if (loading) return;
    loading = true; error = "";
    try {
      const res = await api.listEvents({ eventType: "session_summary", beforeId, limit: 100 }, { signal: controller.signal });
      merge(res.events);
      older = res.events.length === 100;
    } catch (e) { if (!controller.signal.aborted) error = e instanceof Error ? e.message : String(e); }
    finally { loading = false; }
  }
  onMount(() => {
    void load();
    const timer = setInterval(() => { now = Date.now(); }, 1000);
    return () => { clearInterval(timer); controller.abort(); };
  });
  $effect(() => {
    const updates = live.events.filter(e => e.type === "session_summary");
    untrack(() => { if (updates.length) merge(updates); });
  });
</script>

<section class="progress" aria-label="AI progress feed" onfocusin={() => layout.focus(PROGRESS_WINDOW)} onpointerdown={() => layout.focus(PROGRESS_WINDOW)}>
  <header>
    <button class="who" class:movable={windowed} onkeydown={onwindowkey} ondblclick={onmaximize} aria-label="Move Progress feed window" title="Drag to move. Arrow keys move, Shift + arrows resize.">
      <span class="title">Progress feed</span><span class="stream" class:online={live.streaming}>{live.streaming ? "Live AI updates" : "Reconnecting"}</span>
    </button>
    {#if onmaximize}<button class="x" onclick={onmaximize} aria-label={maximized ? "Restore window" : "Maximize window"}>{maximized ? "▣" : "□"}</button>{/if}
    <button class="x" onclick={() => layout.close(PROGRESS_WINDOW)} aria-label="Close progress feed">✕</button>
  </header>
  <div class="filters">
    <button class:on={!attention} onclick={() => (attention = false)}>All updates</button>
    <button class:on={attention} onclick={() => (attention = true)}>Needs attention</button>
    <button onclick={() => load()} disabled={loading}>{loading ? "Loading" : "Refresh"}</button>
  </div>
  {#if error}<p class="error">{error}</p>{/if}
  <div class="entries">
    {#each shown as e (e.id)}
      {@const info = meta(e)}
      {@const date = new Date(Number(e.timestampUnix) * 1000)}
      <article data-event-id={String(e.id)} data-reason={info.reason}>
        <div class="meta"><button class="session" onclick={() => layout.open(e.sessionId)}>{e.sessionName || live.byId(e.sessionId)?.name || "Session"}</button><span class="dir" title={info.working_dir}>{shortDir(info.working_dir ?? "")}</span><time datetime={date.toISOString()} title={date.toLocaleString(undefined, { timeZoneName: "short" })}>{age(e.timestampUnix)}{#if now - date.getTime() > 86400000}<span class="date">{date.toLocaleDateString()}</span>{/if}</time></div>
        <div class="reason">{label(info.reason)}</div>
        {#if info.title}<h3>{info.title}</h3>{/if}
        <div class="summary">{@html md(e.summary)}</div>
      </article>
    {:else}
      <div class="empty">{!features.summaries ? "Enable an AI provider in the server configuration to receive progress updates." : loading ? "Loading progress updates…" : attention ? "No sessions need attention." : "AI updates appear after turn completion, plan changes, and file-edit checkpoints. Active-session updates are rate-limited to one per 45 seconds."}</div>
    {/each}
    {#if older && entries.length}<button class="more" disabled={loading} onclick={() => load(entries[entries.length - 1].id)}>Load older updates</button>{/if}
  </div>
  <footer>AI-generated from observed activity. {features.summaryModel}</footer>
</section>

<style>
  .progress { display: flex; flex-direction: column; flex: 1; min-width: 0; min-height: 0; background: var(--ink); }
  header { display: flex; gap: 0.625rem; padding: 0.625rem 0.75rem; border-bottom: 1px solid var(--line); background: var(--ink-2); }
  .who { flex: 1; min-width: 0; text-align: left; display: flex; flex-wrap: wrap; align-items: baseline; gap: 0.5rem; }
  .who.movable { cursor: grab; touch-action: none; user-select: none; }
  .title { font-weight: 600; }
  .stream { font-size: var(--s-1); color: var(--busy); }
  .stream.online { color: var(--ready); }
  .x { color: var(--fg-3); align-self: flex-start; padding: 0 0.25rem; }
  .filters { display: flex; gap: 0.375rem; padding: 0.5rem 0.75rem; border-bottom: 1px solid var(--line); font-size: var(--s-1); }
  .filters button { color: var(--fg-3); padding: 0.2rem 0.5rem; border-radius: 4px; }
  .filters button.on { color: var(--fg); background: var(--ink-3); }
  .filters button:last-child { margin-left: auto; }
  .entries { flex: 1; min-height: 0; overflow-y: auto; overscroll-behavior: contain; }
  article { padding: 0.75rem 1rem; border-bottom: 1px solid var(--line); }
  article[data-reason="needs_attention"] { border-left: 3px solid var(--failed); }
  .meta { display: flex; gap: 0.5rem; align-items: baseline; font-size: var(--s-1); }
  .session { font-weight: 600; color: var(--fg); }
  .session:hover { text-decoration: underline; }
  .dir { flex: 1; min-width: 0; color: var(--fg-3); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  time { color: var(--fg-3); flex: none; text-align: right; font-variant-numeric: tabular-nums; }
  .date { display: block; font-size: var(--s-1); }
  .reason { color: var(--ready); font-size: var(--s-1); margin-top: 0.2rem; }
  h3 { font-size: var(--s0); font-weight: 600; margin: 0.4rem 0 0; }
  [data-reason="needs_attention"] .reason { color: var(--failed); }
  .summary { color: var(--fg-2); font-size: var(--s0); line-height: 1.5; overflow-wrap: anywhere; }
  .summary :global(p) { margin: 0.375rem 0 0; }
  .empty, .error { padding: 1rem; color: var(--fg-3); font-size: var(--s-1); }
  .error { color: var(--failed); margin: 0; }
  .more { padding: 0.75rem 1rem; color: var(--fg-2); font-size: var(--s-1); }
  footer { border-top: 1px solid var(--line); padding: 0.4rem 0.75rem; color: var(--fg-3); font-size: var(--s-1); }
</style>
