<script lang="ts">
  import { sessions } from "../lib/client";
  import type { ScheduledTask } from "../gen/jcodeweb/v1/jcodeweb_pb";

  let { id }: { id: string } = $props();
  let tasks = $state<ScheduledTask[]>([]);
  let error = $state("");
  let loaded = $state(false);
  let open = $state(false);
  let now = $state(Date.now());

  $effect(() => {
    const sessionId = id;
    const controller = new AbortController();
    let stopped = false;
    let timer: ReturnType<typeof setTimeout>;
    tasks = [];
    error = "";
    loaded = false;
    async function refresh() {
      try {
        const result = await sessions.listScheduledTasks({ sessionId }, { signal: controller.signal, timeoutMs: 8000 });
        if (stopped) return;
        tasks = result.tasks;
        error = "";
        loaded = true;
      } catch (e) {
        if (stopped) return;
        error = e instanceof Error ? e.message : String(e);
      }
      if (!stopped) timer = setTimeout(refresh, 10000);
    }
    void refresh();
    const clock = setInterval(() => { now = Date.now(); }, 1000);
    return () => { stopped = true; controller.abort(); clearTimeout(timer); clearInterval(clock); };
  });

  function countdown(due: string, current: number) {
    const seconds = Math.ceil((Date.parse(due) - current) / 1000);
    if (seconds <= 0) return "Due now, awaiting delivery";
    const days = Math.floor(seconds / 86400);
    const hours = Math.floor((seconds % 86400) / 3600);
    const minutes = Math.floor((seconds % 3600) / 60);
    const rest = seconds % 60;
    return `in ${days ? `${days}d ` : ""}${hours || days ? `${hours}h ` : ""}${minutes || hours || days ? `${minutes}m ` : ""}${rest}s`;
  }
</script>

{#if tasks.length || error}
<details class="scheduled" bind:open>
  <summary>Scheduled tasks{#if loaded} <span>({tasks.length} pending)</span>{/if}{#if error} <span class="error">unavailable</span>{/if}</summary>
  {#if error}<p class="error" role="status">Could not refresh scheduled tasks: {error}. Retrying automatically.{tasks.length ? " Showing last successful result." : ""}</p>{/if}
  {#if open}
    {#if !loaded && !error}<p>Loading scheduled tasks…</p>{/if}
    {#if loaded && !tasks.length && !error}<p>No pending scheduled tasks for this session.</p>{/if}
    {#if tasks.length}
      <ul>
        {#each tasks as task (task.id)}
          <li>
            <p class="task">{task.task || "Scheduled task"}</p>
            <div class="due"><time datetime={task.scheduledFor}>{new Date(task.scheduledFor).toLocaleString()}</time><strong>{countdown(task.scheduledFor, now)}</strong></div>
            {#if task.target === "spawn"}<small>Runs in a new session</small>{/if}
          </li>
        {/each}
      </ul>
    {/if}
  {/if}
</details>
{/if}

<style>
  .scheduled { flex: 0 0 auto; border-bottom: 1px solid var(--line); padding: 7px 14px; font-size: 12px; max-height: 30%; overflow: auto; }
  summary { cursor: pointer; color: var(--text); }
  summary span, p, small { color: var(--muted); }
  p { margin: 7px 0; }
  ul { list-style: none; padding: 0; margin: 0; }
  li { border-top: 1px solid var(--line); padding: 5px 0; }
  .task { color: var(--text); white-space: pre-wrap; overflow-wrap: anywhere; }
  .due { display: flex; flex-wrap: wrap; gap: 5px 12px; color: var(--muted); }
  strong { font-weight: 500; color: var(--text); }
  .error, summary .error { color: var(--warn, #e6ae67); overflow-wrap: anywhere; }
</style>
