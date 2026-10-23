import { pushApi } from "./client";

function b64(s: string): Uint8Array<ArrayBuffer> {
  const pad = "=".repeat((4 - (s.length % 4)) % 4);
  const raw = atob((s + pad).replace(/-/g, "+").replace(/_/g, "/"));
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

class Push {
  supported = $state(typeof navigator !== "undefined" && "serviceWorker" in navigator && "PushManager" in window);
  enabled = $state(false);
  error = $state("");

  async refresh() {
    if (!this.supported) return;
    const reg = await navigator.serviceWorker.ready;
    this.enabled = !!(await reg.pushManager.getSubscription());
  }

  async enable() {
    this.error = "";
    try {
      if ((await Notification.requestPermission()) !== "granted") {
        this.error = "Notifications are blocked for this site. Allow them in browser settings.";
        return;
      }
      const { publicKey } = await pushApi.getVapidKey({});
      const reg = await navigator.serviceWorker.ready;
      const sub = await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: b64(publicKey) });
      const j = sub.toJSON();
      await pushApi.subscribe({
        endpoint: sub.endpoint,
        p256dh: j.keys?.p256dh ?? "",
        auth: j.keys?.auth ?? "",
        userAgent: navigator.userAgent,
      });
      this.enabled = true;
    } catch (e) {
      this.error = e instanceof Error ? e.message : String(e);
    }
  }

  async disable() {
    const reg = await navigator.serviceWorker.ready;
    const sub = await reg.pushManager.getSubscription();
    if (sub) {
      await pushApi.unsubscribe({ endpoint: sub.endpoint }).catch(() => {});
      await sub.unsubscribe();
    }
    this.enabled = false;
  }

  async test() {
    const r = await pushApi.test({});
    if (r.sent === 0) this.error = "No device received the test. Turn notifications off and on again.";
  }
}

export const push = new Push();
