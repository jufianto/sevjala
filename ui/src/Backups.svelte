<script>
  import { api, get, every } from './lib/api.js'
  import { matrix } from './lib/matrix.js'
  import { when, bytes, exportName } from './lib/format.js'

  let { status, agents, now } = $props()

  let backups = $state([])
  let policies = $state([])
  let held = $state(new Set())
  let all = $state(false)
  let db = $state('')
  let tag = $state('')
  let err = $state('')
  let note = $state('')
  let noteBad = $state(false)
  let nowDB = $state('')
  let nowTag = $state('')
  let busy = $state(false)

  async function load() {
    try {
      const [b, h, p] = await Promise.all([get('/backups' + (all ? '?all=1' : '')), get('/held'), get('/policies')])
      backups = b.backups
      held = new Set(h.backups.map((m) => m.backup_id))
      policies = p.policies
      err = ''
    } catch (e) {
      err = e.message
    }
  }
  $effect(() => {
    all
    return every(10e3, load)
  })

  const m = $derived(matrix(backups, agents, { db, tag }))
  const dbs = $derived((status.health?.databases ?? []).map((d) => d.name))
  const label = { verified: 'verified', pending: 'waiting', transferring: 'sending', failed: 'failed', corrupt: 'damaged', deleted: 'deleted' }
  const tone = { verified: 'green', pending: 'yellow', transferring: 'yellow', failed: 'red', corrupt: 'red', deleted: 'grey' }

  function say(text, bad = false) {
    note = text
    noteBad = bad
  }

  async function backupNow() {
    busy = true
    try {
      const v = await api('POST', '/backup/now', { db: nowDB, tags: nowTag ? [nowTag] : [] })
      say(`Backing up ${v.backups.map((b) => b.database).join(', ')}; the list updates when it is done.`)
      load()
    } catch (e) {
      say(e.message, true)
    } finally {
      busy = false
    }
  }

  function download(href, name) {
    const a = document.createElement('a')
    a.href = href
    a.download = name
    document.body.append(a)
    a.click()
    a.remove()
  }

  async function exportBackup(id) {
    try {
      const h = await get('/held')
      const man = h.backups.find((x) => x.backup_id === id)
      if (!man) {
        held = new Set(h.backups.map((x) => x.backup_id))
        say(`${id} is no longer in the controller's own copy (it keeps only the newest); export it on a backup PC that holds it.`, true)
        return
      }
      const name = exportName(man)
      download('/api/held/' + encodeURIComponent(id), name + '.sql.gz')
      const url = URL.createObjectURL(new Blob([JSON.stringify(man, null, 2) + '\n'], { type: 'application/json' }))
      download(url, name + '.json')
      setTimeout(() => URL.revokeObjectURL(url), 60e3)
      say(`Downloading ${name}.sql.gz and ${name}.json.`)
    } catch (e) {
      say(e.message, true)
    }
  }
</script>

<section class="card">
  <h2>Backups</h2>
  <p class="muted">Newest first. Each column is a backup PC: what its copy of that backup is doing.</p>

  <div class="toolbar">
    <label>Database <select bind:value={db}><option value="">all</option>{#each m.dbs as d}<option>{d}</option>{/each}</select></label>
    <label>Schedule <select bind:value={tag}><option value="">all</option>{#each m.tags as t}<option>{t}</option>{/each}</select></label>
    <label><input type="checkbox" bind:checked={all} /> include expired</label>
  </div>

  <div class="toolbar">
    <strong>Back up now</strong>
    <select bind:value={nowDB} aria-label="Database to back up"><option value="">every database</option>{#each dbs as d}<option>{d}</option>{/each}</select>
    <select bind:value={nowTag} aria-label="Schedule tag"><option value="">manual</option>{#each policies.filter((p) => p.name !== 'manual' && p.name !== 'pre-restore') as p}<option>{p.name}</option>{/each}</select>
    <button class="primary" onclick={backupNow} disabled={busy}>{busy ? 'Starting…' : 'Back up now'}</button>
  </div>
  {#if note}<p class="msg" class:bad={noteBad} class:ok={!noteBad}>{note}</p>{/if}
  {#if err}<p class="msg bad">{err}</p>{/if}

  <div class="scroll">
    <table class="list">
      <thead>
        <tr>
          <th>Made</th><th>Database</th><th>Size</th><th>Schedule</th>
          {#each m.columns as c}<th>{c.hostname}</th>{/each}
          <th></th>
        </tr>
      </thead>
      <tbody>
        {#each m.rows as r (r.b.backup_id)}
          <tr>
            <td title={r.b.backup_id}>{when(r.b.created_at, now)}</td>
            <td>{r.b.database}</td>
            <td>{r.b.state === 'dumping' ? 'dumping…' : r.b.state === 'failed' ? 'failed' : bytes(r.b.size)}</td>
            <td>{(r.b.tags ?? []).join(', ')}</td>
            {#each r.cells as s}<td class={tone[s] ?? ''}>{label[s] ?? s}</td>{/each}
            <td class="actions">
              {#if r.b.state !== 'dumping' && r.b.state !== 'failed' && r.b.state !== 'expired'}
                <a href="#/restore?backup={encodeURIComponent(r.b.backup_id)}">Restore…</a>
              {/if}
              {#if held.has(r.b.backup_id)}<button class="link" onclick={() => exportBackup(r.b.backup_id)}>Export</button>{/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan={5 + m.columns.length} class="muted">No backups yet.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<style>
  .green {
    color: var(--green);
  }
  .yellow {
    color: var(--yellow);
  }
  .red {
    color: var(--red);
  }
  .grey {
    color: var(--grey);
  }
  .actions {
    white-space: nowrap;
    display: flex;
    gap: 10px;
  }
  a,
  .link {
    color: var(--accent);
  }
  .link {
    border: 0;
    background: none;
    padding: 0;
    text-decoration: underline;
  }
</style>
