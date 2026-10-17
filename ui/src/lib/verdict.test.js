import { test } from 'node:test'
import assert from 'node:assert/strict'
import { verdict } from './verdict.js'

test('the colour follows the health level, as in the tray', () => {
  const st = (level) => ({ role: 'controller', health: { level, headline: 'H', items: [{ level, text: 'H' }, { level: 'warning', text: 'W' }] } })
  assert.deepEqual(verdict({ status: st('ok') }), { color: 'green', headline: 'H', items: ['W'] })
  assert.equal(verdict({ status: st('warning') }).color, 'yellow')
  assert.equal(verdict({ status: st('problem') }).color, 'red')
})

test('a stopped service is grey', () => {
  assert.deepEqual(verdict({ down: 'The Sevjala service is not running' }),
    { color: 'grey', headline: 'The Sevjala service is not running', items: [] })
})

test('an agent without health is older than the dashboard', () => {
  assert.deepEqual(verdict({ status: { role: 'node' } }),
    { color: 'yellow', headline: 'The agent is older than this dashboard; run: sevjala update', items: [] })
})

test('before the first answer it waits', () => {
  assert.equal(verdict({}).color, 'grey')
})
