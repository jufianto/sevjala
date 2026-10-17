// Times and sizes worded as the CLI and the tray word them (localapi.When,
// inventory.HumanBytes).

const pad = (n) => String(n).padStart(2, '0')

export function clock(t, now = new Date()) {
  const hm = `${pad(t.getHours())}:${pad(t.getMinutes())}`
  if (t.toDateString() === now.toDateString()) return hm
  return `${t.getFullYear()}-${pad(t.getMonth() + 1)}-${pad(t.getDate())} ${hm}`
}

export function ago(ms) {
  if (ms < 60e3) return 'just now'
  if (ms < 3600e3) return `${Math.floor(ms / 60e3)} min ago`
  if (ms < 48 * 3600e3) return `${Math.floor(ms / 3600e3)} h ago`
  return `${Math.floor(ms / 86400e3)} days ago`
}

// when takes an RFC 3339 string from the agent; absent means it never happened.
export function when(iso, now = new Date()) {
  if (!iso) return 'never'
  const t = new Date(iso)
  return `${clock(t, now)} (${ago(now - t)})`
}

export function bytes(n) {
  if (n < 1024) return `${n} B`
  let div = 1024
  let exp = 0
  for (let m = Math.floor(n / 1024); m >= 1024; m = Math.floor(m / 1024)) {
    div *= 1024
    exp++
  }
  return `${(n / div).toFixed(1)} ${'KMGTPE'[exp]}iB`
}

export const plural = (n, one, many) => (n === 1 ? `1 ${one}` : `${n} ${many}`)

export function until(iso, now = new Date()) {
  if (!iso) return 'none scheduled'
  const t = new Date(iso)
  const ms = t - now
  let rel = 'now'
  if (ms >= 48 * 3600e3) rel = `in ${Math.floor(ms / 86400e3)} days`
  else if (ms >= 3600e3) rel = `in ${Math.floor(ms / 3600e3)} h`
  else if (ms >= 60e3) rel = `in ${Math.floor(ms / 60e3)} min`
  return `${clock(t, now)} (${rel})`
}

// upFor is how long the agent has run, as 'sevjala status' says it.
export function upFor(s) {
  const h = Math.floor(s / 3600)
  if (h >= 24) return `${Math.floor(h / 24)}d ${h % 24}h`
  if (h >= 1) return `${h}h ${Math.floor(s / 60) % 60}m`
  return `${Math.floor(s / 60)}m`
}
