<script>
  import { live } from './lib/live.svelte.js'
  import { verdict } from './lib/verdict.js'
  import Verdict from './Verdict.svelte'
  import Dashboard from './Dashboard.svelte'
  import BackupPCs from './BackupPCs.svelte'
  import Backups from './Backups.svelte'
  import Restore from './Restore.svelte'
  import Schedules from './Schedules.svelte'
  import Database from './Database.svelte'
  import Logs from './Logs.svelte'
  import { parse } from './lib/route.js'

  const nav = [
    ['', 'Dashboard'],
    ['backups', 'Backups'],
    ['restore', 'Restore'],
    ['pcs', 'Backup PCs'],
    ['schedules', 'Schedules'],
    ['database', 'Database'],
    ['logs', 'Logs'],
  ]

  let route = $state(parse(location.hash))
  $effect(() => {
    const on = () => (route = parse(location.hash))
    addEventListener('hashchange', on)
    return () => removeEventListener('hashchange', on)
  })

  const st = $derived(live.status)
  const controller = $derived(st?.role === 'controller')
  const v = $derived(verdict(live))
  $effect(() => {
    document.title = `Sevjala · ${st?.hostname ?? ''} · ${v.color === 'green' ? 'OK' : v.headline}`
  })
</script>

<header>
  <div class="brand">
    <div class="logo" aria-hidden="true">
      <img src="/icon.png" width="32" height="32" alt="" />
      <span class="dot" style:background="var(--{v.color})"></span>
    </div>
    <div>
      <h1>Sevjala</h1>
      {#if st}<div class="muted small">{st.hostname} · {controller ? 'controller (the database PC)' : 'backup PC'}</div>{/if}
    </div>
  </div>
  {#if controller}
    <nav>
      {#each nav as [page, label]}
        <a href="#/{page}" class:on={route.page === page}>{label}</a>
      {/each}
    </nav>
  {/if}
</header>

<main>
  {#if live.via && st}
    <div class="via card">{st.hostname} · controller — opened from {live.via}</div>
  {/if}
  {#if live.lost}
    <div class="lost card">
      This page lost its connection to <code>sevjala ui</code>. It stops by itself a minute after its tab is
      closed; open the dashboard again with
      {#if live.via}<code>sevjala ui -c</code> (or the tray's “Open controller dashboard”).{:else}<code
          >sevjala ui</code
        > (or the tray's “Open dashboard”).{/if}
    </div>
  {/if}

  <Verdict {v} />

  {#if st && !live.down}
    {#if controller && route.page === 'backups'}
      <Backups status={st} agents={live.agents} now={live.now} />
    {:else if controller && route.page === 'restore'}
      <Restore params={route.params} now={live.now} />
    {:else if controller && route.page === 'database'}
      <Database />
    {:else if controller && route.page === 'logs'}
      <Logs now={live.now} />
    {:else if controller && route.page === 'schedules'}
      <Schedules agents={live.agents} now={live.now} />
    {:else if controller && route.page === 'pcs'}
      <BackupPCs status={st} agents={live.agents} now={live.now} />
    {:else}
      <Dashboard status={st} now={live.now} />
    {/if}
  {/if}
</main>

<style>
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 16px;
    flex-wrap: wrap;
    padding: 14px 24px;
    background: var(--card);
    border-bottom: 1px solid var(--line);
  }
  .brand {
    display: flex;
    gap: 10px;
    align-items: center;
  }
  h1 {
    font-size: 18px;
  }
  .logo {
    position: relative;
    width: 32px;
    height: 32px;
  }
  .logo img {
    display: block;
  }
  .dot {
    position: absolute;
    right: -2px;
    bottom: -2px;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    border: 2px solid var(--card);
  }
  .small {
    font-size: 13px;
  }
  nav {
    display: flex;
    flex-wrap: wrap;
    gap: 4px;
  }
  nav a {
    padding: 7px 14px;
    border-radius: 8px;
    color: var(--muted);
    text-decoration: none;
  }
  nav a.on {
    background: var(--grey-bg);
    color: var(--text);
    font-weight: 600;
  }
  main {
    max-width: 1000px;
    margin: 0 auto;
    padding: 20px 16px 40px;
    display: grid;
    gap: 16px;
  }
  .lost {
    border-color: var(--yellow);
    background: var(--yellow-bg);
  }
  .via {
    font-weight: 600;
    border-color: var(--accent);
  }
</style>
