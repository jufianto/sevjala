import { test } from 'node:test'
import assert from 'node:assert/strict'
import { sourceName, controllerName } from './sources.js'

test('a controller is named after its PC, or by its id before its first backup', () => {
  assert.equal(sourceName({ agent_id: 'c960acf5-1111', hostname: 'SERVER-01' }), 'SERVER-01')
  assert.equal(sourceName({ agent_id: 'c960acf5-1111' }), 'c960acf5')
})

test('the paired controller', () => {
  assert.equal(controllerName({ controller: { agent_id: 'c960acf5-1111', hostname: 'SERVER-01' } }), 'SERVER-01')
  assert.equal(controllerName({ controller: { agent_id: 'c960acf5-1111' } }), 'c960acf5 (its name shows with its first backup)')
  assert.equal(controllerName({}), '')
})
