<script lang="ts">
  import { onDestroy, tick } from "svelte";
  import { Transcript } from "../lib/transcript.svelte";
  import { live } from "../lib/live.svelte";
  import { layout } from "../lib/layout.svelte";
  import { sessions as api } from "../lib/client";
  import { SendMode } from "../gen/jcodeweb/v1/jcodeweb_pb";
  import { shortDir, tokens, tone, ago } from "../lib/format";
  import { features } from "../lib/features.svelte";
  import Block from "./Block.svelte";
  import ScheduledTasks from "./ScheduledTasks.svelte";
  import SessionSearch from "./SessionSearch.svelte";
  import { promptHistory, rememberPrompt } from "../lib/prompt-history";

  let { id, windowed = false, onwindowkey, onmaximize, maximized = false }: {
    id: string;
    windowed?: boolean;
    onwindowkey?: (e: KeyboardEvent) => void;
    onmaximize?: () => void;
    maximized?: boolean;
  } = $props();

  // svelte-ignore state_referenced_locally
  const t = new Transcript(id);
  t.open();
  onDestroy(() => t.close());

  const s = $derived(live.byId(id));
  const parent = $derived(layout.forks[id] ? live.byId(layout.forks[id]) : undefined);
  const coordinator = $derived(s?.parentSessionId ? live.byId(s.parentSessionId) : undefined);
  const children = $derived(live.sessions.filter(child => child.parentSessionId === id));
  const working = $derived(t.busy || !!s?.isProcessing);

  let draft = $state("");
  let finding = $state(false);
  let summaryOpen = $state(false);
  let summaryPreview = $state<{ x: number; y: number } | null>(null);
  function previewSummary(node: HTMLElement) {
    const rect = node.getBoundingClientRect();
    summaryPreview = { x: Math.max(8, Math.min(rect.right - 360, innerWidth - 368)), y: Math.max(8, Math.min(rect.bottom + 6, innerHeight - 276)) };
  }
  function showPreview(node: HTMLDivElement) { node.showPopover(); }
  $effect(() => {
    if (layout.searchSession === id) {
      finding = true;
      layout.searchSession = "";
    }
  });
  let btw = $state(false);
  let sending = $state(false);
  let notice = $state("");
  let scroller: HTMLElement;
  let pinned = $state(true);
  let composer = $state<HTMLTextAreaElement>();
  let historyIndex = $state(-1);
  let recalled: string[] = [];
  let savedDraft = "";
  let anchor: { height: number; top: number } | null = null;

  function recall(older: boolean) {
    if (historyIndex < 0) {
      if (!older) return;
      recalled = promptHistory(id, t.userMessages);
      savedDraft = draft;
    }
    const next = older ? Math.min(historyIndex + 1, recalled.length - 1) : historyIndex - 1;
    if (next < -1 || !recalled.length) return;
    historyIndex = next;
    draft = next < 0 ? savedDraft : recalled[next];
    void tick().then(() => { composer?.focus(); composer?.setSelectionRange(draft.length, draft.length); });
  }

  function loadOlder() {
    pinned = false;
    anchor = { height: scroller.scrollHeight, top: scroller.scrollTop };
    t.loadOlder();
  }

  $effect(() => {
    if (!t.loadingOlder) void tick().then(() => {
      if (anchor && scroller) {
        scroller.scrollTop = anchor.top + scroller.scrollHeight - anchor.height;
        anchor = null;
      }
    });
  });

  function onScroll() {
    pinned = scroller.scrollHeight - scroller.scrollTop - scroller.clientHeight < 80;
  }

  // Follow output while pinned to the bottom.
  $effect(() => {
    void t.blocks.length;
    const last = t.blocks[t.blocks.length - 1];
    if (last && "text" in last) void last.text;
    if (last && last.kind === "tool") void last.output;
    if (pinned) tick().then(() => scroller && (scroller.scrollTop = scroller.scrollHeight));
  });

  async function submit(mode: SendMode = SendMode.UNSPECIFIED) {
    const text = draft.trim();
    if (!text || sending) return;
    sending = true;
    notice = "";
    try {
      if (btw) {
        const r = await api.btw({ sessionId: id, question: text });
        layout.markFork(r.sessionId, id);
        layout.open(r.sessionId, { after: id, tabWith: id });
        btw = false;
      } else {
        const r = await api.sendMessage({ sessionId: id, content: text, mode });
        if (r.mode === SendMode.INTERRUPT || r.mode === SendMode.INTERRUPT_URGENT) notice = "Queued for the running turn";
      }
      rememberPrompt(id, text);
      historyIndex = -1;
      draft = "";
      pinned = true;
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    } finally {
      sending = false;
    }
  }

  async function cancel() {
    notice = "";
    try {
      await api.cancel({ sessionId: id });
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    }
  }

  let summarizing = $state(false);
  async function resummarize() {
    summarizing = true;
    try {
      await api.summarize({ sessionId: id });
    } catch (e) {
      notice = e instanceof Error ? e.message : String(e);
    } finally {
      summarizing = false;
    }
  }

  function onKey(e: KeyboardEvent) {
    if (e.isComposing) return;
    const input = e.currentTarget as HTMLTextAreaElement;
    if (e.key === "ArrowUp" && !e.shiftKey && input.selectionStart === input.selectionEnd && !draft.slice(0, input.selectionStart).includes("\n")) {
      e.preventDefault(); recall(true);
    } else if (e.key === "ArrowDown" && !e.shiftKey && historyIndex >= 0 && !draft.slice(input.selectionEnd).includes("\n")) {
      e.preventDefault(); recall(false);
    } else if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      void submit(e.altKey ? SendMode.INTERRUPT_URGENT : SendMode.UNSPECIFIED);
    } else if (e.key === "Escape" && working) {
      e.preventDefault();
      void cancel();
    }
  }
