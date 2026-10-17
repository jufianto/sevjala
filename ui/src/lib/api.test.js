import { test } from 'node:test'
import assert from 'node:assert/strict'
import { errorFrom, ConfirmNeeded } from './api.js'

test('confirm_required becomes ConfirmNeeded with the word and what it deletes', () => {
  const e = errorFrom(409, { error: 'this change deletes 2 backups on PC-1', code: 'confirm_required', word: 'daily', deletes: 2, hosts: ['PC-1'] })
  assert.ok(e instanceof ConfirmNeeded)
  assert.equal(e.message, 'this change deletes 2 backups on PC-1')
  assert.equal(e.word, 'daily')
  assert.equal(e.deletes, 2)
  assert.deepEqual(e.hosts, ['PC-1'])
})

test('admin_required says where to run it', () => {
  const e = errorFrom(403, { error: 'choosing the dump program (--dumper) needs administrator rights', code: 'admin_required' })
  assert.ok(!(e instanceof ConfirmNeeded))
  assert.equal(e.message, 'choosing the dump program (--dumper) needs administrator rights: run it from an administrator prompt')
})

test("any other error is the agent's message, or the status", () => {
  assert.equal(errorFrom(404, { error: 'BK-1 is not in this PC' }).message, 'BK-1 is not in this PC')
  assert.equal(errorFrom(502, {}).message, 'HTTP 502')
})
