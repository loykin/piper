import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import tailwindcss from '@tailwindcss/vite'
import path from 'path'

export default defineConfig({
  base: '/ui/',
  plugins: [react(), tailwindcss()],
  resolve: {
    dedupe: ['react', 'react-dom', '@base-ui/react'],
    alias: {
      '@': path.resolve(__dirname, './src'),
      'react': path.resolve(__dirname, 'node_modules/react'),
      'react-dom': path.resolve(__dirname, 'node_modules/react-dom'),
    },
  },
  server: {
    proxy: {
      '/runs': 'http://localhost:8080',
      '/schedules': 'http://localhost:8080',
      '/api': 'http://localhost:8080',
      '/health': 'http://localhost:8080',
      '/serving': 'http://localhost:8080',
      '/notebooks': { target: 'http://localhost:8080', ws: true },
      '/notebook-volumes': 'http://localhost:8080',
      '/services': 'http://localhost:8080',
      '/custom': 'http://localhost:8080',
    },
  },
  build: { outDir: 'dist' },
})
