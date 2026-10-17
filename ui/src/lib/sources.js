// A controller a backup PC holds backups from: its PC name, or the start of
// its agent id until its first named backup arrives.
export const sourceName = (s) => s.hostname || s.agent_id.slice(0, 8)

// controllerName is the controller this backup PC is paired with.
export function controllerName(status) {
  const c = status.controller
  if (!c) return ''
  return c.hostname || `${sourceName(c)} (its name shows with its first backup)`
}
