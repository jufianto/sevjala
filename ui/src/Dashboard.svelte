<script>
  import { post } from './lib/live.svelte.js'
  import { when, until, upFor, bytes, plural } from './lib/format.js'

  let { status, now } = $props()
  const h = $derived(status.health ?? {})
  const controller = $derived(status.role === 'controller')

  let busy = $state(false)
  let result = $state('')
  let failed = $state(false)

  async function backupNow() {
    busy = true
    result = ''
    try {
      const v = await post('/backup/now')
      const dbs = (v.backups ?? []).map((b) => b.database)
      result = dbs.length ? `Backing up ${dbs.join(', ')}; this page updates when it is done.` : 'Nothing to back up yet.'
      failed = false
    } catch (e) {
      result = e.message
      failed = true
    } finally {
      busy = false
    }
  }
</script>

{#if controller}
  <section class="grid">
    {#each h.databases ?? [] as d}
      <article class="card">
        <div class="label">Database</div>
        <h3>{d.name}</h3>
        {#if d.last_good}
          <p>Last good backup <strong>{when(d.last_good_at, now)}</strong>, {bytes(d.size)}</p>
          <p class:warn={d.copies < d.planned}>On {d.copies} of {plural(d.planned, 'backup PC', 'backup PCs')}</p>
        {:else}
          <p class="warn">No backup has reached a backup PC yet</p>
        {/if}
        {#if d.last_error}
          <p class="bad">Last attempt failed {when(d.last_failed_at, now)}: {d.last_error}</p>
        {/if}
      </article>
    {:else}
      <article class="card">
        <div class="label">Database</div>
        <p>No database chosen yet: run <code>sevjala db set --db NAME</code></p>
      </article>
    {/each}

    <article class="card">
      <div class="label">Next backup</div>
      <h3>{until(h.next_backup_at, now)}</h3>
      {#if h.next_policy}<p class="muted">schedule “{h.next_policy}”</p>{/if}
      <button class="primary" onclick={backupNow} disabled={busy}>{busy ? 'Starting…' : 'Back up now'}</button>
      {#if result}<p class:bad={failed} role="status">{result}</p>{/if}
    </article>

    <article class="card">
      <div class="label">Backup PCs</div>
      <h3>{plural((h.agents ?? []).filter((a) => a.state === 'online' || a.state === 'warning').length, 'online', 'online')} of {(h.agents ?? []).length}</h3>
      {#each h.agents ?? [] as a}
        <p>
          <span class="state {a.state}">{a.state}</span>
          {a.hostname}{#if a.waiting} · {plural(a.waiting, 'copy waits', 'copies wait')}{/if}
        </p>
      {:else}
        <p class="warn">None paired: run <code>sevjala discover</code>, then <code>sevjala pair</code></p>
      {/each}
      <a href="#/pcs">Show the diagram →</a>
    </article>
  </section>
{:else}
  <section class="grid">
    <article class="card">
      <div class="label">This backup PC holds</div>
      <h3>{plural(status.store?.backups ?? 0, 'backup', 'backups')}</h3>
      <p class="muted">{status.store?.path}</p>
      {#each status.store?.problems ?? [] as p}<p class="warn">{p}</p>{/each}
    </article>
    <article class="card">
      <div class="label">The controller</div>
      <h3>{h.controller_contact_at ? `checked in ${when(h.controller_contact_at, now)}` : 'has not checked in yet'}</h3>
      <p class="muted">The controller sends the backups here and checks on this PC every 15 seconds.</p>
    </article>
  </section>
{/if}

<section class="card this-pc">
  <div><span class="label">This PC</span> {status.hostname}</div>
  <div><span class="label">Disk free</span> {bytes(h.free_bytes ?? 0)}</div>
  <div><span class="label">Sevjala</span> {status.version}, up {upFor(status.uptime_s ?? 0)}</div>
  <div><span class="label">Data</span> <code>{status.data_dir}</code></div>
</section>

<style>
  .grid {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
    gap: 16px;
  }
  .label {
    font-size: 12px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--muted);
  }
  h3 {
    font-size: 18px;
    margin: 2px 0 8px;
  }
  p {
    margin: 6px 0;
  }
  .warn {
    color: var(--yellow);
  }
  .bad {
    color: var(--red);
  }
  .primary {
    margin-top: 6px;
    padding: 9px 18px;
    border: 0;
    border-radius: 8px;
    background: var(--accent);
    color: #fff;
    font-weight: 600;
  }
  .primary:disabled {
    opacity: 0.6;
    cursor: default;
  }
  .state {
    display: inline-block;
    min-width: 62px;
    font-size: 12px;
    font-weight: 600;
    text-transform: uppercase;
  }
  .online {
    color: var(--green);
  }
  .warning {
    color: var(--yellow);
  }
  .offline,
  .error {
    color: var(--red);
  }
  a {
    color: var(--accent);
  }
  .this-pc {
    display: flex;
    flex-wrap: wrap;
    gap: 8px 28px;
  }
</style>
