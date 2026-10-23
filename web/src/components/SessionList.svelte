<script lang="ts">
  import type { Session } from "../gen/jcodeweb/v1/jcodeweb_pb";
  import { live } from "../lib/live.svelte";
  import { layout, SESSION_DRAG_TYPE } from "../lib/layout.svelte";
  import { sessions as api } from "../lib/client";
  import { ago, shortDir, tone } from "../lib/format";

  let { onpick }: { onpick: (id: string, e?: MouseEvent) => void } = $props();

  let q = $state("");
  let showIdle = $state(false);
  let creating = $state("");
  let createError = $state("");

  async function create(dir: string) {
    if (creating) return;
    creating = dir; createError = "";
    try {
      const session = await api.createSession({ workingDir: dir });
      collapsed = { ...collapsed, [dir]: false };
      layout.open(session.sessionId);
      onpick(session.sessionId);
    } catch (e) { createError = e instanceof Error ? e.message : String(e); }
    finally { creating = ""; }
  }
  let collapsed = $state<Record<string, boolean>>(JSON.parse(localStorage.getItem("jw.collapsed") ?? "{}"));

  function dragSource(node: HTMLButtonElement, id: string) {
    const media = matchMedia("(min-width: 900px)");
    const setup = () => { node.draggable = media.matches; };
    function start(event: DragEvent) {
      if (!media.matches || !event.dataTransfer) { event.preventDefault(); return; }
      event.dataTransfer.effectAllowed = "move";
      event.dataTransfer.setData(SESSION_DRAG_TYPE, id);
      event.dataTransfer.setData("text/plain", id);
    }
    setup();
    node.addEventListener("dragstart", start);
    media.addEventListener("change", setup);
    return { destroy() { media.removeEventListener("change", setup); node.removeEventListener("dragstart", start); } };
  }

  function toggle(dir: string) {
    collapsed = { ...collapsed, [dir]: !collapsed[dir] };
    localStorage.setItem("jw.collapsed", JSON.stringify(collapsed));
  }

  // Hide sessions with no recorded activity for a day unless asked.
  const DAY = 86400;
  const filtered = $derived.by(() => {
    const now = Date.now() / 1000;
    const needle = q.trim().toLowerCase();
    return live.sessions.filter((s) => {
      if (needle) {
        return [s.name, s.workingDir, s.model, s.detail].some((f) => f.toLowerCase().includes(needle));
      }
      if (showIdle || s.isProcessing || layout.panes.includes(s.id)) return true;
      return Number(s.lastEventUnix) > now - DAY;
    });
  });
  const hidden = $derived(live.sessions.length - filtered.length);

  type Group = { dir: string; sessions: Session[]; busy: number; last: number };

  // Groups with work in progress first, then by latest activity.
  const groups = $derived.by(() => {
    const m = new Map<string, Group>();
    for (const s of filtered) {
      const dir = s.workingDir || "(no folder)";
      let g = m.get(dir);
      if (!g) m.set(dir, (g = { dir, sessions: [], busy: 0, last: 0 }));
      g.sessions.push(s);
      if (s.isProcessing) g.busy++;
      g.last = Math.max(g.last, Number(s.lastEventUnix));
    }
    return [...m.values()].sort((a, b) => (b.busy > 0 ? 1 : 0) - (a.busy > 0 ? 1 : 0) || b.last - a.last || a.dir.localeCompare(b.dir));
  });
</script>

