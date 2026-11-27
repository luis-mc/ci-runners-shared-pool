import react from '@vitejs/plugin-react';
import { defineConfig } from 'vite';

// The client build is one stylesheet (the shared UI package's) and one small script that picks the visitor's
// system. The pages themselves are rendered to static HTML by scripts/build.mjs (website R6.2, R6.6).
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    manifest: true,
    cssCodeSplit: false,
    modulePreload: false,
    rollupOptions: { input: 'src/client.ts' },
  },
});
