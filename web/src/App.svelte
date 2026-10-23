<script lang="ts">
  import { onMount } from "svelte";
  import { live } from "./lib/live.svelte";
  import { layout, PROGRESS_WINDOW } from "./lib/layout.svelte";
  import { push } from "./lib/push.svelte";
  import { features } from "./lib/features.svelte";
  import SessionList from "./components/SessionList.svelte";
  import Pane from "./components/Pane.svelte";
  import Feed from "./components/Feed.svelte";
  import Workspace from "./components/Workspace.svelte";
  import ProgressFeed from "./components/ProgressFeed.svelte";

  let showFeed = $state(false);
  let showList = $state(false);
  let wide = $state(true);

  onMount(() => {
    live.start();
    void features.load();
    void push.refresh();
    const mq = matchMedia("(min-width: 900px)");
    const set = () => (wide = mq.matches);
    set();
    mq.addEventListener("change", set);
    return () => mq.removeEventListener("change", set);
  });

  // On narrow screens show one pane: the focused one.
  const visible = $derived(wide ? layout.panes : layout.panes.filter((p) => p === layout.focused));
  const busyCount = $derived(live.sessions.filter((s) => s.isProcessing).length);

  function pick(id: string, e?: MouseEvent) {
    const add = !wide || layout.focused === PROGRESS_WINDOW || e?.metaKey || e?.ctrlKey || e?.shiftKey;
    if (add || layout.panes.includes(id)) layout.open(id);
    else layout.swap(layout.focused, id);
    if (!wide) showList = false;
  }
</script>

<div class="shell" class:wide>
  {#if wide || showList}
    <aside class="side">
      <header class="brand">
        <span class="title">jcode</span>
        {#if !wide}<button onclick={() => (showList = false)} aria-label="Close sessions">Close</button>{/if}
        <span class="conn" class:ok={live.connected} title={live.upstreamError || "Connected to jcode"}>
          {live.connected ? `${live.sessions.length} sessions, ${busyCount} working` : "jcode unreachable"}
        </span>
      </header>
      {#if !live.connected && live.upstreamError}
        <p class="upstream">{live.upstreamError}</p>
      {/if}
      <SessionList onpick={pick} />
      <footer class="side-foot">
        <button onclick={() => { layout.open(PROGRESS_WINDOW); showList = false; }}>Progress feed</button>
        <button onclick={() => (showFeed = !showFeed)}>{showFeed ? "Hide activity" : "Show activity"}</button>
        {#if push.supported}
          {#if push.enabled}
            <button onclick={() => push.test()}>Send test notification</button>
            <button onclick={() => push.disable()}>Turn off notifications</button>
          {:else}
            <button onclick={() => push.enable()}>Turn on notifications</button>
          {/if}
        {/if}
        {#if push.error}<p class="err">{push.error}</p>{/if}
      </footer>
    </aside>
  {/if}

  <main class="panes">
    {#if !wide}
      <nav class="tabs">
        <button class="menu" onclick={() => (showList = !showList)} aria-label="Sessions">☰</button>
        {#each layout.panes as id (id)}
          {@const s = live.byId(id)}
          <button class="tab" class:on={id === layout.focused} onclick={() => layout.focus(id)}>
            <span class="dot" data-busy={s?.isProcessing}></span>{id === PROGRESS_WINDOW ? "Progress" : s?.name ?? id.slice(8, 11)}
          </button>
        {/each}
        <button class="menu" onclick={() => (showFeed = !showFeed)} aria-label="Activity">≋</button>
      </nav>
    {/if}
    {#if wide}
      <Workspace />
    {:else if visible.length === 0}
      <section class="empty">
        <p>Pick a session to watch it live.</p>
        <p class="hint">
          {wide ? "Ctrl or Shift click to open more side by side. Use btw in a pane to ask a side question in a fork." : "Open the session list from the menu."}
        </p>
      </section>
    {:else}
      {#each visible as id (id)}
        {#if id === PROGRESS_WINDOW}<ProgressFeed />{:else}<Pane {id} />{/if}
      {/each}
    {/if}
  </main>

  {#if showFeed}
    <aside class="feed">
      <Feed onclose={() => (showFeed = false)} onpick={(id) => pick(id)} />
    </aside>
  {/if}
</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: 1fr;
    height: 100dvh;
  }
  .shell.wide {
    grid-template-columns: 300px 1fr auto;
  }
  .side {
    display: flex;
    flex-direction: column;
    min-height: 0;
    background: var(--ink-2);
    border-right: 1px solid var(--line);
  }
  .shell:not(.wide) .side {
    position: fixed;
    inset: 0;
    z-index: 10;
    padding-top: env(safe-area-inset-top);
  }
  .brand {
    display: flex;
    align-items: baseline;
    justify-content: space-between;
    padding: 0.875rem 1rem 0.5rem;
  }
  .title {
    font-weight: 600;
    font-size: var(--s1);
    letter-spacing: -0.01em;
  }
  .conn {
    font-size: var(--s-1);
    color: var(--failed);
  }
  .conn.ok {
    color: var(--fg-3);
  }
  .upstream {
    margin: 0 1rem 0.5rem;
    font-size: var(--s-1);
    color: var(--failed);
  }
  .side-foot {
    display: flex;
    flex-wrap: wrap;
    gap: 0.25rem 1rem;
    padding: 0.75rem 1rem calc(0.75rem + env(safe-area-inset-bottom));
    border-top: 1px solid var(--line);
    font-size: var(--s-1);
    color: var(--fg-2);
  }
  .side-foot button:hover {
    color: var(--fg);
  }
  .err {
    margin: 0;
    color: var(--failed);
    flex-basis: 100%;
  }
  .panes {
    display: flex;
    min-width: 0;
    min-height: 0;
    flex-direction: row;
  }
  .shell:not(.wide) .panes {
    flex-direction: column;
  }
  .tabs {
    display: flex;
    gap: 0.25rem;
    overflow-x: auto;
    padding: calc(0.5rem + env(safe-area-inset-top)) 0.5rem 0.5rem;
    background: var(--ink-2);
    border-bottom: 1px solid var(--line);
    scrollbar-width: none;
  }
  .tab,
  .menu {
    flex: none;
    padding: 0.375rem 0.75rem;
    border-radius: 6px;
    color: var(--fg-2);
    font-size: var(--s0);
  }
  .tab.on {
    background: var(--ink-3);
    color: var(--fg);
  }
  .dot {
    display: inline-block;
    width: 6px;
    height: 6px;
    margin-right: 0.4rem;
    border-radius: 50%;
    background: var(--ready);
    vertical-align: middle;
  }
  .dot[data-busy="true"] {
    background: var(--busy);
  }
  .empty {
    margin: auto;
    padding: 2rem;
    max-width: 32rem;
    color: var(--fg-2);
  }
  .empty p {
    margin: 0 0 0.5rem;
  }
  .hint {
    font-size: var(--s-1);
    color: var(--fg-3);
  }
  .feed {
    width: 360px;
    min-height: 0;
    border-left: 1px solid var(--line);
    background: var(--ink-2);
  }
  .shell:not(.wide) .feed {
    position: fixed;
    inset: 0;
    width: auto;
    z-index: 11;
    padding-top: env(safe-area-inset-top);
  }
</style>
