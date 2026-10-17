<script>
  let { v } = $props()
</script>

<section class="verdict card {v.color}" aria-live="polite">
  <div class="dot" aria-hidden="true"></div>
  <div>
    <div class="word">{v.word}</div>
    <h2>{v.headline}</h2>
    {#if v.items.length}
      <ul>
        {#each v.items as item}<li>{item}</li>{/each}
      </ul>
    {/if}
  </div>
</section>

<style>
  .verdict {
    display: flex;
    gap: 16px;
    align-items: flex-start;
    border-width: 2px;
    padding: 20px 22px;
  }
  .dot {
    flex: none;
    width: 22px;
    height: 22px;
    margin-top: 4px;
    border-radius: 50%;
    background: var(--c);
  }
  .word {
    font-size: 13px;
    font-weight: 700;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: var(--c);
  }
  h2 {
    font-size: 20px;
    margin-top: 2px;
  }
  ul {
    margin: 10px 0 0;
    padding-left: 18px;
  }
  .green {
    --c: var(--green);
    border-color: var(--green);
    background: var(--green-bg);
  }
  .yellow {
    --c: var(--yellow);
    border-color: var(--yellow);
    background: var(--yellow-bg);
  }
  .red {
    --c: var(--red);
    border-color: var(--red);
    background: var(--red-bg);
  }
  .grey {
    --c: var(--grey);
    border-color: var(--grey);
    background: var(--grey-bg);
  }
</style>
