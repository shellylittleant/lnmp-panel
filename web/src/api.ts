export function panelPrefix(): string {
  const segs = window.location.pathname.split("/").filter(Boolean);
  return segs.length ? "/" + segs[0] : "";
}

let csrf = "";

export function setCsrf(token: string) {
  csrf = token;
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
  if (res.status === 404 && !res.headers.get("content-type")?.includes("json")) {
    return { ok: false, error: { code: "not_found", message: "接口不存在" } };
  }
  return (await res.json()) as Envelope<T>;
}
