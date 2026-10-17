import { test } from 'node:test'
import assert from 'node:assert/strict'
import { parse } from './route.js'

test('a page and its parameters come from the hash', () => {
  assert.deepEqual(parse('#/restore?backup=BK-1'), { page: 'restore', params: { backup: 'BK-1' } })
  assert.deepEqual(parse('#/pcs'), { page: 'pcs', params: {} })
})

test('nothing or an unknown page is the dashboard', () => {
  assert.deepEqual(parse(''), { page: '', params: {} })
  assert.deepEqual(parse('#/'), { page: '', params: {} })
  assert.deepEqual(parse('#/nosuch?x=1'), { page: '', params: {} })
})
