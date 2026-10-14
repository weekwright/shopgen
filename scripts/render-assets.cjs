#!/usr/bin/env node
// Renders the PNGs in docs/assets from the HTML sources in docs/assets/src.
//
//   DEMO_OUT=$PWD/docs/assets/src/run-demo.ansi go test -run TestDocsDemo -count=1 .   # optional
//   node scripts/render-assets.cjs
//
// Needs Go (to generate real plan data) and Playwright with Chromium
// (npm i -g playwright && npx playwright install chromium).
'use strict';

const { execFileSync } = require('node:child_process');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');

const root = path.resolve(__dirname, '..');
const src = path.join(root, 'docs', 'assets', 'src');
const out = path.join(root, 'docs', 'assets');

let chromium;
try {
  ({ chromium } = require('playwright'));
} catch {
  console.error('playwright is not installed: npm i -g playwright (and set NODE_PATH to your global node_modules)');
  process.exit(1);
}

// ---------------------------------------------------------------------------
// 1. Real data: plan profile.example.json and bucket it by local day.

function planData() {
  const tmp = path.join(os.tmpdir(), `shopgen-plan-${process.pid}.jsonl`);
  execFileSync('go', ['run', '.', 'plan', '-profile', 'profile.example.json', '-out', tmp], {
    cwd: root,
    env: { ...process.env, SHOPIFY_STORE: '', NO_COLOR: '1' },
    stdio: ['ignore', 'ignore', 'inherit'],
  });
  const profile = JSON.parse(fs.readFileSync(path.join(root, 'profile.example.json'), 'utf8'));
  const orders = fs.readFileSync(tmp, 'utf8').trim().split('\n').map((l) => JSON.parse(l));
  fs.unlinkSync(tmp);

  const fmt = new Intl.DateTimeFormat('en-CA', { timeZone: profile.timezone, year: 'numeric', month: '2-digit', day: '2-digit' });
  const days = [];
  const start = new Date(profile.start + 'T00:00:00Z');
  const end = new Date(profile.end + 'T00:00:00Z');
  for (let d = new Date(start); d <= end; d.setUTCDate(d.getUTCDate() + 1)) days.push(d.toISOString().slice(0, 10));
  const counts = Object.fromEntries(days.map((d) => [d, 0]));
  for (const o of orders) counts[fmt.format(new Date(o.processed_at))]++;

  // Expected mean without noise: the generator's formula with Z = 0.
  const weekly = profile.weekly || [0.9, 0.95, 1, 1, 1, 1.05, 1.1];
  const g = profile.growth || 0;
  const D = days.length;
  const expected = days.map((day, d) => {
    const t = D > 1 ? d / (D - 1) : 0;
    let s = 1;
    for (const sp of profile.spikes || []) {
      const from = new Date(sp.date + 'T00:00:00Z');
      const to = new Date(from); to.setUTCDate(to.getUTCDate() + (sp.days || 1));
      const cur = new Date(day + 'T00:00:00Z');
      if (cur >= from && cur < to) s *= sp.multiplier;
    }
    const wd = new Date(day + 'T00:00:00Z').getUTCDay();
    return profile.orders_per_day * (1 + g * t) / (1 + g / 2) * weekly[wd] * s;
  });

  const sample = orders.slice(-400).filter((o, i) => i % 97 === 3).slice(0, 4).map((o) => ({
    name: '#' + (1001 + o.index), outcome: o.outcome, total: (o.total_cents / 100).toFixed(2),
    when: new Date(o.processed_at).toLocaleString('en-US', { timeZone: profile.timezone, month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }),
  }));
  return { days, counts: days.map((d) => counts[d]), expected, total: orders.length, sample, profile };
}

// ---------------------------------------------------------------------------
// 2. ANSI (as emitted by shopgen's UI) to HTML spans.

function ansiToHtml(text) {
  const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  const classes = { 1: 'b', 2: 'd', 31: 'red', 32: 'green', 33: 'yellow', 34: 'blue', 35: 'magenta', 36: 'cyan', '38;5;77': 'brand' };
  let html = '';
  let open = [];
  for (const part of text.split(/(\x1b\[[0-9;]*m)/)) {
    const m = part.match(/^\x1b\[([0-9;]*)m$/);
    if (!m) { html += esc(part); continue; }
    if (m[1] === '0' || m[1] === '') { html += '</span>'.repeat(open.length); open = []; continue; }
    const cls = classes[m[1]] || '';
    html += `<span class="${cls}">`;
    open.push(cls);
  }
  return html + '</span>'.repeat(open.length);
}

// ---------------------------------------------------------------------------
// 3. Screenshot every source page in light and dark.

const pages = [
  { name: 'header', themes: ['light', 'dark'] },
  { name: 'architecture', themes: ['light', 'dark'] },
  { name: 'lifecycle', themes: ['light', 'dark'] },
  { name: 'volume', themes: ['light', 'dark'] },
  { name: 'terminal', themes: ['dark'] },
  { name: 'social', file: 'header', query: '&h=640', themes: ['dark'] },
];

(async () => {
  const data = planData();
  // The plan screenshot is captured fresh so its numbers match the charts.
  fs.writeFileSync(path.join(src, 'plan-demo.ansi'), execFileSync('go', ['run', '.', 'plan', '-profile', 'profile.example.json'], {
    cwd: root, env: { ...process.env, SHOPIFY_STORE: '', FORCE_COLOR: '1' }, stdio: ['ignore', 'pipe', 'ignore'],
  }));
  const ansi = (f) => ansiToHtml(fs.readFileSync(path.join(src, f), 'utf8'));
  const payload = {
    ...data,
    planAnsi: ansi('plan-demo.ansi'),
    runAnsi: ansi('run-demo.ansi'),
  };
  fs.writeFileSync(path.join(src, 'data.js'), '// Generated by scripts/render-assets.cjs; do not edit.\nwindow.SHOPGEN = ' + JSON.stringify(payload) + ';\n');

  const browser = await chromium.launch();
  const only = process.env.ONLY ? process.env.ONLY.split(',') : null;
  for (const p of pages.filter((x) => !only || only.includes(x.name))) {
    for (const theme of p.themes) {
      const page = await browser.newPage({ deviceScaleFactor: 2, viewport: { width: 1800, height: 1200 } });
      await page.goto('file://' + path.join(src, (p.file || p.name) + '.html') + '?theme=' + theme + (p.query || ''));
      await page.evaluate(() => document.fonts.ready);
      const el = await page.$('#canvas');
      const file = p.themes.length > 1 ? `${p.name}-${theme}.png` : `${p.name}.png`;
      await el.screenshot({ path: path.join(out, file), omitBackground: false });
      console.log('wrote docs/assets/' + file);
      await page.close();
    }
  }
  await browser.close();
})().catch((err) => { console.error(err); process.exit(1); });
