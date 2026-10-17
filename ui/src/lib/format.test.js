import { test } from 'node:test'
import assert from 'node:assert/strict'
import { when, until, upFor, ago, bytes, plural, exportName } from './format.js'

const now = new Date(2026, 9, 17, 10, 27)

test('when words a time like the CLI and the tray', () => {
  assert.equal(when(new Date(2026, 9, 17, 10, 20).toISOString(), now), '10:20 (7 min ago)')
  assert.equal(when(new Date(2026, 9, 16, 4, 27).toISOString(), now), '2026-10-16 04:27 (30 h ago)')
  assert.equal(when(undefined, now), 'never')
})

test('ago', () => {
  assert.equal(ago(30e3), 'just now')
  assert.equal(ago(59 * 60e3), '59 min ago')
  assert.equal(ago(47 * 3600e3), '47 h ago')
  assert.equal(ago(72 * 3600e3), '3 days ago')
})

test('bytes match inventory.HumanBytes', () => {
  assert.equal(bytes(512), '512 B')
  assert.equal(bytes(10.8 * 1024 * 1024), '10.8 MiB')
  assert.equal(bytes(203.1 * 1024 ** 3), '203.1 GiB')
})

test('plural', () => {
  assert.equal(plural(1, 'backup PC', 'backup PCs'), '1 backup PC')
  assert.equal(plural(3, 'copy', 'copies'), '3 copies')
})

test('until words a coming time', () => {
  assert.equal(until(new Date(2026, 9, 17, 10, 30).toISOString(), now), '10:30 (in 3 min)')
  assert.equal(until(new Date(2026, 10, 1, 1, 0).toISOString(), now), '2026-11-01 01:00 (in 14 days)')
  assert.equal(until(undefined, now), 'none scheduled')
})

test('upFor', () => {
  assert.equal(upFor(42), '0m')
  assert.equal(upFor(3 * 3600 + 120), '3h 2m')
  assert.equal(upFor(3 * 86400 + 7200), '3d 2h')
})

test('a long name is shortened to fit its box', async () => {
  const { fit } = await import('./format.js')
  assert.equal(fit('PC-1', 18), 'PC-1')
  assert.equal(fit('ACCOUNTING-BACKUP-WORKSTATION-02', 18), 'ACCOUNTING-BACKUP…')
  assert.equal(fit('ACCOUNTING-BACKUP-WORKSTATION-02', 18).length, 18)
})

test('exportName matches the agent\'s download name', () => {
  const at = new Date(2026, 9, 14, 0, 20, 0).toISOString()
  assert.equal(exportName({ database: 'shop', created_at: at }), 'shop_2026-10-14_00-20-00')
})
