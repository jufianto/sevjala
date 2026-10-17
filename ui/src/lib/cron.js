// Schedule presets for the 5-field cron the agent reads
// ("minute hour day month weekday").

const pad = (n) => String(n).padStart(2, '0')
export const weekdays = ['Sundays', 'Mondays', 'Tuesdays', 'Wednesdays', 'Thursdays', 'Fridays', 'Saturdays']

export function cronFor({ kind, minute = 0, hour = 2, weekday = 0, day = 1 }) {
  switch (kind) {
    case 'hourly':
      return `${minute} * * * *`
    case 'daily':
      return `${minute} ${hour} * * *`
    case 'weekly':
      return `${minute} ${hour} * * ${weekday}`
    case 'monthly':
      return `${minute} ${hour} ${day} * *`
  }
  return ''
}

const num = /^\d+$/

// presetOf reads a cron line back into the preset that wrote it; any other
// line is { kind: 'custom' }.
export function presetOf(cron) {
  const p = { kind: 'custom', minute: 0, hour: 2, weekday: 0, day: 1 }
  if (!cron) return { ...p, kind: 'hand' }
  const f = cron.trim().split(/\s+/)
  if (f.length !== 5 || !num.test(f[0]) || f[3] !== '*') return p
  const [m, h, d, , w] = f
  p.minute = +m
  if (h === '*' && d === '*' && w === '*') return { ...p, kind: 'hourly' }
  if (!num.test(h)) return { ...p, minute: 0 }
  p.hour = +h
  if (d === '*' && w === '*') return { ...p, kind: 'daily' }
  if (d === '*' && num.test(w) && +w < 7) return { ...p, kind: 'weekly', weekday: +w }
  if (num.test(d) && w === '*') return { ...p, kind: 'monthly', day: +d }
  return { ...p, kind: 'custom', minute: 0, hour: 2 }
}

export function describe(cron) {
  const p = presetOf(cron)
  const at = `${pad(p.hour)}:${pad(p.minute)}`
  switch (p.kind) {
    case 'hand':
      return 'only by hand'
    case 'hourly':
      return `every hour at :${pad(p.minute)}`
    case 'daily':
      return `every day at ${at}`
    case 'weekly':
      return `${weekdays[p.weekday]} at ${at}`
    case 'monthly':
      return `on day ${p.day} of each month at ${at}`
  }
  return cron
}
