import { defineConfig } from 'vite'
import { svelte } from '@sveltejs/vite-plugin-svelte'

export default defineConfig({
  plugins: [svelte()],
  base: '/static/',
  build: {
    outDir: '../web',
    emptyOutDir: true,
    rollupOptions: {
      input: 'index.html',
    },
  },
  server: {
    proxy: {
      '/api': 'https://cc.lamparelli.eu',
      '/apps': 'https://cc.lamparelli.eu',
      '/auth': 'https://cc.lamparelli.eu',
    },
  },
})
