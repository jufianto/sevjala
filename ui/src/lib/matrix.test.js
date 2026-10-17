import { test } from 'node:test'
import assert from 'node:assert/strict'
import { matrix } from './matrix.js'

const agents = [
  { agent_id: 'c', hostname: 'SERVER-01', role: 'controller' },
  { agent_id: 'a', hostname: 'PC-1', role: 'node' },
  { agent_id: 'b', hostname: 'PC-2', role: 'node' },
]
const bk = (id, database, tags, copies) => ({ backup_id: id, database, tags, copies })
const backups = [
  bk('B3', 'shop', ['daily'], [{ agent_id: 'a', state: 'verified' }, { agent_id: 'b', state: 'pending' }]),
  bk('B2', 'crm', ['manual'], [{ agent_id: 'b', state: 'verified' }]),
  bk('B1', 'shop', ['daily', 'monthly'], []),
]

test('one column per backup PC, one row per backup, empty where a PC has no copy', () => {
  const m = matrix(backups, agents)
  assert.deepEqual(m.columns, [{ agent_id: 'a', hostname: 'PC-1' }, { agent_id: 'b', hostname: 'PC-2' }])
  assert.deepEqual(m.rows.map((r) => [r.b.backup_id, ...r.cells]), [
    ['B3', 'verified', 'pending'],
    ['B2', '', 'verified'],
    ['B1', '', ''],
  ])
  assert.deepEqual(m.dbs, ['crm', 'shop'])
  assert.deepEqual(m.tags, ['daily', 'manual', 'monthly'])
})

test('filters by database and schedule', () => {
  assert.deepEqual(matrix(backups, agents, { db: 'shop' }).rows.map((r) => r.b.backup_id), ['B3', 'B1'])
  assert.deepEqual(matrix(backups, agents, { tag: 'monthly' }).rows.map((r) => r.b.backup_id), ['B1'])
  assert.deepEqual(matrix(backups, agents, { db: 'crm', tag: 'daily' }).rows, [])
})
