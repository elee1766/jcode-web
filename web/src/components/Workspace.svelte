<script lang="ts">
  import { mount, unmount, untrack } from "svelte";
  import {
    createDockview, themeDark, positionToDirection,
    type DockviewApi, type AddPanelPositionOptions, type SerializedDockview,
  } from "dockview";
  import "dockview/dist/styles/dockview.css";
  import { layout, PROGRESS_WINDOW, SESSION_DRAG_TYPE } from "../lib/layout.svelte";
  import { live } from "../lib/live.svelte";
  import Pane from "./Pane.svelte";
  import ProgressFeed from "./ProgressFeed.svelte";

  let dock = $state.raw<DockviewApi>();
  let maximized = $state(false);
  let syncing = false;
  let disposed = false;

  function title(id: string) {
    return id === PROGRESS_WINDOW ? "Progress feed" : live.byId(id)?.name ?? id;
  }

  function addPanel(api: DockviewApi, id: string, position?: AddPanelPositionOptions) {
    if (!position) {
      const largest = [...api.groups].sort((a, b) => b.width * b.height - a.width * a.height)[0];
      if (largest) position = { referenceGroup: largest, direction: largest.width > largest.height ? "right" : "below" };
    }
    return api.addPanel({
      id, component: "session", title: title(id), position, inactive: true,
      minimumWidth: 0, minimumHeight: 0,
    });
  }

  // The hash owns membership. Dockview owns split sizes, grouping, and tab order.
  function sync(api: DockviewApi, ids: string[], focused: string) {
    syncing = true;
    try {
      const removed = api.panels.filter(p => !ids.includes(p.id));
      const added = ids.filter(id => !api.getPanel(id));
      for (const [i, id] of added.entries()) {
        const replacement = removed[i];
        addPanel(api, id, replacement ? {
          referencePanel: replacement, direction: "within", index: replacement.group.panels.indexOf(replacement),
        } : undefined);
      }
      for (const panel of removed) api.removePanel(panel);
      for (const panel of api.panels) {
        const next = title(panel.id);
        if (panel.title !== next) panel.api.setTitle(next);
      }
      const active = api.getPanel(focused);
      if (active && api.activePanel?.id !== focused) {
        if (maximized && !active.api.isMaximized()) api.exitMaximizedGroup();
        active.api.setActive();
      }
      layout.saveDock(api.toJSON());
    } finally { syncing = false; }
  }

  function restore(api: DockviewApi) {
    const saved = layout.readDock();
    if (!saved) return;
    try {
      type Node = SerializedDockview["grid"]["root"];
      function prune(node: Node): Node | undefined {
        if (node.type === "branch" && Array.isArray(node.data)) {
          const children = node.data.map(prune).filter((n): n is Node => !!n);
          return children.length ? { ...node, data: children } : undefined;
        }
        if (node.type !== "leaf" || Array.isArray(node.data)) return;
        const views = node.data.views.filter(id => layout.panes.includes(id));
        if (!views.length) return;
        return { ...node, data: { ...node.data, views, activeView: views.includes(node.data.activeView ?? "") ? node.data.activeView : views[0] } };
      }
      const root = prune(saved.grid.root);
      if (!root) return;
      const panels = Object.fromEntries(layout.panes.filter(id => saved.panels[id]).map(id => [id, {
        id, contentComponent: "session", title: title(id), renderer: "always" as const,
        minimumWidth: 0, minimumHeight: 0,
      }]));
      // Only restore the docked grid, never floating or popout groups.
      api.fromJSON({ grid: { ...saved.grid, root }, panels, activeGroup: saved.activeGroup });
    } catch {
      api.clear();
    }
  }

  function initDock(node: HTMLDivElement) {
    disposed = false;
    const api = createDockview(node, {
      theme: { ...themeDark, gap: 4 },
      disableFloatingGroups: true,
      defaultRenderer: "always",
      createComponent: ({ id }) => {
        const element = document.createElement("div");
        element.className = "workspace-panel";
        element.dataset.panelId = id;
        let component: ReturnType<typeof mount>;
        return {
          element,
          init() {
            component = id === PROGRESS_WINDOW
              ? mount(ProgressFeed, { target: element })
              : mount(Pane, { target: element, props: { id } });
          },
          dispose() { if (component) void unmount(component); },
        };
      },
      createWatermarkComponent: () => {
        const element = document.createElement("div");
        element.className = "workspace-empty";
        element.textContent = "Drag a session here, or open the Progress feed. Drop at an edge to split, or in the center to add a tab.";
        return { element, init() {} };
      },
    });
    const subscriptions = [
      api.onDidActivePanelChange(({ panel }) => {
        if (!syncing && panel && layout.panes.includes(panel.id)) layout.focus(panel.id);
      }),
      api.onDidRemovePanel(panel => {
        // Moves can remove and re-add a panel within the same mutation.
        queueMicrotask(() => {
          if (!disposed && !api.getPanel(panel.id) && layout.panes.includes(panel.id)) layout.close(panel.id);
        });
      }),
      api.onDidLayoutChange(() => { if (!disposed && !syncing) layout.saveDock(api.toJSON()); }),
      api.onDidMaximizedGroupChange(event => { maximized = event.isMaximized; }),
      api.onUnhandledDragOver(event => {
        if (event.nativeEvent instanceof DragEvent && event.nativeEvent.dataTransfer?.types.includes(SESSION_DRAG_TYPE)) event.accept();
      }),
      api.onWillDrop(event => {
        if (!(event.nativeEvent instanceof DragEvent)) return;
        const id = event.nativeEvent.dataTransfer?.getData(SESSION_DRAG_TYPE);
        if (!id || !live.byId(id)) return;
        event.preventDefault();
        syncing = true;
        try {
          layout.open(id);
          const existing = api.getPanel(id);
          const index = event.position === "center" && event.panel ? event.group?.panels.indexOf(event.panel) : undefined;
          if (existing) {
            const group = event.group ?? (event.position !== "center"
              ? api.addGroup({ direction: positionToDirection(event.position) }) : existing.group);
            existing.api.moveTo({ group, position: event.group ? event.position : "center", index });
          } else {
            addPanel(api, id, event.group
              ? { referenceGroup: event.group, direction: positionToDirection(event.position), index }
              : event.position !== "center" ? { direction: positionToDirection(event.position) } : undefined);
          }
          api.getPanel(id)?.api.setActive();
          layout.saveDock(api.toJSON());
        } finally { syncing = false; }
      }),
    ];
    syncing = true;
    untrack(() => { restore(api); sync(api, [...layout.panes], layout.focused); });
    dock = api;
    return { destroy() {
      disposed = true;
      layout.saveDock(api.toJSON());
      subscriptions.forEach(s => s.dispose());
      dock = undefined;
      api.dispose();
    } };
  }

  $effect(() => {
    const api = dock;
    const ids = [...layout.panes];
    const focused = layout.focused;
    // Keep Dockview tab titles current as discovery updates session metadata.
    for (const id of ids) title(id);
    if (api) untrack(() => sync(api, ids, focused));
  });

  function toggleMaximized() {
    if (maximized) dock?.exitMaximizedGroup();
    else dock?.activePanel?.api.maximize();
  }
