<script>
  import { layout } from './lib/layout.js'
  import { when, clock, bytes, plural, fit } from './lib/format.js'
  import { api, ConfirmNeeded } from './lib/api.js'
  import Confirm from './Confirm.svelte'

  let { status, agents, now } = $props()
  const nodes = $derived(agents.filter((a) => a.role !== 'controller'))
  const l = $derived(layout(nodes.length))
  const waiting = $derived(Object.fromEntries((status.health?.agents ?? []).map((a) => [a.agent_id, a.waiting])))
  const dbs = $derived((status.health?.databases ?? []).map((d) => d.name).join(', '))
  const tone = { online: 'green', warning: 'yellow', offline: 'red', error: 'red' }

  let picked = $state('')
  const sel = $derived(nodes.find((a) => a.agent_id === picked))
  const toggle = (id) => (picked = picked === id ? '' : id)

  let msg = $state('')
  let bad = $state(false)
  let forceable = $state(false)
  let need = $state(null)
  let found = $state(null)
  let addr = $state('')
  let pairing = $state(null) // { agent, hostname, code, fp }
  let busy = $state(false)

  function say(text, isBad = false) {
    msg = text
    bad = isBad
  }

  async function act(f) {
    busy = true
    try {
      await f()
    } catch (e) {
      if (e instanceof ConfirmNeeded) need = e
      else say(e.message, true)
    } finally {
      busy = false
    }
  }

  const check = (a) =>
    act(async () => {
      const p = await api('POST', `/agents/${encodeURIComponent(a.agent_id)}/ping`)
      say(`${a.hostname} answered in ${p.rtt_ms} ms; its health: ${p.health}.`)
    })

  // remove asks without force first; the agent says when force (and then the
  // PC's name) is needed.
  function remove(a, force = false, confirm = '') {
    need = null
    forceable = false
    const q = new URLSearchParams()
    if (force) q.set('force', 'true')
    if (confirm) q.set('confirm', confirm)
    const qs = q.toString()
    return act(async () => {
      try {
        await api('DELETE', `/agents/${encodeURIComponent(a.agent_id)}${qs ? '?' + qs : ''}`)
        picked = ''
        say(`Removed ${a.hostname}; its backup files stay on that PC.`)
      } catch (e) {
        forceable = !force && !(e instanceof ConfirmNeeded) && e.message.includes('--force')
        throw e
      }
    })
  }

  function askRemove(a) {
    if (confirm(`Remove ${a.hostname}? Backups stop going to it; the backup files on it stay there.`)) remove(a)
  }

  const discover = () =>
    act(async () => {
      found = (await api('POST', '/discover')).agents.filter((d) => d.role !== 'controller')
      if (found.length) say('')
      else say('No other Sevjala PC answered on this network. Is Sevjala installed and running there?', true)
    })

  const addByAddress = () =>
    act(async () => {
      found = [await api('POST', '/agents', { addr: addr.trim() })]
      say('')
    })

  const pair = () =>
    act(async () => {
      const a = await api('POST', '/pair', { agent: pairing.agent, code: pairing.code.trim(), fp: pairing.fp.trim() })
      say(`Paired with ${a.hostname}. Backups go to it from the next run.`)
      pairing = null
      found = null
    })

  function lineLabel(a) {
    const n = waiting[a.agent_id] ?? 0
    if (n) return plural(n, 'copy waiting', 'copies waiting')
    return a.state === 'online' ? 'up to date' : a.state
  }
</script>

