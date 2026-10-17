// live holds what /events last said. lost is set when sevjala ui itself is
// gone (the tab outlived it, or a new sevjala ui started another session).
export const live = $state({ status: null, agents: [], down: '', lost: false, now: new Date() })

export function connect() {
  const es = new EventSource('/events')
  es.addEventListener('state', (e) => {
    const v = JSON.parse(e.data)
    live.status = v.status
    live.agents = v.agents ?? []
    live.down = ''
    live.lost = false
  })
  es.addEventListener('down', (e) => {
    live.down = JSON.parse(e.data).error
    live.lost = false
  })
  es.onerror = () => {
    live.lost = true
  }
  // Keeps "7 min ago" moving between events.
  setInterval(() => (live.now = new Date()), 15e3)
}

// post sends a change to the agent; the agent's own message comes back as the error.
export async function post(path, body = {}) {
  const r = await fetch('/api' + path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json', 'X-Sevjala-UI': '1' },
    body: JSON.stringify(body),
  })
  const v = await r.json().catch(() => ({}))
  if (!r.ok) throw new Error(v.code === 'admin_required' ? `${v.error}: run it from the command line, which asks for them` : v.error || r.statusText)
  return v
}
