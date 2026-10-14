#!/usr/bin/env node
// Renders the PNGs in docs/assets from the HTML sources in docs/assets/src.
//
//   make demo     # optional: re-record the run in the terminal screenshot
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
  execFileSync('go', ['run', '.', 'plan', '--profile', 'profile.example.json', '--out', tmp], {
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

function xterm256(n) {
  const base = ['#000000', '#cd3131', '#0dbc79', '#e5e510', '#2472c8', '#bc3fbc', '#11a8cd', '#e5e5e5',
    '#666666', '#f14c4c', '#23d18b', '#f5f543', '#3b8eea', '#d670d6', '#29b8db', '#ffffff'];
  if (n < 16) return base[n];
  if (n >= 232) { const v = 8 + (n - 232) * 10; return `rgb(${v},${v},${v})`; }
  n -= 16;
  const c = (x) => (x === 0 ? 0 : 55 + x * 40);
  return `rgb(${c(Math.floor(n / 36))},${c(Math.floor(n / 6) % 6)},${c(n % 6)})`;
}

// Converts SGR escape sequences (bold, faint, 16/256/true color) to spans.
function ansiToHtml(text) {
  const esc = (s) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
  let html = '';
  let open = 0;
  let state = { bold: false, faint: false, color: null };
  const emit = () => {
    html += '</span>'.repeat(open);
    open = 0;
    const css = [];
    if (state.bold) css.push('font-weight:700;color:#f4f6f7');
    if (state.faint) css.push('opacity:.6');
    if (state.color) css.push('color:' + state.color);
    if (css.length) { html += `<span style="${css.join(';')}">`; open = 1; }
  };
  for (const part of text.split(/(\x1b\[[0-9;]*m)/)) {
    const m = part.match(/^\x1b\[([0-9;]*)m$/);
    if (!m) { html += esc(part); continue; }
    const codes = (m[1] || '0').split(';').map(Number);
    for (let i = 0; i < codes.length; i++) {
      const c = codes[i];
      if (c === 0) state = { bold: false, faint: false, color: null };
      else if (c === 1) state.bold = true;
      else if (c === 2) state.faint = true;
      else if (c === 22) { state.bold = false; state.faint = false; }
      else if (c === 39) state.color = null;
      else if (c >= 30 && c <= 37) state.color = xterm256(c - 30);
      else if (c >= 90 && c <= 97) state.color = xterm256(c - 90 + 8);
      else if (c === 38 && codes[i + 1] === 5) { state.color = xterm256(codes[i + 2]); i += 2; }
      else if (c === 38 && codes[i + 1] === 2) { state.color = `rgb(${codes[i + 2]},${codes[i + 3]},${codes[i + 4]})`; i += 4; }
    }
    emit();
  }
  return html + '</span>'.repeat(open);
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
  fs.writeFileSync(path.join(src, 'plan-demo.ansi'), execFileSync('go', ['run', '.', 'plan', '--profile', 'profile.example.json'], {
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
