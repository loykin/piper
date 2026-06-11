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
      '/runs': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/schedules': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/api': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/health': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/serving': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/notebooks': { target: process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080', ws: true },
      '/notebook-volumes': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/services': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
      '/custom': process.env.PIPER_BACKEND_URL ?? 'http://localhost:8080',
    },
  },
  build: {
    outDir: 'dist',
    rollupOptions: {
      output: {
        entryFileNames: 'assets/[name].js',
        chunkFileNames: 'assets/[name].js',
        assetFileNames: 'assets/[name].[ext]',
        manualChunks(id) {
          if (id.includes('node_modules/@xyflow/')) return 'vendor-reactflow'
          if (id.includes('node_modules/@uiw/') || id.includes('node_modules/@codemirror/')) return 'vendor-codemirror'
          if (id.includes('node_modules/@loykin/')) return 'vendor-loykin'
          if (id.includes('node_modules/@tanstack/') || id.includes('node_modules/@base-ui/') || id.includes('node_modules/@floating-ui/')) return 'vendor-ui'
          if (id.includes('node_modules/react') || id.includes('node_modules/react-dom') || id.includes('node_modules/react-router-dom')) return 'vendor-react'
        },
      },
    },
  },
})
