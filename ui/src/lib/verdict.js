const colors = { ok: 'green', warning: 'yellow', problem: 'red' }
const words = { green: 'Protected', yellow: 'Needs a look', red: 'Problem', grey: 'Not running' }

// verdict is what the big card shows, worded and coloured as the tray does
// (tray.Build). The headline is the first item, so items lists the rest.
export function verdict({ status, down }) {
  const v = (color, headline, items = []) => ({ color, word: words[color], headline, items })
  if (down) return v('grey', down)
  if (!status) return { ...v('grey', 'Asking the Sevjala service…'), word: 'Connecting' }
  const h = status.health
  if (!h) return v('yellow', 'The agent is older than this dashboard; run: sevjala update')
  return v(colors[h.level] ?? 'yellow', h.headline, (h.items ?? []).map((it) => it.text).filter((t) => t !== h.headline))
}
