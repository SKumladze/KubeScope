import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'

// The production build goes into the Go binary (go:embed).
// In dev, Vite proxies the API and WebSocket to the collector on :8090.
export default defineConfig({
  plugins: [react()],
  build: { outDir: '../collector/internal/server/dist', emptyOutDir: true },
  server: {
    proxy: {
      '/api': 'http://localhost:8090',
      '/ws': { target: 'ws://localhost:8090', ws: true },
    },
  },
})
