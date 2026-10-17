import { test } from 'node:test'
import assert from 'node:assert/strict'
import { filterLogs } from './logs.js'

const es = [
  { level: 'INFO', msg: 'policy changed', event: 'policy_changed', by: 'OFFICE\\ana' },
  { level: 'WARN', msg: 'agent offline', event: 'agent_offline' },
  { level: 'ERROR', msg: 'backup failed', event: 'backup_failed' },
]
const msgs = (xs) => xs.map((e) => e.msg)

test('a level shows itself and anything worse', () => {
  assert.deepEqual(msgs(filterLogs(es, { level: 'info' })), ['policy changed', 'agent offline', 'backup failed'])
  assert.deepEqual(msgs(filterLogs(es, { level: 'warn' })), ['agent offline', 'backup failed'])
  assert.deepEqual(msgs(filterLogs(es, { level: 'error' })), ['backup failed'])
})

test('text matches the message, the event or who did it, ignoring case', () => {
  assert.deepEqual(msgs(filterLogs(es, { text: 'ANA' })), ['policy changed'])
  assert.deepEqual(msgs(filterLogs(es, { text: 'offline' })), ['agent offline'])
  assert.deepEqual(msgs(filterLogs(es, { text: 'backup_failed', level: 'warn' })), ['backup failed'])
})
