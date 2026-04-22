import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      '/runs': {
        target: 'http://localhost:8080',
        bypass(req) {
          if (req.headers.accept?.includes('text/html')) return req.url
          return undefined
        },
      },
      '/schedules': {
        target: 'http://localhost:8080',
        bypass(req) {
          // Browser navigation → SPA, API fetch (no text/html accept) → backend
          if (req.headers.accept?.includes('text/html')) return req.url
          return undefined
        },
      },
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/services': {
        target: 'http://localhost:8080',
        bypass(req) {
          if (req.headers.accept?.includes('text/html')) return req.url
          return undefined
        },
      },
    },
  },
  build: { outDir: 'dist' },
})