</script>

<div class="workspace">
  <div class="tools">
    <span>{layout.panes.length ? `${layout.panes.length} panels` : "Session workspace"}</span>
    <span class="help">Drag tabs to split or group. Resize shared dividers.</span>
    <button onclick={() => layout.open(PROGRESS_WINDOW)}>Progress feed</button>
    <button onclick={toggleMaximized} disabled={!layout.panes.length}>{maximized ? "Restore tiles" : "Maximize tile"}</button>
  </div>
  <div class="dock" use:initDock></div>
</div>

<style>
  .workspace { flex: 1; display: flex; flex-direction: column; min-width: 0; min-height: 0; overflow: hidden; }
  .tools { display: flex; align-items: center; gap: 0.75rem; min-height: 38px; padding: 0.4rem 0.75rem; border-bottom: 1px solid var(--line); color: var(--fg-2); font-size: var(--s-1); }
  .help { flex: 1; color: var(--fg-3); overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
  .tools button { flex: none; padding: 0.2rem 0.5rem; border: 1px solid var(--line); border-radius: 4px; }
  .tools button:disabled { opacity: 0.4; }
  .dock { flex: 1; min-height: 0; min-width: 0; overflow: hidden; }
  .dock :global(.workspace-panel) { display: flex; height: 100%; width: 100%; min-width: 0; min-height: 0; overflow: hidden; }
  .dock :global(.workspace-empty) { max-width: 30rem; padding: 2rem; margin: auto; color: var(--fg-3); font-size: var(--s0); }
  .dock :global(.dockview-theme-dark) {
    --dv-background-color: var(--line);
    --dv-group-view-background-color: var(--ink);
    --dv-tabs-and-actions-container-background-color: var(--ink-2);
    --dv-activegroup-visiblepanel-tab-background-color: var(--ink-3);
    --dv-activegroup-hiddenpanel-tab-background-color: var(--ink-2);
    --dv-inactivegroup-visiblepanel-tab-background-color: var(--ink-2);
    --dv-inactivegroup-hiddenpanel-tab-background-color: var(--ink-2);
    --dv-activegroup-visiblepanel-tab-color: var(--fg);
    --dv-activegroup-hiddenpanel-tab-color: var(--fg-2);
    --dv-inactivegroup-visiblepanel-tab-color: var(--fg-2);
    --dv-inactivegroup-hiddenpanel-tab-color: var(--fg-3);
    --dv-tab-divider-color: var(--line);
    --dv-separator-border: var(--line);
    --dv-active-sash-color: var(--focus);
    --dv-drag-over-background-color: #8fb3ff33;
    --dv-drag-over-border: 1px solid var(--focus);
    --dv-tabs-and-actions-container-font-size: var(--s-1);
  }
</style>
