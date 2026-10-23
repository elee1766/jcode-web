// Panes in the multiplex, persisted in the URL hash: #/s/<id>,<id>
class Layout {
  panes = $state<string[]>([]);
  focused = $state<string>("");
  // Fork origin for btw panes: child id -> parent id.
  forks = $state<Record<string, string>>({});

  constructor() {
    this.#read();
    addEventListener("hashchange", () => this.#read());
    try {
      this.forks = JSON.parse(localStorage.getItem("jw.forks") ?? "{}");
    } catch {
      this.forks = {};
    }
  }

  #read() {
    const m = /^#\/s\/(.*)$/.exec(location.hash);
    const ids = m ? m[1].split(",").filter(Boolean).map(decodeURIComponent) : [];
    if (ids.join(",") !== this.panes.join(",")) this.panes = ids;
    if (!this.panes.includes(this.focused)) this.focused = this.panes[0] ?? "";
  }

  #write() {
    const h = this.panes.length ? "#/s/" + this.panes.map(encodeURIComponent).join(",") : "#/";
    if (location.hash !== h) history.replaceState(null, "", h);
  }

  open(id: string, opts: { after?: string } = {}) {
    if (!this.panes.includes(id)) {
      const i = opts.after ? this.panes.indexOf(opts.after) : -1;
      const next = [...this.panes];
      next.splice(i >= 0 ? i + 1 : next.length, 0, id);
      this.panes = next;
    }
    this.focused = id;
    this.#write();
  }

  // swap replaces pane `from` with `to`, or opens `to` when there is no pane.
  swap(from: string, to: string) {
    const i = this.panes.indexOf(from);
    if (i < 0) return this.open(to);
    const next = [...this.panes];
    next[i] = to;
    this.panes = next;
    this.focused = to;
    this.#write();
  }

  close(id: string) {
    this.panes = this.panes.filter((p) => p !== id);
    if (this.focused === id) this.focused = this.panes[this.panes.length - 1] ?? "";
    this.#write();
  }

  markFork(child: string, parent: string) {
    this.forks = { ...this.forks, [child]: parent };
    localStorage.setItem("jw.forks", JSON.stringify(this.forks));
  }
}

export const layout = new Layout();
