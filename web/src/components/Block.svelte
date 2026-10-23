<script lang="ts">
  import type { Block } from "../lib/transcript.svelte";
  import { md } from "../lib/format";

  let { block }: { block: Block } = $props();

  let open = $state(false);

  function brief(s: string, n = 140) {
    const one = s.replace(/\s+/g, " ").trim();
    return one.length > n ? one.slice(0, n) + "…" : one;
  }
  function pretty(s: string) {
    try {
      return JSON.stringify(JSON.parse(s), null, 2);
    } catch {
      return s;
    }
  }
</script>

{#if block.kind === "user"}
  <div class="user" data-source={block.source}>
    {#if block.source === "interrupt"}<span class="tag">steer</span>{/if}
    <div class="md">{@html md(block.text)}</div>
  </div>
{:else if block.kind === "assistant"}
  <div class="assistant md" class:live={block.live}>{@html md(block.text)}</div>
{:else if block.kind === "reasoning"}
  <details class="reasoning" open={block.live}>
    <summary>Thinking</summary>
    <pre>{block.text}</pre>
  </details>
{:else if block.kind === "tool"}
  <div class="tool" data-state={block.state} class:err={!!block.error}>
    <button class="tool-head" onclick={() => (open = !open)} aria-expanded={open}>
      <span class="tname">{block.name}</span>
      <span class="tintent">{block.intent || brief(block.input, 80)}</span>
    </button>
    {#if open}
      {#if block.input}<pre class="io">{pretty(block.input)}</pre>{/if}
      {#if block.output || block.error}<pre class="io out">{block.error || block.output}</pre>{/if}
    {/if}
  </div>
{:else if block.kind === "error"}
  <p class="error">{block.text}</p>
{:else}
  <p class="system">{brief(block.text, 300)}</p>
{/if}

<style>
  .user {
    margin: 0.875rem 0 0.5rem;
    padding: 0.5rem 0.75rem;
    border-radius: 8px;
    background: var(--user);
  }
  .user[data-source="interrupt"] {
    background: transparent;
    border: 1px dashed var(--user);
  }
  .tag {
    font-size: var(--s-1);
    color: var(--fg-3);
  }
  .assistant {
    margin: 0.375rem 0;
  }
  .md :global(p) {
    margin: 0.4rem 0;
  }
  .md :global(pre) {
    background: var(--ink-2);
    padding: 0.5rem 0.625rem;
    border-radius: 6px;
    overflow-x: auto;
    white-space: pre;
  }
  .md :global(code) {
    background: var(--ink-2);
    padding: 0.05rem 0.25rem;
    border-radius: 3px;
  }
  .md :global(pre code) {
    background: none;
    padding: 0;
  }
  .md :global(table) {
    border-collapse: collapse;
    font-size: var(--s-1);
  }
  .md :global(td),
  .md :global(th) {
    border: 1px solid var(--line);
    padding: 0.2rem 0.5rem;
  }
  .md :global(ul),
  .md :global(ol) {
    padding-left: 1.25rem;
  }
  .live :global(> :last-child)::after {
    content: "▍";
    color: var(--busy);
  }
  .reasoning {
    margin: 0.25rem 0;
    color: var(--fg-3);
  }
  .reasoning summary {
    cursor: pointer;
    font-size: var(--s-1);
  }
  .reasoning pre {
    font-family: var(--sans);
    font-size: var(--s-1);
    padding-left: 0.75rem;
    border-left: 2px solid var(--line);
  }
  .tool {
    margin: 0.125rem 0;
    font-size: var(--s-1);
  }
  .tool-head {
    display: flex;
    gap: 0.5rem;
    width: 100%;
    text-align: left;
    padding: 0.15rem 0;
    color: var(--fg-2);
  }
  .tool-head:hover {
    color: var(--fg);
  }
  .tname {
    font-family: var(--mono);
    color: var(--fg-3);
    flex: none;
  }
  .tool[data-state="running"] .tname,
  .tool[data-state="input"] .tname {
    color: var(--busy);
  }
  .tool.err .tname {
    color: var(--failed);
  }
  .tintent {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .io {
    margin: 0.25rem 0 0.5rem;
    padding: 0.5rem 0.625rem;
    background: var(--ink-2);
    border-radius: 6px;
    max-height: 24rem;
    overflow: auto;
  }
  .out {
    color: var(--fg-2);
  }
  .system {
    color: var(--fg-3);
    font-size: var(--s-1);
    margin: 0.375rem 0;
  }
  .error {
    color: var(--failed);
    font-size: var(--s-1);
  }
</style>
