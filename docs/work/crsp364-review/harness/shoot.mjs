import { chromium } from '@playwright/test';
import { mkdirSync, readFileSync } from 'node:fs';
const [, , out, file, port, only] = process.argv;
const jobs = JSON.parse(readFileSync(file, 'utf8')).filter((j) => !only || only.split(',').includes(j.name));
mkdirSync(out, { recursive: true });
const b = await chromium.launch();
const variants = [['blocks', null], ['dots', 'dotted']].filter(([n]) => !process.env.ONLY || n === process.env.ONLY);
for (const j of jobs) {
  for (const [gn, g] of variants) {
    for (const [vn, w] of [['1300', 1300], ['393', 393]]) {
      const ctx = await b.newContext({ viewport: { width: w, height: 900 } });
      await ctx.addInitScript((g) => { localStorage.setItem('crsp.theme-name', 'terminal'); localStorage.setItem('crsp.theme-mode', 'dark'); if (g) localStorage.setItem('crsp.theme-graphs', g); }, g);
      const p = await ctx.newPage();
      p.on('console', (m) => { if (m.text().startsWith('MISSING')) console.log(j.name, m.text()); });
      p.on('pageerror', (e) => console.log(j.name, 'PAGEERROR', e.message));
      await p.goto(`http://localhost:${port}/shots/shots.html?path=${encodeURIComponent(j.path)}&${j.query ?? ''}`);
      await p.waitForTimeout(j.wait ?? 2500);
      for (const st of j.steps ?? []) { if (st.click) await p.getByRole(st.role ?? 'button', { name: st.click }).first().click(); await p.waitForTimeout(st.wait ?? 800); }
      const h = await p.evaluate(() => document.documentElement.scrollHeight);
      await p.setViewportSize({ width: w, height: Math.min(h + 20, 12000) });
      await p.waitForTimeout(400);
      const text = await p.evaluate(() => document.body.innerText);
      if (/Could not load|No mock for/.test(text)) console.log(j.name, gn, vn, 'HAS ERROR BOX');
      await p.screenshot({ path: `${out}/${j.name}-${gn}-${vn}.png`, fullPage: true });
      await ctx.close();
    }
  }
}
await b.close();
