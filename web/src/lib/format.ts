import { marked } from "marked";
import DOMPurify from "dompurify";

marked.setOptions({ gfm: true, breaks: false });

export function md(text: string): string {
  return DOMPurify.sanitize(marked.parse(text, { async: false }) as string);
}

export function shortDir(dir: string): string {
  const d = dir.replace(/\/+$/, "").replace(/^\/home\/[^/]+/, "~").replace(/^~\/repo\/github\.com\//, "");
  const parts = d.split("/");
  return parts.length <= 3 ? d : parts.slice(-2).join("/");
}

export function ago(unix: bigint | number): string {
  const t = Number(unix);
  if (!t) return "";
  const s = Math.max(0, Math.floor(Date.now() / 1000 - t));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

export function tokens(n: bigint | number): string {
  const v = Number(n);
  if (v >= 1e6) return `${(v / 1e6).toFixed(1)}M`;
  if (v >= 1e3) return `${Math.round(v / 1e3)}k`;
  return String(v);
}

export type Tone = "busy" | "ready" | "failed" | "idle";

export function tone(status: string, processing: boolean): Tone {
  if (processing || status === "running") return "busy";
  if (status === "failed") return "failed";
  if (status === "ready" || status === "completed") return "ready";
  return "idle";
}
