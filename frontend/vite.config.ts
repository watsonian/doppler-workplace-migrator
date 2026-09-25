import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      '@': path.resolve(__dirname, './src'),
    },
  },
  server: {
    port: 5173,
    proxy: {
      '/api/v1/events': {
        target: 'http://localhost:8080',
        changeOrigin: true,
        // SSE requires no response buffering and no compression
        headers: { 'Accept': 'text/event-stream' },
        configure: (proxy) => {
          proxy.on('proxyReq', (_proxyReq, _req, res) => {
            // Disable Node.js socket buffering (Nagle's algorithm)
            res.socket?.setNoDelay(true);
          });
        },
      },
      '/api': {
        target: 'http://localhost:8080',
        changeOrigin: true,
      },
    },
  },
  build: {
    outDir: 'dist',
    sourcemap: false,
  },
});
