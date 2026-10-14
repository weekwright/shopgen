// Applies ?theme=light|dark before first paint.
document.documentElement.dataset.theme = new URLSearchParams(location.search).get('theme') || 'light';

// Tiny helpers shared by the pages.
window.h = (tag, attrs = {}, ...kids) => {
  const svg = ['svg', 'path', 'rect', 'line', 'circle', 'g', 'text', 'polyline', 'defs', 'marker', 'pattern'].includes(tag);
  const el = svg ? document.createElementNS('http://www.w3.org/2000/svg', tag) : document.createElement(tag);
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
  for (const k of kids) el.append(k);
  return el;
};

// The shopgen mark: a receipt with a rising bar chart.
window.logoSVG = (size) => `
<svg width="${size}" height="${size}" viewBox="0 0 64 64" fill="none" xmlns="http://www.w3.org/2000/svg">
  <rect x="2" y="2" width="60" height="60" rx="16" fill="var(--brand)"/>
  <path d="M18 14h28v34l-4.7-3-4.6 3-4.7-3-4.6 3-4.7-3-4.7 3z" fill="#fff" fill-opacity=".96"/>
  <rect x="23" y="31" width="4" height="8" rx="1.5" fill="var(--brand)"/>
  <rect x="30" y="26" width="4" height="13" rx="1.5" fill="var(--brand)"/>
  <rect x="37" y="20" width="4" height="19" rx="1.5" fill="var(--brand)"/>
</svg>`;

// Diagram wiring: anchors computed from rendered boxes.
window.wires = (svgId) => {
  const svg = document.getElementById(svgId);
  svg.innerHTML = ['ink-2', 'brand', 'red', 'warm'].map((c) =>
    `<marker id="m-${c}" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse"><path d="M0,0 L10,5 L0,10 z" fill="var(--${c})"/></marker>`).join('');
  svg.innerHTML = '<defs>' + svg.innerHTML + '</defs>';
  const canvas = document.getElementById('canvas');
  return {
    box(id) {
      const c = canvas.getBoundingClientRect();
      const r = document.getElementById(id).getBoundingClientRect();
      return { l: r.left - c.left, r: r.right - c.left, t: r.top - c.top, b: r.bottom - c.top, cx: (r.left + r.right) / 2 - c.left, cy: (r.top + r.bottom) / 2 - c.top };
    },
    curve(x1, y1, x2, y2) { const m = (x1 + x2) / 2; return `M${x1} ${y1} C ${m} ${y1}, ${m} ${y2}, ${x2} ${y2}`; },
    wire(d, color = 'ink-2', dashed = false) {
      svg.append(h('path', { d, fill: 'none', stroke: `var(--${color})`, 'stroke-width': 2, 'marker-end': `url(#m-${color})`, ...(dashed ? { 'stroke-dasharray': '6 6' } : {}) }));
    },
    put(id, x, y) { Object.assign(document.getElementById(id).style, { left: x + 'px', top: y + 'px' }); },
  };
};
