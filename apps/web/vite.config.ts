import react from '@vitejs/plugin-react';
import { existsSync, readdirSync, readFileSync, writeFileSync } from 'node:fs';
import { join, resolve } from 'node:path';
import { brotliCompressSync, constants, gzipSync } from 'node:zlib';
import { defineConfig } from 'vite';
import type { Plugin } from 'vite';

// The dev server proxies the controller so the app can call /healthz and /api. The controller
// serves plain HTTP on 127.0.0.1 (R5.1, D38), and it sends the controller's own Origin because the controller refuses a
// change from another one (R6.2).
const controller = 'http://127.0.0.1:8460';
const toController = {
  target: controller,
  configure: (proxy: {
    on: (
      e: string,
      f: (req: { setHeader: (k: string, v: string) => void }) => void,
    ) => void;
  }) => {
    proxy.on('proxyReq', (req) => req.setHeader('Origin', controller));
  },
};

// CRSP296 (web-console R6.14): ship every compressible hashed asset (.js, .css) next to a gzip and a brotli copy, made once at build time.
// The controller serves the copy the browser asks for (internal/webui) and never compresses at request time.
function precompress(): Plugin {
  let outDir = 'dist';
  let write = true;
  return {
    name: 'crsp-precompress',
    apply: 'build',
    configResolved(config) {
      outDir = resolve(config.root, config.build.outDir);
      write = config.build.write;
    },
    // After the write: vite adds the stylesheet late, so read what is on disk.
    closeBundle() {
      const assets = join(outDir, 'assets');
      if (!write || !existsSync(assets)) return;
      for (const name of readdirSync(assets)) {
        if (!/\.(js|css)$/.test(name)) continue;
        const data = readFileSync(join(assets, name));
        writeFileSync(
          join(assets, `${name}.br`),
          brotliCompressSync(data, {
            params: {
              [constants.BROTLI_PARAM_QUALITY]: 11,
              [constants.BROTLI_PARAM_SIZE_HINT]: data.length,
            },
          }),
        );
        writeFileSync(join(assets, `${name}.gz`), gzipSync(data, { level: 9 }));
      }
    },
  };
}

export default defineConfig({
  plugins: [react(), precompress()],
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    // One script and one stylesheet (R6.14): no per-screen chunks, so no modulepreload list and fewer requests.
    cssCodeSplit: false,
    modulePreload: false,
    rollupOptions: { output: { inlineDynamicImports: true } },
  },
  server: {
    proxy: {
      '/healthz': toController,
      '/api': toController,
    },
  },
});
