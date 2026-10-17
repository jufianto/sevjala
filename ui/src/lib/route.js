export const pages = ['', 'backups', 'restore', 'pcs', 'schedules', 'database', 'logs']

// parse reads "#/restore?backup=BK-1" as { page: 'restore', params: { backup: 'BK-1' } }.
export function parse(hash) {
  const [path, query = ''] = hash.replace(/^#\/?/, '').split('?')
  if (!pages.includes(path)) return { page: '', params: {} }
  return { page: path, params: Object.fromEntries(new URLSearchParams(query)) }
}
