import type { SerializedDockview } from "dockview";

export const PROGRESS_WINDOW = "~progress";
export const SESSION_DRAG_TYPE = "application/x-jcode-session";

class Layout {
  panes = $state<string[]>([]);
  focused = $state("");
  #tabTargets = new Map<string, string>();
  forks = $state<Record<string, string>>({});

  constructor() {
    try {
      this.forks = JSON.parse(localStorage.getItem("jw.forks") ?? "{}");
      this.focused = localStorage.getItem("jw.focused") ?? "";
    } catch { /* Layout still works without storage. */ }
    this.#read();
    addEventListener("hashchange", () => this.#read());
  }

  #read() {
    const m = /^#\/s\/(.*)$/.exec(location.hash);
    let ids: string[] = [];
    try { ids = m ? [...new Set(m[1].split(",").filter(Boolean).map(decodeURIComponent))] : []; } catch { /* Ignore invalid links. */ }
    if (ids.join(",") !== this.panes.join(",")) this.panes = ids;
    if (!this.panes.includes(this.focused)) this.focus(this.panes[0] ?? "");
  }

  #write() {
    const h = this.panes.length ? "#/s/" + this.panes.map(encodeURIComponent).join(",") : "#/";
    if (location.hash !== h) history.replaceState(null, "", h);
  }

  focus(id: string) {
    if (this.focused === id) return;
    this.focused = id;
    try { localStorage.setItem("jw.focused", id); } catch { /* Focus still works without storage. */ }
  }

  readDock(): SerializedDockview | undefined {
    try { return JSON.parse(localStorage.getItem("jw.dockview") ?? "null") ?? undefined; }
    catch { return undefined; }
  }

  saveDock(value: SerializedDockview) {
    try { localStorage.setItem("jw.dockview", JSON.stringify(value)); } catch { /* Docking still works without storage. */ }
  }

  takeTabTarget(id: string): string | undefined {
    const target = this.#tabTargets.get(id);
    this.#tabTargets.delete(id);
    return target;
  }

  open(id: string, opts: { after?: string; tabWith?: string } = {}) {
    if (!this.panes.includes(id)) {
      if (opts.tabWith) this.#tabTargets.set(id, opts.tabWith);
      const i = opts.after ? this.panes.indexOf(opts.after) : -1;
      const next = [...this.panes];
      next.splice(i >= 0 ? i + 1 : next.length, 0, id);
      this.panes = next;
    }
    this.focus(id);
    this.#write();
  }

  swap(from: string, to: string) {
    if (this.panes.includes(to)) { this.focus(to); return; }
    if (!this.panes.includes(from)) return this.open(to);
    this.panes = this.panes.map(p => p === from ? to : p);
    this.focus(to);
    this.#write();
  }

  close(id: string) {
    this.#tabTargets.delete(id);
    this.panes = this.panes.filter(p => p !== id);
    if (this.focused === id) this.focus(this.panes[this.panes.length - 1] ?? "");
    this.#write();
  }

  markFork(child: string, parent: string) {
    this.forks = { ...this.forks, [child]: parent };
    localStorage.setItem("jw.forks", JSON.stringify(this.forks));
  }
}

export const layout = new Layout();
