// usage: node shoot2.mjs outdir port jobs.json   jobs: {name,path,query,theme,pal,mode,graphs,widths:[..],selector?}
import { chromium } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
const [, , out, port, file, only] = process.argv;
const jobs = JSON.parse(readFileSync(file, 'utf8')).filter((j) => !only || only.split(',').includes(j.name));
mkdirSync(out, { recursive: true });
const b = await chromium.launch();
for (const j of jobs) {
  for (const w of j.widths ?? [1300]) {
    const ctx = await b.newContext({ viewport: { width: w, height: 900 }, colorScheme: j.scheme ?? 'light' });
    await ctx.addInitScript((j) => {
      localStorage.setItem('crsp.theme-name', j.theme ?? 'terminal');
      if (j.pal) localStorage.setItem('crsp.theme-palette', j.pal);
      localStorage.setItem('crsp.theme-mode', j.mode ?? 'light');
      if (j.graphs) localStorage.setItem('crsp.theme-graphs', j.graphs);
    }, j);
    const p = await ctx.newPage();
    p.on('pageerror', (e) => console.log(j.name, 'PAGEERROR', e.message));
    await p.goto(`http://localhost:${port}/shots/shots.html?path=${encodeURIComponent(j.path)}&${j.query ?? ''}`);
    await p.waitForTimeout(j.wait ?? 2500);
    const h = await p.evaluate(() => document.documentElement.scrollHeight);
    await p.setViewportSize({ width: w, height: Math.min(h + 20, 12000) });
    await p.waitForTimeout(400);
    const over = await p.evaluate(() => { const W = document.documentElement.clientWidth; return [...document.querySelectorAll('body *')].filter((e) => e.getBoundingClientRect().right > W + 1).slice(0, 8).map((e) => e.tagName + '.' + String(e.className).slice(0, 50) + ' ' + Math.round(e.getBoundingClientRect().right) + ' ' + (e.textContent || '').slice(0, 40)); });
    if (over.length) console.log(j.name, w, 'OVERFLOW', over);
    const text = await p.evaluate(() => document.body.innerText);
    if (/Could not load|No mock for/.test(text)) console.log(j.name, w, 'HAS ERROR BOX');
    await p.screenshot({ path: `${out}/${j.name}-${w}.png`, fullPage: true });
    await ctx.close();
  }
}
await b.close();
