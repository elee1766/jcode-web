<script lang="ts">
  import type { Session } from "../gen/jcodeweb/v1/jcodeweb_pb";
  import { layout } from "../lib/layout.svelte";

  let { session, x, y, onpick, onhide, onclose }: {
    session: Session; x: number; y: number;
    onpick: (id: string, event?: MouseEvent) => void;
    onhide: (id: string) => void;
    onclose: () => void;
  } = $props();
  let error = $state("");

  function setup(node: HTMLDivElement) {
    const previous = document.activeElement;
    const outside = (event: PointerEvent) => { if (!event.composedPath().includes(node)) onclose(); };
    document.addEventListener("pointerdown", outside, true);
    node.showPopover();
    node.querySelector<HTMLButtonElement>("button")?.focus();
    return { destroy() {
      document.removeEventListener("pointerdown", outside, true);
      if (node.contains(document.activeElement) && previous instanceof HTMLElement) previous.focus();
    } };
  }

  function pick(modifier?: "split" | "replace") {
    onpick(session.id, new MouseEvent("click", { ctrlKey: modifier === "split", shiftKey: modifier === "replace" }));
    onclose();
  }

  function search() {
    onpick(session.id);
    layout.searchSession = session.id;
    onclose();
  }

  async function copy() {
    try { await navigator.clipboard.writeText(session.id); onclose(); }
    catch { error = "Could not copy the session ID."; }
  }

  function navigate(event: KeyboardEvent) {
    if (event.key === "Escape") { event.preventDefault(); onclose(); return; }
    if (!["ArrowDown", "ArrowUp", "Home", "End"].includes(event.key)) return;
    event.preventDefault();
    const items = [...(event.currentTarget as HTMLElement).querySelectorAll<HTMLButtonElement>("button")];
    const current = items.indexOf(document.activeElement as HTMLButtonElement);
    const index = event.key === "Home" ? 0 : event.key === "End" ? items.length - 1
      : (current + (event.key === "ArrowDown" ? 1 : -1) + items.length) % items.length;
    items[index]?.focus();
  }
</script>

<div class="session-menu" popover="manual" use:setup role="menu" tabindex="-1" aria-label={`Actions for ${session.name}`} onkeydown={navigate}
  style:left={`${Math.max(8, Math.min(x, innerWidth - 244))}px`} style:top={`${Math.max(8, Math.min(y, innerHeight - 330))}px`}>
  <div class="caption">{session.name}</div>
  <button role="menuitem" onclick={() => pick()}>Open in tab</button>
  <button role="menuitem" onclick={() => pick("split")}>Open in split</button>
  <button role="menuitem" onclick={() => pick("replace")}>Replace focused session</button>
  <button role="menuitem" onclick={search}>Search this session</button>
  <button role="menuitem" onclick={copy}>Copy session ID</button>
  <hr />
  <button role="menuitem" onclick={() => onhide(session.id)}>Close and hide</button>
  <p>Keeps running. Click it under quiet sessions to restore it.</p>
  {#if error}<p class="error" role="alert">{error}</p>{/if}
</div>

<style>
  .session-menu { position: fixed; inset: auto; margin: 0; width: 236px; max-height: calc(100dvh - 16px); overflow-y: auto; padding: 0.35rem; border: 1px solid var(--line); border-radius: 6px; background: var(--ink-2); color: var(--fg); box-shadow: 0 8px 24px #0006; font-size: var(--s-1); }
  .caption { padding: 0.25rem 0.5rem; color: var(--fg-3); font-weight: 600; }
  button { display: block; width: 100%; text-align: left; padding: 0.4rem 0.5rem; border-radius: 4px; }
  button:hover, button:focus-visible { background: var(--ink-3); outline: 1px solid var(--focus); }
  hr { border: 0; border-top: 1px solid var(--line); margin: 0.3rem 0; }
  p { margin: 0.25rem 0.5rem; color: var(--fg-3); font-size: 11px; }
  .error { color: var(--failed); }
</style>
