export function panelPrefix(): string {
  const segs = window.location.pathname.split("/").filter(Boolean);
  return segs.length ? "/" + segs[0] : "";
}

let csrf = "";
let onUnauthorized: (() => void) | null = null;

export function setCsrf(token: string) {
  csrf = token;
}

export function setUnauthorized(fn: () => void) {
  onUnauthorized = fn;
}

function noteStatus(res: Response) {
  if (res.status === 401) {
    csrf = "";
    onUnauthorized?.();
  }
}

export type Envelope<T> = { ok: true; data: T } | { ok: false; error: { code: string; message: string } };

export async function api<T>(method: string, path: string, body?: unknown): Promise<Envelope<T>> {
  const headers: Record<string, string> = {};
  if (body !== undefined) headers["Content-Type"] = "application/json";
  if (csrf && method !== "GET") headers["X-CSRF-Token"] = csrf;
  const res = await fetch(panelPrefix() + "/api/v1" + path, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
    credentials: "include",
  });
  noteStatus(res);
  if (res.status === 404 && !res.headers.get("content-type")?.includes("json")) {
    return { ok: false, error: { code: "not_found", message: "接口不存在" } };
  }
  return (await res.json()) as Envelope<T>;
}

export async function downloadSQL(path: string, filename: string): Promise<{ ok: true } | { ok: false; error: { message: string } }> {
  const headers: Record<string, string> = { "Content-Type": "application/json" };
  if (csrf) headers["X-CSRF-Token"] = csrf;
  const res = await fetch(panelPrefix() + "/api/v1" + path, {
    method: "POST",
    headers,
    body: "{}",
    credentials: "include",
  });
  noteStatus(res);
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/sql") || ct.includes("octet-stream")) {
    const blob = await res.blob();
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    return { ok: true };
  }
  const j = (await res.json()) as Envelope<unknown>;
  if (!j.ok) return { ok: false, error: { message: j.error.message } };
  return { ok: false, error: { message: "导出失败" } };
}

export async function downloadPluginFile(path: string, filename: string): Promise<{ ok: true } | { ok: false; error: { message: string } }> {
  const headers: Record<string, string> = {};
  if (csrf) headers["X-CSRF-Token"] = csrf;
  const res = await fetch(panelPrefix() + "/api/v1" + path, { method: "GET", headers, credentials: "include" });
  noteStatus(res);
  const ct = res.headers.get("content-type") || "";
  if (res.ok && !ct.includes("application/json")) {
    const blob = await res.blob();
    const a = document.createElement("a");
    a.href = URL.createObjectURL(blob);
    a.download = filename;
    a.click();
    return { ok: true };
  }
  const j = (await res.json()) as Envelope<unknown>;
  if (!j.ok) return { ok: false, error: { message: j.error.message } };
  return { ok: false, error: { message: "下载失败" } };
}

export async function apiForm<T>(path: string, form: FormData): Promise<Envelope<T>> {
  const headers: Record<string, string> = {};
  if (csrf) headers["X-CSRF-Token"] = csrf;
  const res = await fetch(panelPrefix() + "/api/v1" + path, {
    method: "POST",
    headers,
    body: form,
    credentials: "include",
  });
  noteStatus(res);
  return (await res.json()) as Envelope<T>;
}

export async function waitJob(id: string, onTick?: (phase: string, detail: string) => void): Promise<void> {
  for (let i = 0; ; i++) {
    const r = await api<{ phase: string; detail: string }>("GET", "/jobs/" + id);
    if (!r.ok) throw new Error(r.error.message);
    let detail = r.data.detail;
    if (i >= 180 && r.data.phase !== "done" && r.data.phase !== "failed") {
      detail = detail ? detail + "；可能仍在执行" : "可能仍在执行";
    }
    onTick?.(r.data.phase, detail);
    if (r.data.phase === "done") return;
    if (r.data.phase === "failed") throw new Error(r.data.detail || "任务失败");
    await new Promise((x) => setTimeout(x, 2000));
  }
}
