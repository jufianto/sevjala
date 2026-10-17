<script>
  import { live } from './lib/live.svelte.js'
  import { verdict } from './lib/verdict.js'
  import Verdict from './Verdict.svelte'
  import Dashboard from './Dashboard.svelte'
  import BackupPCs from './BackupPCs.svelte'

  let route = $state(location.hash || '#/')
  $effect(() => {
    const on = () => (route = location.hash || '#/')
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
    <svg viewBox="0 0 32 32" width="26" height="26" aria-hidden="true"
      ><path d="M16 2 4 7v8c0 7.5 5.1 13.4 12 15 6.9-1.6 12-7.5 12-15V7z" style:fill="var(--{v.color})" /></svg
    >
    <div>
      <h1>Sevjala</h1>
      {#if st}<div class="muted small">{st.hostname} · {controller ? 'controller (the database PC)' : 'backup PC'}</div>{/if}
    </div>
  </div>
  {#if controller}
    <nav>
      <a href="#/" class:on={route !== '#/pcs'}>Dashboard</a>
      <a href="#/pcs" class:on={route === '#/pcs'}>Backup PCs</a>
    </nav>
  {/if}
</header>

<main>
  {#if live.lost}
    <div class="lost card">
      This page lost its connection to <code>sevjala ui</code>. It stops by itself a minute after its tab is
      closed; open the dashboard again with <code>sevjala ui</code> (or the tray's “Open dashboard”).
    </div>
  {/if}

  <Verdict {v} />

  {#if st && !live.down}
    {#if controller && route === '#/pcs'}
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
  .small {
    font-size: 13px;
  }
  nav {
    display: flex;
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
</style>
