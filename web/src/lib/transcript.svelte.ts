import type { AttachResponse, HistoryMessage } from "../gen/jcodeweb/v1/jcodeweb_pb";
import { sessions as api } from "./client";

export type Block =
  | { kind: "user"; text: string; source?: string }
  | { kind: "assistant"; text: string; live?: boolean }
  | { kind: "reasoning"; text: string; live?: boolean }
  | { kind: "tool"; id: string; name: string; input: string; intent: string; output: string; error: string; state: "input" | "running" | "done" }
  | { kind: "system"; text: string }
  | { kind: "error"; text: string };

/** Transcript is the live view of one attached session. */
export class Transcript {
  blocks = $state<Block[]>([]);
  total = $state(0);
  status = $state("");
  attached = $state(false);
  error = $state("");
  busy = $state(false);

  #abort: AbortController | null = null;

  constructor(public sessionId: string, public historyLimit = 200) {}

  open() {
    this.close();
    const ac = new AbortController();
    this.#abort = ac;
    void this.#run(ac.signal);
  }

  close() {
    this.#abort?.abort();
    this.#abort = null;
  }

  async #run(signal: AbortSignal) {
    let backoff = 500;
    while (!signal.aborted) {
      try {
        for await (const res of api.attach({ sessionId: this.sessionId, historyLimit: this.historyLimit }, { signal })) {
          this.attached = true;
          this.error = "";
          backoff = 500;
          this.#apply(res);
        }
      } catch (e) {
        if (signal.aborted) return;
        this.error = e instanceof Error ? e.message : String(e);
      }
      this.attached = false;
      await new Promise((r) => setTimeout(r, backoff));
      backoff = Math.min(backoff * 2, 15000);
    }
  }

  #last<K extends Block["kind"]>(kind: K): Extract<Block, { kind: K }> | undefined {
    const b = this.blocks[this.blocks.length - 1];
    return b && b.kind === kind ? (b as Extract<Block, { kind: K }>) : undefined;
  }

  #tool(id: string) {
    for (let i = this.blocks.length - 1; i >= 0; i--) {
      const b = this.blocks[i];
      if (b.kind === "tool" && b.id === id) return b;
    }
    return undefined;
  }

  #settle() {
    for (const b of this.blocks) if ((b.kind === "assistant" || b.kind === "reasoning") && b.live) b.live = false;
  }

  #apply(res: AttachResponse) {
    const u = res.update;
    switch (u.case) {
      case "history":
        this.total = u.value.total;
        this.blocks = fromHistory(u.value.messages);
        break;
      case "textDelta": {
        this.busy = true;
        const b = this.#last("assistant");
        if (b?.live) b.text += u.value.text;
        else this.blocks.push({ kind: "assistant", text: u.value.text, live: true });
        break;
      }
      case "textReplace": {
        const b = this.#last("assistant");
        if (b) b.text = u.value.text;
        break;
      }
      case "reasoningDelta": {
        this.busy = true;
        const b = this.#last("reasoning");
        if (b?.live) b.text += u.value.text;
        else this.blocks.push({ kind: "reasoning", text: u.value.text, live: true });
        break;
      }
      case "toolStart":
        this.busy = true;
        this.#settle();
        this.blocks.push({ kind: "tool", id: u.value.id, name: u.value.name, input: "", intent: "", output: "", error: "", state: "input" });
        break;
      case "toolInput": {
        const t = this.#tool(u.value.id) ?? lastTool(this.blocks);
        if (t) {
          t.input += u.value.delta;
          const m = /"intent"\s*:\s*"((?:[^"\\]|\\.)*)"/.exec(t.input);
          if (m) t.intent = m[1];
        }
        break;
      }
      case "toolExec": {
        const t = this.#tool(u.value.id);
        if (t) t.state = "running";
        break;
      }
      case "toolDone": {
        const t = this.#tool(u.value.id);
        if (t) {
          t.output = u.value.output;
          t.error = u.value.error;
          t.state = "done";
        }
        break;
      }
      case "turnDone":
      case "interrupted":
        this.#settle();
        this.busy = false;
        this.status = "";
        if (u.case === "interrupted") this.blocks.push({ kind: "system", text: "Interrupted" });
        break;
      case "statusDetail":
        this.status = u.value.detail;
        break;
      case "userMessage":
        this.#settle();
        this.blocks.push({ kind: "user", text: u.value.content, source: u.value.source });
        break;
      case "error":
        this.blocks.push({ kind: "error", text: u.value.message });
        break;
      case "detached":
        this.attached = false;
        break;
      case "raw":
        if (u.value.type === "text_done" || u.value.type === "reasoning_done") this.#settle();
        break;
    }
  }
}

function lastTool(blocks: Block[]) {
  for (let i = blocks.length - 1; i >= 0; i--) {
    const b = blocks[i];
    if (b.kind === "tool") return b;
  }
}

function fromHistory(msgs: HistoryMessage[]): Block[] {
  const out: Block[] = [];
  for (const m of msgs) {
    if (m.role === "tool") {
      out.push({
        kind: "tool",
        id: m.tool?.id ?? "",
        name: m.tool?.name ?? "tool",
        input: m.tool?.inputJson ?? "",
        intent: m.tool?.intent ?? "",
        output: m.content,
        error: "",
        state: "done",
      });
    } else if (m.role === "user") out.push({ kind: "user", text: m.content });
    else if (m.role === "assistant") out.push({ kind: "assistant", text: m.content });
    else if (m.role === "error") out.push({ kind: "error", text: m.content });
    else out.push({ kind: "system", text: m.content });
  }
  return out;
}
