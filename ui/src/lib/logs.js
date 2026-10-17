const rank = { DEBUG: 0, INFO: 1, WARN: 2, ERROR: 3 }
const min = { '': 0, info: 1, warn: 2, error: 3 }

// filterLogs keeps entries at level or worse whose message, event or "by"
// contains text.
export function filterLogs(entries, { level = '', text = '' } = {}) {
  const t = text.trim().toLowerCase()
  return entries.filter(
    (e) => (rank[e.level] ?? 1) >= min[level] && (!t || [e.msg, e.event, e.by].some((s) => s?.toLowerCase().includes(t))),
  )
}
