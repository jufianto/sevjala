import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

// Windows 7 keeps Chrome 109 and Firefox 115 as its last browsers.
export default defineConfig({
  plugins: [svelte()],
  build: { target: ['chrome109', 'firefox115'] },
})
