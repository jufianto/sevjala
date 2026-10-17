import { test } from 'node:test'
import assert from 'node:assert/strict'
import { cronFor, describe, presetOf } from './cron.js'

test('each preset writes its cron line', () => {
  assert.equal(cronFor({ kind: 'hourly', minute: 5 }), '5 * * * *')
  assert.equal(cronFor({ kind: 'daily', hour: 2, minute: 0 }), '0 2 * * *')
  assert.equal(cronFor({ kind: 'weekly', hour: 3, minute: 0, weekday: 0 }), '0 3 * * 0')
  assert.equal(cronFor({ kind: 'monthly', hour: 2, minute: 0, day: 1 }), '0 2 1 * *')
})

test('describe words the presets and leaves anything else as written', () => {
  assert.equal(describe('5 * * * *'), 'every hour at :05')
  assert.equal(describe('0 2 * * *'), 'every day at 02:00')
  assert.equal(describe('0 3 * * 0'), 'Sundays at 03:00')
  assert.equal(describe('0 2 1 * *'), 'on day 1 of each month at 02:00')
  assert.equal(describe(''), 'only by hand')
  assert.equal(describe('*/10 * * * *'), '*/10 * * * *')
  assert.equal(describe('@daily'), '@daily')
})

test('presetOf reads a preset back, anything else is custom', () => {
  assert.deepEqual(presetOf('0 3 * * 0'), { kind: 'weekly', minute: 0, hour: 3, weekday: 0, day: 1 })
  assert.deepEqual(presetOf('15 * * * *'), { kind: 'hourly', minute: 15, hour: 2, weekday: 0, day: 1 })
  assert.deepEqual(presetOf(''), { kind: 'hand', minute: 0, hour: 2, weekday: 0, day: 1 })
  assert.equal(presetOf('*/10 * * * *').kind, 'custom')
  for (const p of [{ kind: 'daily', minute: 30, hour: 2 }, { kind: 'monthly', minute: 0, hour: 1, day: 15 }]) {
    assert.equal(cronFor(presetOf(cronFor(p))), cronFor(p))
  }
})
