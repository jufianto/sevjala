<script>
  import { api, get, every, ConfirmNeeded } from './lib/api.js'
  import { cronFor, describe, presetOf, weekdays } from './lib/cron.js'
  import { when } from './lib/format.js'
  import Confirm from './Confirm.svelte'

  let { agents, now } = $props()

  let policies = $state([])
  let err = $state('')
  let note = $state('')
  let edit = $state(null) // the form: null, or { adding, name, kind, minute, hour, weekday, day, cron, keep, everyPC, to, enabled }
  let need = $state(null)
  let cleanup = $state(null)
  let busy = $state(false)

  const nodes = $derived(agents.filter((a) => a.role !== 'controller'))

  async function load() {
    try {
      policies = (await get('/policies')).policies
      err = ''
    } catch (e) {
      err = e.message
    }
  }
  $effect(() => every(10e3, load))

  function open(p) {
    const pre = presetOf(p?.schedule ?? '0 2 * * *')
    edit = {
      adding: !p,
      name: p?.name ?? '',
      ...pre,
      cron: p?.schedule ?? '',
      keep: p?.keep ?? 7,
      everyPC: !p || p.to.length === 0,
      to: (p?.to ?? []).map((t) => t.agent_id),
      enabled: p?.enabled ?? true,
    }
  }

  const schedule = (f) => (f.kind === 'custom' ? f.cron.trim() : f.kind === 'hand' ? '' : cronFor(f))

  async function save(confirm = '') {
    need = null
    busy = true
    const f = edit
    const ch = { schedule: schedule(f), keep: Number(f.keep), to: f.everyPC ? ['all'] : f.to, confirm }
    try {
      if (f.adding) await api('POST', '/policies', { ...ch, name: f.name.trim() })
      else await api('PATCH', '/policies/' + encodeURIComponent(f.name), { ...ch, enabled: f.enabled })
      note = `Saved ${f.name}.`
      edit = null
      load()
    } catch (e) {
      if (e instanceof ConfirmNeeded) need = e
      else err = e.message
    } finally {
      busy = false
    }
  }

  async function remove(p) {
    if (!confirm(`Remove schedule ${p.name}? It stops running; the backups it made are kept.`)) return
    try {
      await api('DELETE', '/policies/' + encodeURIComponent(p.name))
      note = `Removed ${p.name}.`
      load()
    } catch (e) {
      err = e.message
    }
  }

  async function cleanUp(run) {
    busy = true
    err = ''
    try {
      cleanup = await api('POST', '/retention/run', { dry_run: !run })
      if (run) note = `Clean-up done: ${cleanup.deletes.length} deleted, ${cleanup.sends.length} sent again.`
    } catch (e) {
      err = e.message
    } finally {
      busy = false
    }
  }

  const toText = (p) => (p.to.length ? p.to.map((t) => t.hostname).join(', ') : 'every backup PC')
</script>

