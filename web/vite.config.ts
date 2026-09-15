import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import stylex from '@stylexjs/unplugin';

// Official StyleX Vite integration (@stylexjs/unplugin). Output is embedded by
// web/embed.go; no dev proxy exists because the daemon refuses foreign origins.
export default defineConfig({
  plugins: [stylex.vite({ useCSSLayers: true, dev: false, runtimeInjection: false }), react()],
  build: { modulePreload: false, target: 'es2023' },
});
