const colors = { ok: 'green', warning: 'yellow', problem: 'red' }

// verdict is what the big card shows, worded and coloured as the tray does
// (tray.Build). The headline is the first item, so items lists the rest.
export function verdict({ status, down }) {
  if (down) return { color: 'grey', headline: down, items: [] }
  if (!status) return { color: 'grey', headline: 'Asking the Sevjala service…', items: [] }
  const h = status.health
  if (!h) return { color: 'yellow', headline: 'The agent is older than this dashboard; run: sevjala update', items: [] }
  const items = (h.items ?? []).map((it) => it.text).filter((t) => t !== h.headline)
  return { color: colors[h.level] ?? 'yellow', headline: h.headline, items }
}
