// matrix lays backups out as rows and backup PCs as columns; a cell is that
// PC's copy state, '' when it has none.
export function matrix(backups, agents, { db = '', tag = '' } = {}) {
  const columns = agents.filter((a) => a.role !== 'controller').map(({ agent_id, hostname }) => ({ agent_id, hostname }))
  const rows = backups
    .filter((b) => (!db || b.database === db) && (!tag || (b.tags ?? []).includes(tag)))
    .map((b) => {
      const st = Object.fromEntries((b.copies ?? []).map((c) => [c.agent_id, c.state]))
      return { b, cells: columns.map((c) => st[c.agent_id] ?? '') }
    })
  const dbs = [...new Set(backups.map((b) => b.database))].sort()
  const tags = [...new Set(backups.flatMap((b) => b.tags ?? []))].sort()
  return { columns, rows, dbs, tags }
}