<section class="card">
  <h2>Schedules</h2>
  <p class="muted">A schedule makes backups at set times and keeps the newest ones on each backup PC; older ones are deleted.</p>

  <div class="scroll">
    <table class="list">
      <thead><tr><th>Name</th><th>When</th><th>Keeps</th><th>Backup PCs</th><th>On</th><th>Next run</th><th></th></tr></thead>
      <tbody>
        {#each policies as p (p.name)}
          <tr>
            <td><strong>{p.name}</strong></td>
            <td>{describe(p.schedule)}</td>
            <td>{p.keep}</td>
            <td>{toText(p)}</td>
            <td>{p.enabled ? 'on' : 'off'}</td>
            <td>{p.next_run ? when(p.next_run, now) : '—'}</td>
            <td class="actions">
              <button class="link" onclick={() => open(p)}>Change</button>
              {#if p.name !== 'manual' && p.name !== 'pre-restore'}<button class="link" onclick={() => remove(p)}>Remove</button>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <div class="toolbar">
    <button class="primary" onclick={() => open(null)}>Add a schedule</button>
    <button class="plain" onclick={() => cleanUp(false)} disabled={busy}>Clean up now…</button>
  </div>
  {#if note}<p class="msg ok">{note}</p>{/if}
  {#if err}<p class="msg bad">{err}</p>{/if}
</section>

{#if edit}
  <section class="card">
    <h2>{edit.adding ? 'Add a schedule' : `Change ${edit.name}`}</h2>
    <form class="form" onsubmit={(e) => (e.preventDefault(), save())}>
      {#if edit.adding}
        <label>Name <input bind:value={edit.name} required pattern="[a-z0-9_\-]{'{'}1,32{'}'}" placeholder="weekly" /></label>
      {/if}
      <label>
        Runs
        <select bind:value={edit.kind}>
          <option value="hand">only by hand</option>
          <option value="hourly">every hour</option>
          <option value="daily">every day</option>
          <option value="weekly">every week</option>
          <option value="monthly">every month</option>
          <option value="custom">custom (cron)</option>
        </select>
      </label>
      {#if edit.kind === 'weekly'}
        <label>On <select bind:value={edit.weekday}>{#each weekdays as d, i}<option value={i}>{d}</option>{/each}</select></label>
      {/if}
      {#if edit.kind === 'monthly'}
        <label>Day of the month <input type="number" min="1" max="28" bind:value={edit.day} /></label>
      {/if}
      {#if ['daily', 'weekly', 'monthly'].includes(edit.kind)}
        <label>At (hour) <input type="number" min="0" max="23" bind:value={edit.hour} /></label>
      {/if}
      {#if ['hourly', 'daily', 'weekly', 'monthly'].includes(edit.kind)}
        <label>Minute <input type="number" min="0" max="59" bind:value={edit.minute} /></label>
      {/if}
      {#if edit.kind === 'custom'}
        <label>Cron line (minute hour day month weekday) <input bind:value={edit.cron} placeholder="*/10 * * * *" /></label>
      {/if}
      <p class="muted">{describe(schedule(edit))}</p>
      <label>Each backup PC keeps the newest <input type="number" min="1" max="1000" bind:value={edit.keep} /></label>
      <fieldset>
        <legend>Send to</legend>
        <label class="inline"><input type="checkbox" bind:checked={edit.everyPC} /> every backup PC</label>
        {#if !edit.everyPC}
          {#each nodes as n}
            <label class="inline"><input type="checkbox" value={n.agent_id} bind:group={edit.to} /> {n.hostname}</label>
          {/each}
        {/if}
      </fieldset>
      {#if !edit.adding}<label class="inline"><input type="checkbox" bind:checked={edit.enabled} /> runs on schedule</label>{/if}
      <div class="toolbar">
        <button class="plain" type="button" onclick={() => (edit = null)}>Cancel</button>
        <button class="primary" type="submit" disabled={busy}>Save</button>
      </div>
    </form>
  </section>
{/if}

{#if cleanup}
  <section class="card">
    <h2>{cleanup.dry_run ? 'What clean-up would do' : 'What clean-up did'}</h2>
    <p>Delete {cleanup.deletes.length}, send again {cleanup.sends.length}, drop from the controller's own copy {cleanup.unstaged.length}.</p>
    {#if cleanup.deletes.length}
      <ul>{#each cleanup.deletes as d}<li>{d.backup_id} on {d.hostname}: {d.reason}</li>{/each}</ul>
    {/if}
    <div class="toolbar">
      <button class="plain" onclick={() => (cleanup = null)}>Close</button>
      {#if cleanup.dry_run}<button class="primary" onclick={() => cleanUp(true)} disabled={busy}>Run it now</button>{/if}
    </div>
  </section>
{/if}

{#if need}<Confirm {need} onconfirm={(w) => save(w)} oncancel={() => (need = null)} />{/if}

<style>
  .form {
    display: grid;
    gap: 10px;
    max-width: 520px;
  }
  label {
    display: grid;
    gap: 4px;
  }
  label.inline {
    display: flex;
    gap: 8px;
    align-items: center;
  }
  fieldset {
    border: 1px solid var(--line);
    border-radius: 8px;
    display: grid;
    gap: 6px;
  }
  .actions {
    white-space: nowrap;
  }
  .link {
    border: 0;
    background: none;
    padding: 0 6px 0 0;
    color: var(--accent);
    text-decoration: underline;
  }
</style>
