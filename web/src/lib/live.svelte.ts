import { fromJson, toJson } from "@bufbuild/protobuf";
import { SessionSchema, type Event, type Session } from "../gen/jcodeweb/v1/jcodeweb_pb";
import { sessions as api } from "./client";

const MAX_EVENTS = 2000;

class Live {
  sessions = $state<Session[]>([]);
  #known = $state<Session[]>([]);

  constructor() {
    try {
      const saved = JSON.parse(localStorage.getItem("jw.searchSessions") ?? "[]");
      if (Array.isArray(saved)) this.#known = saved.slice(0, 100).map(value => fromJson(SessionSchema, value));
    } catch { /* Search results remain usable without stored metadata. */ }
  }

  remember(session: Session) {
    this.#known = [session, ...this.#known.filter(s => s.id !== session.id)].slice(0, 100);
    try { localStorage.setItem("jw.searchSessions", JSON.stringify(this.#known.map(s => toJson(SessionSchema, s)))); }
    catch { /* Opening a result does not require storage. */ }
  }
  events = $state<Event[]>([]);
  connected = $state(false);
  upstreamError = $state("");
  streaming = $state(false);

  #lastId = 0n;
  #abort: AbortController | null = null;

  get topLevelSessions(): Session[] {
    return this.sessions.filter(s => !s.parentSessionId && !s.isHeadless);
  }

  byId(id: string): Session | undefined {
    return this.sessions.find((s) => s.id === id) ?? this.#known.find((s) => s.id === id);
  }

  start() {
    if (this.#abort) return;
    this.#abort = new AbortController();
    void this.#loop(this.#abort.signal);
    document.addEventListener("visibilitychange", () => {
      // Phones suspend streams in the background. Reconnect on return.
      if (document.visibilityState === "visible") this.#kick();
    });
  }

  #kick() {
    this.#abort?.abort();
    this.#abort = new AbortController();
    void this.#loop(this.#abort.signal);
  }

  async #loop(signal: AbortSignal) {
    let backoff = 500;
    if (this.#lastId === 0n) await this.#backfill();
    while (!signal.aborted) {
      try {
        this.streaming = true;
        for await (const res of api.watch({ afterId: this.#lastId }, { signal })) {
          backoff = 500;
          const u = res.update;
          if (u.case === "sessions") this.sessions = u.value.sessions;
          else if (u.case === "connection") {
            this.connected = u.value.connected;
            this.upstreamError = u.value.error;
          } else if (u.case === "event") this.#push(u.value);
        }
      } catch {
        if (signal.aborted) return;
      }
      this.streaming = false;
      await new Promise((r) => setTimeout(r, backoff));
      backoff = Math.min(backoff * 2, 15000);
    }
  }

  async #backfill() {
    try {
      const res = await api.listEvents({ limit: 300 });
      const evs = res.events.slice().reverse();
      this.events = evs;
      if (evs.length) this.#lastId = evs[evs.length - 1].id;
    } catch {
      // Watch will retry.
    }
  }

  #push(e: Event) {
    if (e.id <= this.#lastId) return;
    this.#lastId = e.id;
    this.events.push(e);
    if (this.events.length > MAX_EVENTS) this.events.splice(0, this.events.length - MAX_EVENTS);
  }
}

export const live = new Live();
