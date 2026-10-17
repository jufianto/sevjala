// Where the diagram draws the controller and its backup PCs (centres, in
// SVG units): the controller on top, the PCs in rows below it.
export const PER_ROW = 4
const W = 880
const box = { w: 190, h: 92 }
const top = 70
const firstRow = 250
const rowGap = 140

export function layout(count) {
  const rows = Math.max(1, Math.ceil(count / PER_ROW))
  const nodes = []
  for (let i = 0; i < count; i++) {
    const row = Math.floor(i / PER_ROW)
    const inRow = Math.min(PER_ROW, count - row * PER_ROW)
    const slot = W / inRow
    nodes.push({ x: slot * (i % PER_ROW) + slot / 2, y: firstRow + row * rowGap })
  }
  return { width: W, height: firstRow + (rows - 1) * rowGap + box.h / 2 + 20, box, controller: { x: W / 2, y: top }, nodes }
}