<div class="wrap">
  <input class="search" type="search" placeholder="Filter by name, folder, model" bind:value={q} />
  <div class="list">
    {#each groups as g (g.dir)}
      {@const shut = collapsed[g.dir] && !q}
      <section class="group">
        <div class="gbar"><button class="ghead" onclick={() => toggle(g.dir)} aria-expanded={!shut} title={g.dir}>
          <span class="caret" class:shut>▾</span>
          <span class="gdir">{shortDir(g.dir)}</span>
          {#if g.busy}<span class="gbusy">{g.busy} working</span>{/if}
          <span class="gcount">{g.sessions.length}</span>
        </button><button class="create" disabled={!!creating || g.dir === "(no folder)"} onclick={() => create(g.dir)} aria-label={`New session in ${g.dir}`} title={`New session in ${shortDir(g.dir)}`}>{creating === g.dir ? "…" : "+"}</button></div>
        {#if !shut}
          <ul>
            {#each g.sessions as s (s.id)}
              {@const t = tone(s.status, s.isProcessing)}
              <li>
                <button
                  class="row"
                  class:open={layout.panes.includes(s.id)}
                  class:focused={layout.focused === s.id}
                  use:dragSource={s.id}
                  data-session-id={s.id}
                  title={[s.shortSummary, s.summary, s.detail].filter(Boolean).join("\n")}
                  data-tone={t}
                  onclick={(e) => onpick(s.id, e)}
                >
                  <span class="bar"></span>
                  <span class="main">
                    <span class="line1">
                      <span class="name">{s.name}</span>
                      <span class="sub">
                        {#if s.shortSummary}
                          {s.shortSummary}
                        {:else if s.isProcessing && s.detail}
                          {s.detail}
                        {:else if s.summary}
                          {s.summary}
                        {:else if layout.forks[s.id]}
                          btw fork of {live.byId(layout.forks[s.id])?.name ?? "session"}
                        {:else}
                          {s.status}{s.model ? `, ${s.model}` : ""}
                        {/if}
                      </span>
                      <span class="age">{ago(s.lastEventUnix)}</span>
                    </span>
                  </span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      </section>
    {/each}
  </div>
  {#if createError}<p class="create-error">{createError}</p>{/if}
  {#if (showIdle || hidden > 0) && !q}
    <button class="more" onclick={() => (showIdle = !showIdle)}>
      {showIdle ? "Hide quiet sessions" : `Show ${hidden} quiet sessions`}
    </button>
  {/if}
</div>

<style>
  .wrap {
    flex: 1;
    min-height: 0;
    display: flex;
    flex-direction: column;
  }
  .search {
    margin: 0 0.75rem 0.5rem;
    padding: 0.45rem 0.65rem;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--ink);
    font-size: var(--s0);
  }
  .list {
    overflow-y: auto;
    flex: 1;
  }
  .group + .group {
    border-top: 1px solid var(--line);
  }
  .ghead {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    flex: 1;
    min-width: 0;
    padding: 0.5rem 1rem 0.25rem 0.625rem;
    text-align: left;
    color: var(--fg-2);
    font-size: var(--s-1);
  }
  .gbar { display: flex; align-items: center; }
  .create { flex: none; color: var(--fg-2); padding: 0.25rem 0.75rem; font-size: var(--s1); }
  .create:hover { color: var(--fg); background: var(--ink-3); }
  .create:disabled { opacity: 0.4; }
  .create-error { margin: 0; padding: 0.5rem 0.75rem; color: var(--failed); font-size: var(--s-1); }
  .ghead:hover {
    color: var(--fg);
  }
  .caret {
    width: 0.75rem;
    color: var(--fg-3);
    transition: transform 120ms;
  }
  .caret.shut {
    transform: rotate(-90deg);
  }
  .gdir {
    flex: 1;
    min-width: 0;
    font-weight: 500;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .gbusy {
    color: var(--busy);
  }
  .gcount {
    color: var(--fg-3);
    font-variant-numeric: tabular-nums;
  }
  ul {
    list-style: none;
    margin: 0 0 0.25rem;
    padding: 0;
  }
  .row {
    display: flex;
    width: 100%;
    text-align: left;
    padding: 0.3rem 0.75rem 0.3rem 0;
    gap: 0.625rem;
  }
  .row:hover {
    background: var(--ink-3);
  }
  .row.open {
    background: color-mix(in srgb, var(--ink-3) 60%, transparent);
  }
  .row.focused {
    background: var(--ink-3);
  }
  .bar {
    flex: none;
    width: 3px;
    margin-left: 0.625rem;
    border-radius: 2px;
    background: var(--idle);
  }
  [data-tone="busy"] .bar {
    background: var(--busy);
    animation: pulse 1.6s ease-in-out infinite;
  }
  [data-tone="ready"] .bar {
    background: var(--ready);
  }
  [data-tone="failed"] .bar {
    background: var(--failed);
  }
  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }
  .main {
    min-width: 0;
    flex: 1;
  }
  .line1 {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
  }
  .name {
    flex: none;
    font-weight: 600;
    font-size: var(--s0);
    min-width: 1.75rem;
  }
  .sub {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--fg-3);
    font-size: var(--s-1);
  }
  [data-tone="busy"] .sub {
    color: var(--fg-2);
  }
  .age {
    color: var(--fg-3);
    font-size: var(--s-1);
    font-variant-numeric: tabular-nums;
  }
  .more {
    padding: 0.5rem 1rem;
    text-align: left;
    color: var(--fg-3);
    font-size: var(--s-1);
    border-top: 1px solid var(--line);
  }
  .more:hover {
    color: var(--fg);
  }
</style>