</script>

<section
  class="pane"
  role="group"
  aria-label={s?.name ?? id}
  class:focused={layout.focused === id}
  data-tone={s ? tone(s.status, working) : "idle"}
  onfocusin={() => layout.focus(id)}
  onpointerdown={() => layout.focus(id)}
>
  <header class="head">
    <span class="bar"></span>
    <button class="who" class:movable={windowed} onkeydown={onwindowkey} ondblclick={onmaximize}
      title={windowed ? "Drag to move. Arrow keys move, Shift + arrows resize. Double-click to maximize." : undefined}
      aria-label={windowed ? `Move ${s?.name ?? id} window` : `Session ${s?.name ?? id}`}>
      <div class="l1">
        <span class="name">{s?.name ?? id}</span>
        {#if s?.shortSummary}<span class="task" title={s.summary}>{s.shortSummary}</span>{/if}
        <span class="dir" title={s?.workingDir}>{s ? shortDir(s.workingDir) : ""}</span>
        {#if parent}<span class="context">btw fork of {parent.name}</span>{/if}
        {#if !t.attached && !t.readOnly}<span class="context warn">{t.error ? "Reconnecting" : "Connecting"}</span>{/if}
      </div>
    </button>
    {#if onmaximize}<button class="x" onclick={onmaximize} aria-label={maximized ? "Restore window" : "Maximize window"}>{maximized ? "▣" : "□"}</button>{/if}
    {#if features.summaries}
      <button class="x" onclick={() => { summaryOpen = !summaryOpen; summaryPreview = null; }} aria-label="Toggle session summary" aria-expanded={summaryOpen}
        onpointerenter={(e) => { if (e.pointerType !== "touch") previewSummary(e.currentTarget); }} onpointerleave={() => summaryPreview = null}
        onfocus={(e) => previewSummary(e.currentTarget)} onblur={() => summaryPreview = null}>{summaryOpen ? "▾" : "▸"} Summary</button>
      <button class="x" onclick={resummarize} disabled={summarizing || t.readOnly} aria-label="Refresh session summary" title={s?.summaryUnix ? `Updated ${ago(s.summaryUnix)} ago` : "Generate summary"}>{summarizing ? "…" : "↻"}</button>
    {/if}
    <button class="x" onclick={() => finding = !finding} aria-label="Search this session" title="Search saved session history">⌕</button>
    <button class="x" onclick={() => layout.close(id)} aria-label="Close pane">✕</button>
  </header>
  {#if summaryPreview}
    <div class="summary-preview" popover="manual" use:showPreview role="tooltip" style="inset: auto; margin: 0; left: {summaryPreview.x}px; top: {summaryPreview.y}px">{s?.summary || "No summary yet."}</div>
  {/if}
  {#if finding}<SessionSearch {id} onclose={() => finding = false} />{/if}
  {#if features.summaries && summaryOpen}
    <div class="summary">
      <p>{s?.summary || (summarizing ? "" : "No summary yet.")}</p>
    </div>
  {/if}

  {#if coordinator}
    <div class="subsessions">Sub-session of <button onclick={() => layout.open(coordinator.id)}>{coordinator.name}</button></div>
  {:else if s?.isHeadless}
    <div class="subsessions">Headless worker</div>
  {/if}
  {#if children.length}
    <details class="subsessions" open>
      <summary>Sub-sessions · {children.filter(child => child.isProcessing).length} running · {children.length} total</summary>
      <div class="children">
        {#each children as child (child.id)}
          <button data-child-id={child.id} onclick={() => layout.open(child.id, { tabWith: id })} title={child.detail || child.summary}>
            <strong>{child.name}</strong><span class:busy={child.isProcessing}>{child.status}</span><span class="child-detail">{child.shortSummary || child.detail || child.model}</span><span>Inspect ↗</span>
          </button>
        {/each}
      </div>
    </details>
  {/if}
  {#if !t.readOnly}<ScheduledTasks {id} />{/if}
  <div class="scroll" bind:this={scroller} onscroll={onScroll}>
    {#if t.total > t.shownMessages && (!t.readOnly || t.historyLimit < 1000)}
      <button class="older" disabled={t.loadingOlder} onclick={loadOlder}>{t.loadingOlder ? "Loading history…" : `Load older messages (${t.total - t.shownMessages} earlier)`}</button>
    {/if}
    {#each t.blocks as b, i (i)}
      <Block block={b} />
    {/each}
    {#if working}
      <p class="status"><span class="spin"></span>{t.status || s?.detail || "Working"}</p>
    {/if}
  </div>

  {#if t.readOnly}
    <p class="archive-notice">Archived session · Read-only history</p>
  {:else}
  <form class="composer" class:btw onsubmit={(e) => (e.preventDefault(), submit())}>
    {#if btw}
      <p class="mode">Side question. It runs in a fork with this conversation. {s?.name ?? "This session"} keeps going.</p>
    {/if}
    <textarea
      rows="2"
      bind:value={draft}
      bind:this={composer}
      aria-label="Message"
      oninput={() => (historyIndex = -1)}
      onkeydown={onKey}
      placeholder={btw ? "Ask in a fork" : working ? "Steer the running turn" : "Message"}
      enterkeyhint="send"
    ></textarea>
    <div class="actions">
      <button type="button" class="toggle" class:on={btw} onclick={() => (btw = !btw)} aria-pressed={btw}>btw</button>
      <button type="button" class="history" onclick={() => recall(true)} aria-label="Previous sent message" title="Previous sent message (Up arrow)">↑</button>
      <button type="button" class="history" onclick={() => recall(false)} disabled={historyIndex < 0} aria-label="Next sent message" title="Next sent message (Down arrow)">↓</button>
      <span class="metadata" title={`${s?.model ?? ""}${s?.tokenUsage ? ` · ${tokens(s.tokenUsage.input)} in, ${tokens(s.tokenUsage.output)} out` : ""}`}>
        <span class="model">{s?.model}</span>
        {#if s?.tokenUsage && (s.tokenUsage.input || s.tokenUsage.output)}
          <span class="usage">{tokens(s.tokenUsage.input)} in, {tokens(s.tokenUsage.output)} out</span>
        {/if}
      </span>
      {#if notice}<span class="notice">{notice}</span>{/if}
      <span class="grow"></span>
      {#if working}
        <button type="button" class="stop" onclick={cancel}>Stop</button>
      {/if}
      <button type="submit" class="send" disabled={!draft.trim() || sending}>
        {btw ? "Ask" : working ? "Steer" : "Send"}
      </button>
    </div>
  </form>
  {/if}
</section>

<style>
  .subsessions { padding: 0.35rem 0.75rem; border-bottom: 1px solid var(--line); color: var(--fg-2); font-size: var(--s-1); }
  .subsessions > button { text-decoration: underline; }
  .children { max-height: 8rem; overflow-y: auto; }
  .children button { display: flex; gap: 0.5rem; align-items: baseline; width: 100%; text-align: left; padding: 0.3rem 0; }
  .children button:hover { background: var(--ink-3); }
  .child-detail { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .busy { color: var(--busy); }
  .pane {
    flex: 1 1 0;
    min-width: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--line);
    overflow: hidden;
    border-radius: inherit;
  }
  .pane:last-child {
    border-right: 0;
  }
  .head {
    display: flex;
    gap: 0.625rem;
    padding: 0.625rem 0.75rem 0.625rem 0;
    border-bottom: 1px solid var(--line);
    background: var(--ink-2);
  }
  .pane.focused .head {
    background: var(--ink-3);
  }
  .bar {
    flex: none;
    width: 3px;
    border-radius: 0 2px 2px 0;
    background: var(--idle);
  }
  [data-tone="busy"] .bar {
    background: var(--busy);
  }
  [data-tone="ready"] .bar {
    background: var(--ready);
  }
  [data-tone="failed"] .bar {
    background: var(--failed);
  }
  .who {
    flex: 1;
    min-width: 0;
    text-align: left;
  }
  .who.movable { cursor: grab; touch-action: none; user-select: none; }
  .who.movable:active { cursor: grabbing; }
  .l1 {
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
  }
  .name {
    font-weight: 600;
  }
  .task { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--s-1); color: var(--fg-2); }
  .dir {
    color: var(--fg-2);
    font-size: var(--s-1);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .context {
    color: var(--fg-3);
    font-size: var(--s-1);
    white-space: nowrap;
  }
  .warn {
    color: var(--busy);
  }
  .x {
    color: var(--fg-3);
    padding: 0 0.25rem;
    align-self: flex-start;
  }
  .x:hover {
    color: var(--fg);
  }
  .summary {
    display: flex;
    align-items: flex-start;
    gap: 0.75rem;
    padding: 0.5rem 1rem;
    border-bottom: 1px solid var(--line);
    font-size: var(--s-1);
    color: var(--fg-2);
    line-height: 1.45;
  }
  .summary p {
    flex: 1;
    margin: 0;
  }
  .summary-preview { position: fixed; z-index: 100; width: min(360px, calc(100vw - 16px)); max-height: min(260px, calc(100dvh - 16px)); overflow: hidden; padding: 0.75rem; border: 1px solid var(--line); border-radius: 6px; background: var(--ink-2); color: var(--fg-2); font-size: var(--s-1); line-height: 1.5; box-shadow: 0 6px 20px #0006; pointer-events: none; overflow-wrap: anywhere; }
  .scroll {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    padding: 0.75rem 1rem 1rem;
    overscroll-behavior: contain;
  }
  .older {
    color: var(--fg-3);
    font-size: var(--s-1);
    margin: 0 0 0.75rem;
    padding: 0.35rem 0.5rem;
    border: 1px solid var(--line);
    border-radius: 4px;
  }
  .history { color: var(--fg-2); padding: 0.25rem 0.4rem; }
  .history:disabled { opacity: 0.35; }
  .status {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    color: var(--busy);
    font-size: var(--s-1);
    margin: 0.5rem 0 0;
  }
  .spin {
    width: 0.6rem;
    height: 0.6rem;
    border: 2px solid var(--busy);
    border-right-color: transparent;
    border-radius: 50%;
    animation: spin 0.9s linear infinite;
  }
  @keyframes spin {
    to {
      transform: rotate(360deg);
    }
  }
  .archive-notice { margin: 0; padding: 0.65rem 0.75rem; border-top: 1px solid var(--line); background: var(--ink-2); color: var(--fg-3); font-size: var(--s-1); }
  .composer {
    border-top: 1px solid var(--line);
    padding: 0.5rem 0.75rem calc(0.5rem + env(safe-area-inset-bottom));
    background: var(--ink-2);
  }
  .composer.btw {
    box-shadow: inset 3px 0 0 var(--focus);
  }
  .mode {
    margin: 0 0 0.375rem;
    font-size: var(--s-1);
    color: var(--focus);
  }
  textarea {
    width: 100%;
    resize: none;
    padding: 0.5rem 0.625rem;
    border: 1px solid var(--line);
    border-radius: 6px;
    background: var(--ink);
    font-size: var(--s0);
    field-sizing: content;
    max-height: 40vh;
  }
  .actions {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    margin-top: 0.375rem;
  }
  .actions > button { flex: none; }
  .metadata {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    min-width: 0;
    color: var(--fg-3);
    font-size: var(--s-1);
    white-space: nowrap;
    overflow: hidden;
  }
  .model { min-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .usage { flex: none; }
  .grow {
    flex: 1;
  }
  .notice {
    font-size: var(--s-1);
    color: var(--fg-2);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .toggle,
  .stop,
  .send {
    padding: 0.3rem 0.75rem;
    border-radius: 6px;
    font-size: var(--s-1);
    font-weight: 500;
  }
  .toggle {
    border: 1px solid var(--line);
    color: var(--fg-2);
  }
  .toggle.on {
    border-color: var(--focus);
    color: var(--focus);
  }
  .stop {
    border: 1px solid var(--failed);
    color: var(--failed);
  }
  .send {
    background: var(--fg);
    color: var(--ink);
  }
  .send:disabled {
    opacity: 0.4;
    cursor: default;
  }
</style>
