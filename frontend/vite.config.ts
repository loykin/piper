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
          // Browser navigation (text/html) → let Vite serve index.html for SPA routing
          if (req.headers.accept?.includes('text/html')) return req.url
          return undefined // API fetch → proxy to backend
        },
      },
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/schedules': 'http://localhost:8080',
      '/services': 'http://localhost:8080',
    },
  },
  build: { outDir: 'dist' },
})