<section class="card">
  <h2>The controller and its backup PCs</h2>
  <p class="muted">
    Backups go from the controller down to each backup PC. A line is green when that PC answers, yellow when it
    warns, red when it does not answer. Click a PC for its details.
  </p>

  <svg viewBox="0 0 {l.width} {l.height}" role="img" aria-label="Diagram of the controller and its backup PCs">
    {#each nodes as a, i}
      {@const p = l.nodes[i]}
      <line
        x1={l.controller.x}
        y1={l.controller.y + l.box.h / 2}
        x2={p.x}
        y2={p.y - l.box.h / 2}
        class="link {tone[a.state] ?? 'grey'}"
        class:dashed={a.state === 'offline' || a.state === 'error'}
      />
      <text
        x={l.labels[i].x}
        y={l.labels[i].y}
        class="linelabel {tone[a.state] ?? 'grey'}"
        text-anchor="middle">{lineLabel(a)}</text
      >
    {/each}

    <g class="box controller" transform="translate({l.controller.x - l.box.w / 2 - 20} {l.controller.y - l.box.h / 2})">
      <rect width={l.box.w + 40} height={l.box.h} rx="12" />
      <text x={(l.box.w + 40) / 2} y="30" text-anchor="middle" class="name">{fit(status.hostname, 22)}</text>
      <text x={(l.box.w + 40) / 2} y="52" text-anchor="middle" class="sub">controller · MySQL</text>
      <text x={(l.box.w + 40) / 2} y="72" text-anchor="middle" class="sub">{fit(dbs || 'no database chosen', 30)}</text>
    </g>

    {#each nodes as a, i}
      {@const p = l.nodes[i]}
      <g
        class="box node {tone[a.state] ?? 'grey'}"
        class:picked={picked === a.agent_id}
        transform="translate({p.x - l.box.w / 2} {p.y - l.box.h / 2})"
        role="button"
        tabindex="0"
        aria-label="{a.hostname}, {a.state}"
        onclick={() => toggle(a.agent_id)}
        onkeydown={(e) => (e.key === 'Enter' || e.key === ' ') && (e.preventDefault(), toggle(a.agent_id))}
      >
        <title>{a.hostname}</title>
        <rect width={l.box.w} height={l.box.h} rx="12" />
        <circle cx="18" cy="22" r="6" class="dot" />
        <text x="32" y="27" class="name">{fit(a.hostname, 15)}</text>
        <text x="16" y="52" class="sub">{a.state}{a.state !== 'online' && a.last_seen ? ` · since ${clock(new Date(a.last_seen), now)}` : ''}</text>
        <text x="16" y="72" class="sub">{bytes(a.free_bytes ?? 0)} free</text>
      </g>
    {/each}

    {#if !nodes.length}
      <g transform="translate({l.width / 2 - 210} 200)">
        <rect width="420" height="70" rx="12" class="empty" />
        <text x="210" y="31" text-anchor="middle" class="name">No backup PC yet</text>
        <text x="210" y="53" text-anchor="middle" class="sub">use “Add a backup PC” below</text>
      </g>
    {/if}
  </svg>
</section>

{#if sel}
  <section class="card details">
    <h3>{sel.hostname}</h3>
    <dl>
      <dt>State</dt><dd>{sel.state}{sel.problem ? `: ${sel.problem}` : ''}</dd>
      <dt>Last answered</dt><dd>{when(sel.last_seen, now)}</dd>
      <dt>Copies waiting</dt><dd>{waiting[sel.agent_id] ?? 0}</dd>
      <dt>Disk free</dt><dd>{bytes(sel.free_bytes ?? 0)}</dd>
      <dt>Address</dt><dd>{sel.addr}</dd>
      <dt>Sevjala</dt><dd>{sel.version}</dd>
      <dt>Paired</dt><dd>{when(sel.paired_at, now)}</dd>
      <dt>Agent id</dt><dd><code>{sel.agent_id}</code></dd>
    </dl>
    <div class="toolbar">
      <button class="plain" onclick={() => check(sel)} disabled={busy}>Check</button>
      <button class="plain" onclick={() => askRemove(sel)} disabled={busy}>Remove…</button>
      {#if forceable}<button class="danger" onclick={() => remove(sel, true)} disabled={busy}>Remove anyway</button>{/if}
    </div>
  </section>
{/if}

{#if msg}<p class="msg" class:bad class:ok={!bad}>{msg}</p>{/if}

<section class="card">
  <h2>Add a backup PC</h2>
  <p class="muted">Install Sevjala on the PC as a backup PC first. On that PC run <code>sevjala code</code>: it shows a pairing code and a fingerprint to type here.</p>
  <div class="toolbar">
    <button class="primary" onclick={discover} disabled={busy}>Find PCs on this network</button>
    <form class="toolbar" onsubmit={(e) => (e.preventDefault(), addByAddress())}>
      <input bind:value={addr} placeholder="192.168.1.21" aria-label="Address of the backup PC" />
      <button class="plain" disabled={busy || !addr.trim()}>Add by address</button>
    </form>
  </div>
  {#if found?.length}
    <table class="list">
      <thead><tr><th>PC</th><th>Address</th><th>Sevjala</th><th></th></tr></thead>
      <tbody>
        {#each found as d (d.agent_id)}
          <tr>
            <td>{d.hostname}</td><td>{d.addr}</td><td>{d.version}</td>
            <td>
              {#if d.paired}paired{:else}
                <button class="plain" onclick={() => (pairing = { agent: d.agent_id, hostname: d.hostname, code: '', fp: '' })}>Pair…</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  {/if}
  {#if pairing}
    <form class="pairform" onsubmit={(e) => (e.preventDefault(), pair())}>
      <h3>Pair with {pairing.hostname}</h3>
      <label>Pairing code <input bind:value={pairing.code} inputmode="numeric" autocomplete="off" required /></label>
      <label>Fingerprint (the first characters are enough) <input bind:value={pairing.fp} autocomplete="off" required /></label>
      <div class="toolbar">
        <button class="plain" type="button" onclick={() => (pairing = null)}>Cancel</button>
        <button class="primary" disabled={busy}>Pair</button>
      </div>
    </form>
  {/if}
</section>

{#if need}<Confirm {need} onconfirm={(w) => remove(sel, true, w)} oncancel={() => ((need = null), (forceable = true))} />{/if}

<style>
  .pairform {
    display: grid;
    gap: 8px;
    max-width: 420px;
    margin-top: 12px;
  }
  .pairform label {
    display: grid;
    gap: 4px;
  }
  h2 {
    font-size: 18px;
  }
  svg {
    width: 100%;
    height: auto;
    margin-top: 8px;
  }
  .link {
    stroke-width: 3;
  }
  .dashed {
    stroke-dasharray: 8 7;
  }
  .linelabel {
    font-size: 13px;
    font-weight: 600;
    paint-order: stroke;
    stroke: var(--card);
    stroke-width: 6px;
  }
  .box rect {
    fill: var(--card);
    stroke: var(--line);
    stroke-width: 2;
  }
  .controller rect {
    fill: var(--grey-bg);
    stroke: var(--text);
  }
  .node {
    cursor: pointer;
    outline: none;
  }
  .node rect {
    stroke: var(--c);
  }
  .node.picked rect,
  .node:focus-visible rect {
    stroke-width: 4;
  }
  .dot {
    fill: var(--c);
  }
  .name {
    font-size: 16px;
    font-weight: 600;
    fill: var(--text);
  }
  .sub {
    font-size: 13px;
    fill: var(--muted);
  }
  .empty {
    fill: none;
    stroke: var(--muted);
    stroke-dasharray: 6 6;
  }
  .green {
    --c: var(--green);
    stroke: var(--green);
    fill: var(--green);
  }
  .yellow {
    --c: var(--yellow);
    stroke: var(--yellow);
    fill: var(--yellow);
  }
  .red {
    --c: var(--red);
    stroke: var(--red);
    fill: var(--red);
  }
  .grey {
    --c: var(--grey);
    stroke: var(--grey);
    fill: var(--grey);
  }
  /* boxes and labels take only the colour variable, not a filled shape */
  .box.green,
  .box.yellow,
  .box.red,
  .box.grey {
    stroke: none;
    fill: none;
  }
  .linelabel.green,
  .linelabel.yellow,
  .linelabel.red,
  .linelabel.grey {
    stroke: var(--card);
  }
  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 6px 18px;
    margin: 10px 0 0;
  }
  dt {
    color: var(--muted);
  }
  dd {
    margin: 0;
  }
</style>
