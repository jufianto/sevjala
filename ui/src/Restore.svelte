<script>
  import { api, get, every, ConfirmNeeded } from './lib/api.js'
  import { when, bytes, plural } from './lib/format.js'
  import Confirm from './Confirm.svelte'

  let { params, now } = $props()

  let backups = $state([])
  let id = $state('')
  $effect(() => {
    if (params.backup) id = params.backup
  })
  let mode = $state('new')
  let into = $state('')
  let plan = $state(null)
  let run = $state(null)
  let err = $state('')
  let need = $state(null)
  let busy = $state(false)

  $effect(() => {
    get('/backups')
      .then((v) => {
        backups = v.backups.filter((b) => b.state !== 'dumping' && b.state !== 'failed')
        if (!id && backups.length) id = backups[0].backup_id
      })
      .catch((e) => (err = e.message))
  })

  // The running or last restore; polled every 2 s while one runs.
  async function status() {
    try {
      run = await get('/restore')
    } catch {
      run = null
    }
  }
  const running = $derived(run?.state === 'running')
  $effect(() => every(running ? 2e3 : 15e3, status))

  const req = (extra = {}) => ({ backup: id, into: mode === 'new' ? into : '', replace: mode === 'replace', ...extra })

  async function preview() {
    err = ''
    plan = null
    try {
      plan = await api('POST', '/restore', req({ dry_run: true }))
      if (mode === 'new' && !into) into = plan.target
    } catch (e) {
      err = e.message
    }
  }

  async function start(confirm = '') {
    err = ''
    need = null
    busy = true
    try {
      run = await api('POST', '/restore', req({ confirm }))
      plan = null
    } catch (e) {
      if (e instanceof ConfirmNeeded) need = e
      else err = e.message
    } finally {
      busy = false
    }
  }

  $effect(() => {
    id
    mode
    plan = null
  })

  const pct = $derived(run?.size ? Math.min(100, Math.round((100 * run.bytes) / run.size)) : 0)
</script>

<section class="card">
  <h2>Restore</h2>
  <p class="muted">Put a backup back into MySQL: as a new database next to the live one (safe, nothing is overwritten), or in place of the live database.</p>

  <div class="form">
    <label>
      Backup
      <select bind:value={id}>
        {#each backups as b}<option value={b.backup_id}>{b.database} · {when(b.created_at, now)} · {bytes(b.size)}</option>{/each}
      </select>
    </label>
    <fieldset>
      <legend>Restore into</legend>
      <label><input type="radio" bind:group={mode} value="new" /> a new database</label>
      {#if mode === 'new'}<input bind:value={into} placeholder="name (default: database_restored_date)" aria-label="New database name" />{/if}
      <label><input type="radio" bind:group={mode} value="replace" /> replace the live database (Sevjala backs it up first)</label>
    </fieldset>
    <div class="toolbar">
      <button class="plain" onclick={preview} disabled={!id}>Preview</button>
      <button class={mode === 'replace' ? 'danger' : 'primary'} onclick={() => start()} disabled={!id || busy || run?.state === 'running'}>Start</button>
    </div>
  </div>

  {#if plan}
    <p>Restores <strong>{plan.backup_id}</strong> ({plan.database}, made {when(plan.backup_created_at, now)}, {bytes(plan.size)}) into
      <strong>{plan.target}</strong>{plan.replace ? ', replacing the live database' : ', a new database'}.</p>
  {/if}
  {#if err}<p class="msg bad">{err}</p>{/if}

  <p class="muted file">Restoring from a file needs an administrator: run <code>sevjala restore --file &lt;path&gt;</code> from an administrator prompt.</p>
</section>

{#if run}
  <section class="card">
    <h2>{run.state === 'running' ? 'Restoring' : run.state === 'done' ? 'Last restore finished' : 'Last restore failed'}</h2>
    <p>{run.backup_id} ({run.database}) into <strong>{run.target}</strong>, started {when(run.started_at, now)}</p>
    {#if run.state === 'running'}
      <p>{run.step}{run.size ? ` · ${pct}%` : ''}</p>
      <progress max="100" value={pct}></progress>
    {:else if run.state === 'done'}
      <p class="msg ok">Done: {plural(run.tables, 'table', 'tables')} in {run.target}.</p>
    {:else}
      <p class="msg bad">{run.error}</p>
    {/if}
    {#if run.pre_restore_id}<p class="muted">The live database was backed up first as {run.pre_restore_id}.</p>{/if}
  </section>
{/if}

{#if need}<Confirm {need} onconfirm={(w) => start(w)} oncancel={() => (need = null)} />{/if}

<style>
  .form {
    display: grid;
    gap: 12px;
    margin-top: 12px;
  }
  label {
    display: grid;
    gap: 4px;
  }
  fieldset {
    border: 1px solid var(--line);
    border-radius: 8px;
    display: grid;
    gap: 6px;
  }
  fieldset label {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  progress {
    width: 100%;
  }
  .file {
    margin-top: 16px;
  }
</style>
