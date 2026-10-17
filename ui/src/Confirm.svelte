<script>
  // Typed confirmation: the button works only once the word the agent named is typed.
  let { need, onconfirm, oncancel } = $props()
  let typed = $state('')
  let dlg
  $effect(() => {
    dlg.showModal()
    return () => dlg.open && dlg.close()
  })
  const ok = $derived(typed.trim() === need.word)
</script>

<dialog bind:this={dlg} oncancel={(e) => (e.preventDefault(), oncancel())}>
  <form method="dialog" onsubmit={(e) => (e.preventDefault(), ok && onconfirm(need.word))}>
    <h2>Are you sure?</h2>
    <p>{need.message}.</p>
    {#if need.hosts.length}<p class="muted">Backup PCs affected: {need.hosts.join(', ')}</p>{/if}
    <label>
      Type <strong>{need.word}</strong> to continue
      <!-- svelte-ignore a11y_autofocus -->
      <input bind:value={typed} autocomplete="off" spellcheck="false" autofocus />
    </label>
    <div class="actions">
      <button type="button" class="plain" onclick={oncancel}>Cancel</button>
      <button type="submit" class="danger" disabled={!ok}>Continue</button>
    </div>
  </form>
</dialog>

<style>
  dialog {
    max-width: min(460px, calc(100vw - 32px));
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--card);
    color: var(--text);
    padding: 20px 22px;
  }
  dialog::backdrop {
    background: rgb(0 0 0 / 0.45);
  }
  label {
    display: grid;
    gap: 6px;
    margin-top: 12px;
  }
  .actions {
    display: flex;
    justify-content: flex-end;
    gap: 8px;
    margin-top: 16px;
  }
</style>
