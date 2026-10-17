// The dashboard talks only to /api, which sevjala ui relays to the agent;
// every change carries the header the relay asks for.

export class ConfirmNeeded extends Error {
  constructor(body) {
    super(body.error)
    this.word = body.word
    this.deletes = body.deletes ?? 0
    this.hosts = body.hosts ?? []
  }
}

export function errorFrom(status, body) {
  if (body.code === 'confirm_required') return new ConfirmNeeded(body)
  if (body.code === 'admin_required') return new Error(`${body.error}: run it from an administrator prompt`)
  return new Error(body.error || `HTTP ${status}`)
}

export async function api(method, path, body) {
  const opts = { method, headers: {} }
  if (method !== 'GET') opts.headers['X-Sevjala-UI'] = '1'
  if (body !== undefined) {
    opts.headers['Content-Type'] = 'application/json'
    opts.body = JSON.stringify(body)
  }
  const r = await fetch('/api' + path, opts)
  const v = await r.json().catch(() => ({}))
  if (!r.ok) throw errorFrom(r.status, v)
  return v
}

export const get = (path) => api('GET', path)

// fetchFile downloads path into a Blob, so nothing is saved when the agent
// answers with an error instead of the file.
export async function fetchFile(path) {
  const r = await fetch('/api' + path)
  if (!r.ok) throw errorFrom(r.status, await r.json().catch(() => ({})))
  return r.blob()
}

// every runs fn now and then every ms while the page is visible; it returns
// the stop function an $effect wants.
export function every(ms, fn) {
  fn()
  const t = setInterval(() => document.visibilityState === 'visible' && fn(), ms)
  return () => clearInterval(t)
}
