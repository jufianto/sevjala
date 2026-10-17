<script>
  import { get, every } from './lib/api.js'
  import { clock } from './lib/format.js'
  import { filterLogs } from './lib/logs.js'

  let { now } = $props()
  let entries = $state([])
  let level = $state('info')
  let text = $state('')
  let err = $state('')

  async function load() {
    try {
      entries = (await get('/logs?limit=500')).entries
      err = ''
    } catch (e) {
      err = e.message
    }
  }
  $effect(() => every(10e3, load))

  const shown = $derived(filterLogs(entries, { level, text }))
  const tone = { WARN: 'yellow', ERROR: 'red' }
</script>

<section class="card">
  <h2>Logs</h2>
  <p class="muted">What Sevjala did, newest first. “By” is the account that asked for a change; scheduled work has none.</p>
  <div class="toolbar">
    <label>Show <select bind:value={level}>
      <option value="info">everything</option>
      <option value="warn">warnings and errors</option>
      <option value="error">errors</option>
    </select></label>
    <input bind:value={text} placeholder="Search" aria-label="Search the log" />
    <button class="plain" onclick={load}>Refresh</button>
  </div>
  {#if err}<p class="msg bad">{err}</p>{/if}
  <div class="scroll">
    <table class="list">
      <thead><tr><th>Time</th><th>Level</th><th>Event</th><th>Message</th><th>By</th></tr></thead>
      <tbody>
        {#each shown as e, i (i)}
          <tr>
            <td class="nowrap">{e.time ? clock(new Date(e.time), now) : ''}</td>
            <td class={tone[e.level] ?? ''}>{e.level}</td>
            <td>{e.event ?? ''}</td>
            <td>{e.msg}{e.err ? `: ${e.err}` : ''}</td>
            <td>{e.by ?? ''}</td>
          </tr>
        {:else}
          <tr><td colspan="5" class="muted">Nothing to show.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</section>

<style>
  .nowrap {
    white-space: nowrap;
  }
  .yellow {
    color: var(--yellow);
  }
  .red {
    color: var(--red);
  }
</style>
