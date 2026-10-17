// live holds what /events last said. lost is set when sevjala ui itself is
// gone (the tab outlived it, or a new sevjala ui started another session).
// via is this PC's name when the page is the controller's, opened from a manager.
export const live = $state({ status: null, agents: [], via: '', down: '', lost: false, now: new Date() })

export function connect() {
  const es = new EventSource('/events')
  es.addEventListener('state', (e) => {
    const v = JSON.parse(e.data)
    live.status = v.status
    live.agents = v.agents ?? []
    live.via = v.via ?? ''
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

