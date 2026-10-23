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

  let { id }: { id: string } = $props();

  // svelte-ignore state_referenced_locally
  const t = new Transcript(id);
  t.open();
  onDestroy(() => t.close());

  const s = $derived(live.byId(id));
  const parent = $derived(layout.forks[id] ? live.byId(layout.forks[id]) : undefined);
  const working = $derived(t.busy || !!s?.isProcessing);

  let draft = $state("");
  let btw = $state(false);
  let sending = $state(false);
  let notice = $state("");
  let scroller: HTMLElement;
  let pinned = $state(true);

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
        layout.open(r.sessionId, { after: id });
        btw = false;
      } else {
        const r = await api.sendMessage({ sessionId: id, content: text, mode });
        if (r.mode === SendMode.INTERRUPT || r.mode === SendMode.INTERRUPT_URGENT) notice = "Queued for the running turn";
      }
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
    if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
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
  onfocusin={() => (layout.focused = id)}
  onpointerdown={() => (layout.focused = id)}
>
  <header class="head">
    <span class="bar"></span>
    <div class="who">
      <div class="l1">
        <span class="name">{s?.name ?? id}</span>
        <span class="dir" title={s?.workingDir}>{s ? shortDir(s.workingDir) : ""}</span>
      </div>
      <div class="l2">
        {#if parent}<span>btw fork of {parent.name}</span>{/if}
        <span>{s?.model}</span>
        {#if s?.tokenUsage && (s.tokenUsage.input || s.tokenUsage.output)}
          <span>{tokens(s.tokenUsage.input)} in, {tokens(s.tokenUsage.output)} out</span>
        {/if}
        {#if !t.attached}<span class="warn">{t.error ? "Reconnecting" : "Connecting"}</span>{/if}
      </div>
    </div>
    <button class="x" onclick={() => layout.close(id)} aria-label="Close pane">✕</button>
  </header>
  {#if features.summaries}
    <div class="summary">
      <p>{s?.summary || (summarizing ? "" : "No summary yet.")}</p>
      <button onclick={resummarize} disabled={summarizing} title={s?.summaryUnix ? `Updated ${ago(s.summaryUnix)} ago` : ""}>
        {summarizing ? "Summarizing" : s?.summary ? "Refresh" : "Summarize"}
      </button>
    </div>
  {/if}

  <div class="scroll" bind:this={scroller} onscroll={onScroll}>
    {#if t.total > t.blocks.length && t.blocks.length > 0}
      <p class="older">{t.total - t.blocks.length} earlier messages not shown</p>
    {/if}
    {#each t.blocks as b, i (i)}
      <Block block={b} />
    {/each}
    {#if working}
      <p class="status"><span class="spin"></span>{t.status || s?.detail || "Working"}</p>
    {/if}
  </div>

  <form class="composer" class:btw onsubmit={(e) => (e.preventDefault(), submit())}>
    {#if btw}
      <p class="mode">Side question. It runs in a fork with this conversation. {s?.name ?? "This session"} keeps going.</p>
    {/if}
    <textarea
      rows="2"
      bind:value={draft}
      onkeydown={onKey}
      placeholder={btw ? "Ask in a fork" : working ? "Steer the running turn" : "Message"}
      enterkeyhint="send"
    ></textarea>
    <div class="actions">
      <button type="button" class="toggle" class:on={btw} onclick={() => (btw = !btw)} aria-pressed={btw}>btw</button>
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
</section>

<style>
  .pane {
    flex: 1 1 0;
    min-width: 0;
    min-height: 0;
    display: flex;
    flex-direction: column;
    border-right: 1px solid var(--line);
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
  }
  .l1 {
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
  }
  .name {
    font-weight: 600;
  }
  .dir {
    color: var(--fg-2);
    font-size: var(--s-1);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .l2 {
    display: flex;
    flex-wrap: wrap;
    gap: 0 0.75rem;
    color: var(--fg-3);
    font-size: var(--s-1);
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
  .summary button {
    flex: none;
    color: var(--fg-3);
  }
  .summary button:hover {
    color: var(--fg);
  }
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
  }
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
