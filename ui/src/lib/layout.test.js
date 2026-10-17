import { test } from 'node:test'
import assert from 'node:assert/strict'
import { layout, PER_ROW } from './layout.js'

test('one backup PC sits under the controller', () => {
  const l = layout(1)
  assert.equal(l.nodes.length, 1)
  assert.equal(l.nodes[0].x, l.controller.x)
  assert.ok(l.nodes[0].y > l.controller.y)
})

test('backup PCs spread evenly, and a full row starts a new one', () => {
  const l = layout(PER_ROW + 1)
  const row1 = l.nodes.slice(0, PER_ROW)
  assert.ok(row1.every((n) => n.y === row1[0].y))
  assert.ok(row1.every((n, i) => i === 0 || n.x > row1[i - 1].x))
  assert.ok(l.nodes[PER_ROW].y > row1[0].y)
  assert.ok(l.height > layout(1).height)
  assert.ok(l.nodes.every((n) => n.x - l.box.w / 2 >= 0 && n.x + l.box.w / 2 <= l.width))
})

test('without backup PCs there is room for the hint', () => {
  assert.equal(layout(0).nodes.length, 0)
  assert.ok(layout(0).height >= layout(1).height)
})

test('a line label sits between the controller and its own PC, below every row above it', () => {
  const l = layout(2 * PER_ROW + 1)
  const bottomOfFirstRow = l.nodes[0].y + l.box.h / 2
  l.nodes.forEach((n, i) => {
    const y = l.labels[i].y
    assert.ok(y > l.controller.y + l.box.h / 2 && y < n.y - l.box.h / 2, `label ${i} at ${y}`)
    if (i >= PER_ROW) assert.ok(y > bottomOfFirstRow, `label ${i} at ${y} is hidden by the first row`)
  })
})
