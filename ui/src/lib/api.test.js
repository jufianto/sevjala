import { test } from 'node:test'
import assert from 'node:assert/strict'
import { errorFrom, ConfirmNeeded, fetchFile, adminWhere } from './api.js'

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

test("fetchFile hands back the body only when the agent answers OK", async (t) => {
  const answers = {
    '/api/held/BK-1': new Response('gzip bytes', { status: 200 }),
    '/api/held/BK-2': new Response(JSON.stringify({ error: 'BK-2 is damaged on this PC' }), { status: 409 }),
    '/api/held/BK-3': new Response('Bad Gateway', { status: 502 }),
  }
  t.mock.method(globalThis, 'fetch', async (url) => answers[url])
  assert.equal(await (await fetchFile('/held/BK-1')).text(), 'gzip bytes')
  await assert.rejects(fetchFile('/held/BK-2'), { message: 'BK-2 is damaged on this PC' })
  await assert.rejects(fetchFile('/held/BK-3'), { message: 'HTTP 502' })
})

test('controller_admin_required is shown as the controller words it', () => {
  const e = errorFrom(403, { error: 'restoring from a file needs administrator rights on SERVER-01 itself', code: 'controller_admin_required' })
  assert.equal(e.message, 'restoring from a file needs administrator rights on SERVER-01 itself')
})

test('admin hints name the controller when the page came through a manager', () => {
  assert.equal(adminWhere('', 'SERVER-01'), 'from an administrator prompt')
  assert.equal(adminWhere('BACKUP-PC-1', 'SERVER-01'), 'from an administrator prompt on SERVER-01 itself')
})
