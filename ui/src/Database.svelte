<script>
  import { api, get, adminWhere } from './lib/api.js'
  import { live } from './lib/live.svelte.js'
  import { plural } from './lib/format.js'

  let cfg = $state(null)
  let form = $state(null)
  let found = $state(null)
  let err = $state('')
  let note = $state('')
  let busy = $state(false)

  async function load() {
    try {
      cfg = await get('/db')
      form = { host: cfg.host, port: cfg.port, user: cfg.user, password: '', noPassword: false, databases: [...cfg.databases] }
      err = ''
    } catch (e) {
      err = e.message
    }
  }
  $effect(() => {
    load()
  })

  async function detect() {
    busy = true
    err = ''
    try {
      found = await api('POST', '/db/detect')
    } catch (e) {
      err = e.message
    } finally {
      busy = false
    }
  }

  const moved = $derived(form && (form.host !== cfg.host || Number(form.port) !== cfg.port || form.user !== cfg.user))
  const needsPassword = $derived(moved && !form.password && !form.noPassword)
  const names = $derived([...new Set([...(found?.databases ?? []).map((d) => d.name), ...(cfg?.databases ?? [])])].sort())

  async function save() {
    const s = {}
    if (form.host !== cfg.host) s.host = form.host.trim()
    if (Number(form.port) !== cfg.port) s.port = Number(form.port)
    if (form.user !== cfg.user) s.user = form.user.trim()
    if (form.noPassword) s.password = ''
    else if (form.password) s.password = form.password
    const dbs = [...form.databases].sort()
    if (dbs.join() !== [...cfg.databases].sort().join()) s.databases = dbs
    if (!Object.keys(s).length) {
      note = 'Nothing changed.'
      return
    }
    busy = true
    err = ''
    try {
      found = await api('PUT', '/db', s)
      note = 'Saved. Sevjala tested the connection first.'
      await load()
    } catch (e) {
      err = e.message
    } finally {
      busy = false
    }
  }
</script>

<section class="card">
  <h2>Database</h2>
  <p class="muted">The MySQL or MariaDB server on this PC and the databases Sevjala backs up. Every change is tested against the server before it is saved.</p>
  {#if form}
    <form class="form" onsubmit={(e) => (e.preventDefault(), save())}>
      <div class="row">
        <label>Host <input bind:value={form.host} /></label>
        <label>Port <input type="number" min="1" max="65535" bind:value={form.port} /></label>
        <label>User <input bind:value={form.user} autocomplete="off" /></label>
      </div>
      <label>
        Password {cfg.has_password ? '(a password is saved; leave empty to keep it)' : '(none saved)'}
        <input type="password" bind:value={form.password} autocomplete="new-password" disabled={form.noPassword} />
      </label>
      <label class="inline"><input type="checkbox" bind:checked={form.noPassword} /> connect without a password</label>
      {#if needsPassword}<p class="msg bad">Changing the host, port or user needs the password again.</p>{/if}

      <fieldset>
        <legend>Back up these databases</legend>
        <div class="toolbar"><button type="button" class="plain" onclick={detect} disabled={busy}>Detect</button></div>
        {#if found}
          <p class="muted">{found.flavour} {found.server_version} · dump tool {found.dumper}</p>
        {/if}
        {#each names as n}
          {@const d = found?.databases.find((x) => x.name === n)}
          <label class="inline">
            <input type="checkbox" value={n} bind:group={form.databases} />
            {n}{#if d} <span class="muted">· {plural(d.tables, 'table', 'tables')}{d.non_innodb?.length ? ` · not InnoDB: ${d.non_innodb.join(', ')}` : ''}</span>{/if}
          </label>
        {:else}
          <p class="muted">Press Detect to list the databases on the server.</p>
        {/each}
      </fieldset>

      <label>
        Dump tool
        <input value={cfg.dumper || 'found automatically'} readonly />
      </label>
      <p class="muted">Changing the dump tool needs an administrator: run <code>sevjala db set --dumper &lt;path&gt;</code> {adminWhere(live.via, live.status?.hostname)}.</p>

      <div class="toolbar"><button class="primary" disabled={busy || needsPassword}>{busy ? 'Testing…' : 'Save'}</button></div>
    </form>
  {/if}
  {#if note}<p class="msg ok">{note}</p>{/if}
  {#if err}<p class="msg bad">{err}</p>{/if}
</section>

<style>
  .form {
    display: grid;
    gap: 12px;
    max-width: 560px;
  }
  .row {
    display: grid;
    grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
    gap: 10px;
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
</style>
