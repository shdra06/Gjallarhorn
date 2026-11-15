// Records real console footage for the demo videos, from a running demo, with a headless
// Chromium browser (Chrome or Edge) driven over the DevTools protocol. A drawn mouse pointer
// follows every move and click, so the footage reads like a screen recording.
//
//   ./demo.sh                                                    # start the demo first
//   node console/scripts/record.mjs --out <dir> [--shots hook,copilot,tour] [--size 1920x1080]
//                                   [--voice brag-output/voice] [--quality 55] [--warm 20]
//
// Each shot is saved as <dir>/<shot>.mp4 (30 fps) plus <dir>/<shot>.marks.json, which lists
// when each action happened, in seconds from the first frame, for the video edit. The shots act
// in time with the narration in --voice (marks named vo:<line id> say where each line starts).
// Needs ffmpeg on the PATH. The token is the demo's (GJ_ADMIN_TOKEN, or the demo default).
import { spawn, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
const url = arg('url', 'http://localhost:18788');
const out = resolve(arg('out', 'footage'));
const shots = arg('shots', 'hook,copilot,tour').split(',');
const [W, H] = arg('size', '1920x1080').split('x').map(Number);
const token = process.env.GJ_ADMIN_TOKEN ?? 'demo-admin-token-0123456789abcdef';
const gpu = !process.argv.includes('--swiftshader');
const quality = Number(arg('quality', '55'));
const candidates = [
  arg('browser', process.env.BROWSER),
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].filter(Boolean);
const browserPath = candidates.find((p) => existsSync(p));
if (!browserPath) throw new Error('No Chrome or Edge found; pass --browser <path>');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = 9334;
const profile = mkdtempSync(join(tmpdir(), 'gj-rec-'));
const flags = [
  '--headless=new',
  `--remote-debugging-port=${port}`,
  `--user-data-dir=${profile}`,
  `--window-size=${W},${H}`,
  '--force-device-scale-factor=1',
  '--hide-scrollbars',
  '--ignore-gpu-blocklist',
  '--disable-background-timer-throttling',
  '--disable-renderer-backgrounding',
  '--disable-backgrounding-occluded-windows',
  ...(gpu ? ['--enable-gpu', '--use-angle=d3d11'] : ['--use-angle=swiftshader', '--enable-unsafe-swiftshader']),
  'about:blank',
];
const browser = spawn(browserPath, flags);

async function target() {
  for (let i = 0; i < 50; i++) {
    try {
      const list = await (await fetch(`http://127.0.0.1:${port}/json/list`)).json();
      const page = list.find((t) => t.type === 'page');
      if (page) return page.webSocketDebuggerUrl;
    } catch {
      // not up yet
    }
    await sleep(200);
  }
  throw new Error('the browser did not start');
}

const ws = new WebSocket(await target());
await new Promise((r) => ws.addEventListener('open', r, { once: true }));
let id = 0;
const pending = new Map();
const listeners = new Map();
ws.addEventListener('message', (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg);
    pending.delete(msg.id);
  } else if (msg.method && listeners.has(msg.method)) {
    listeners.get(msg.method)(msg.params);
  }
});
const send = (method, params = {}) =>
  new Promise((res, rej) => {
    const n = ++id;
    pending.set(n, (msg) => (msg.error ? rej(new Error(`${method}: ${msg.error.message}`)) : res(msg.result)));
    ws.send(JSON.stringify({ id: n, method, params }));
  });
const js = async (expression) => {
  const r = await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(`page: ${r.exceptionDetails.exception?.description ?? r.exceptionDetails.text}`);
  return r.result?.value;
};
const auth = `{Authorization: 'Bearer ${token}', 'Content-Type': 'application/json'}`;
const api = (method, path, body, extra = '') =>
  js(`fetch(${JSON.stringify(path)}, {method: ${JSON.stringify(method)}, headers: Object.assign(${auth}, ${extra || '{}'})${
    body ? `, body: ${JSON.stringify(JSON.stringify(body))}` : ''
  }}).then(async r => ({status: r.status, etag: r.headers.get('ETag'), body: await r.text()}))`);

// ---- The drawn pointer -------------------------------------------------------------------------
// An arrow made of two clip-path layers (dark edge, white fill), moved by real mouse events.
const pointerScript = `(() => {
  if (window.__gjPointer) return;
  window.__gjPointer = true;
  const make = () => {
    const wrap = document.createElement('div');
    Object.assign(wrap.style, {position: 'fixed', left: '0', top: '0', width: '26px', height: '38px', zIndex: '2147483647',
      pointerEvents: 'none', transform: 'translate(-100px,-100px)', transformOrigin: '0 0', filter: 'drop-shadow(0 2px 3px rgba(0,0,0,.55))'});
    const shape = 'polygon(0 0, 0 78%, 22% 61%, 38% 96%, 52% 90%, 37% 56%, 66% 56%)';
    const edge = document.createElement('div');
    Object.assign(edge.style, {position: 'absolute', inset: '0', background: '#0b0f18', clipPath: shape});
    const fill = document.createElement('div');
    Object.assign(fill.style, {position: 'absolute', left: '2.5px', top: '4px', width: '20px', height: '30px', background: '#ffffff', clipPath: shape});
    wrap.append(edge, fill);
    document.documentElement.appendChild(wrap);
    let x = -100, y = -100, s = 1;
    const draw = () => { wrap.style.transform = 'translate(' + x + 'px,' + y + 'px) scale(' + s + ')'; };
    window.addEventListener('mousemove', (e) => { x = e.clientX; y = e.clientY; draw(); }, true);
    window.addEventListener('mousedown', () => { s = 0.86; draw(); }, true);
    window.addEventListener('mouseup', () => { s = 1; draw(); }, true);
  };
  if (document.documentElement) make(); else document.addEventListener('DOMContentLoaded', make);
})()`;

let mx = W / 2;
let my = H / 2;
const ease = (t) => (t < 0.5 ? 4 * t * t * t : 1 - (-2 * t + 2) ** 3 / 2);
async function moveTo(x, y, ms = 700) {
  const sx = mx;
  const sy = my;
  // A slight arc, like a hand moving a mouse. Timed by the clock, because each event waits for
  // the page to draw a frame and a busy 3D page draws slower than 60 fps.
  const bend = Math.min(60, Math.hypot(x - sx, y - sy) * 0.08);
  const start = Date.now();
  for (;;) {
    const raw = Math.min(1, (Date.now() - start) / ms);
    const t = ease(raw);
    const arc = Math.sin(Math.PI * t) * bend;
    mx = sx + (x - sx) * t;
    my = sy + (y - sy) * t - arc;
    await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: mx, y: my });
    if (raw >= 1) break;
    await sleep(8);
  }
  mx = x;
  my = y;
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x, y });
}
async function clickAt(x, y, ms) {
  await moveTo(x, y, ms);
  await sleep(140);
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x, y, button: 'left', clickCount: 1 });
  await sleep(90);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x, y, button: 'left', clickCount: 1 });
}
// Finds an element by CSS selector and, optionally, the text it contains; returns its centre.
async function where(selector, text, { scroll = true } = {}) {
  for (let i = 0; i < 60; i++) {
    const r = await js(`(() => {
      const els = [...document.querySelectorAll(${JSON.stringify(selector)})]
        .filter(e => ${text ? `e.textContent.includes(${JSON.stringify(text)})` : 'true'})
        .filter(e => { const b = e.getBoundingClientRect(); return b.width > 0 && b.height > 0; });
      const el = els[0];
      if (!el) return null;
      ${scroll ? "el.scrollIntoView({block: 'nearest', inline: 'nearest'});" : ''}
      const b = el.getBoundingClientRect();
      return {x: b.left + b.width / 2, y: b.top + b.height / 2, w: b.width, h: b.height, top: b.top};
    })()`);
    if (r) return r;
    await sleep(250);
  }
  throw new Error(`not found: ${selector} ${text ?? ''}`);
}
// Moves onto an element and follows it if the layout shifts under the pointer (hovering can
// resize a panel), so the click lands where it should.
async function aim(selector, text, ms = 800, dx = 0) {
  let p = await where(selector, text);
  await moveTo(p.x + dx * p.w, p.y, ms);
  for (let i = 0; i < 4; i++) {
    await sleep(250);
    const q = await where(selector, text, { scroll: false });
    if (Math.hypot(q.x - p.x, q.y - p.y) < 3) break;
    p = q;
    await moveTo(p.x + dx * p.w, p.y, 260);
  }
  return p;
}
async function click(selector, text, ms, dx = 0) {
  let p = await aim(selector, text, ms, dx);
  // Press only when the intended element is the one under the pointer.
  const under = () => js(`(() => {
    const hit = document.elementFromPoint(${mx}, ${my});
    const el = hit && hit.closest(${JSON.stringify(selector)});
    return !!el && ${text ? `el.textContent.includes(${JSON.stringify(text)})` : 'true'};
  })()`);
  for (let i = 0; i < 6 && !(await under()); i++) p = await aim(selector, text, 250, dx);
  if (!(await under())) throw new Error(`the pointer is not over ${selector} ${text ?? ''}`);
  await sleep(120);
  if (marks) marks.push({ name: 'click', wall: Date.now() / 1000 });
  await send('Input.dispatchMouseEvent', { type: 'mousePressed', x: mx, y: my, button: 'left', clickCount: 1 });
  await sleep(90);
  await send('Input.dispatchMouseEvent', { type: 'mouseReleased', x: mx, y: my, button: 'left', clickCount: 1 });
  return p;
}
async function key(k) {
  const special = { Escape: 27, Enter: 13, Tab: 9 };
  if (special[k]) {
    await send('Input.dispatchKeyEvent', { type: 'rawKeyDown', key: k, code: k, windowsVirtualKeyCode: special[k] });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k, code: k, windowsVirtualKeyCode: special[k] });
  } else {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: k, text: k });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: k });
  }
}
async function typeText(text, perChar = 75) {
  for (const ch of text) {
    await send('Input.dispatchKeyEvent', { type: 'keyDown', key: ch, text: ch });
    await send('Input.dispatchKeyEvent', { type: 'keyUp', key: ch });
    await sleep(perChar);
  }
}
// Scrolls the panel that holds an element so the element sits `offset` px below the panel's
// top, smoothly, the way a wheel would. (Synthetic wheel events don't reach every panel.)
async function scrollTo(selector, offset = 120, ms = 1200, text = null) {
  const target = await where(selector, text, { scroll: false });
  await moveTo(Math.min(W - 60, target.x), Math.min(H - 80, Math.max(120, target.y)), 450);
  await js(`new Promise((done) => {
    const el = [...document.querySelectorAll(${JSON.stringify(selector)})]
      .find(e => ${text ? `e.textContent.includes(${JSON.stringify(text)})` : 'true'});
    let box = el && el.parentElement;
    while (box && !(box.scrollHeight > box.clientHeight + 2 && /auto|scroll/.test(getComputedStyle(box).overflowY))) box = box.parentElement;
    if (!box) return done(false);
    const from = box.scrollTop;
    const to = Math.max(0, Math.min(box.scrollHeight - box.clientHeight,
      from + el.getBoundingClientRect().top - box.getBoundingClientRect().top - ${offset}));
    const t0 = performance.now();
    const step = (now) => {
      const k = Math.min(1, (now - t0) / ${ms});
      const e = k < 0.5 ? 4 * k * k * k : 1 - Math.pow(-2 * k + 2, 3) / 2;
      box.scrollTop = from + (to - from) * e;
      if (k < 1) requestAnimationFrame(step); else done(true);
    };
    requestAnimationFrame(step);
  })`);
}
async function waitFor(expression, ms = 20000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await js(expression)) return true;
    await sleep(200);
  }
  throw new Error(`timed out waiting for ${expression}`);
}

// ---- Recording -----------------------------------------------------------------------------------
let frames = null;
let marks = null;
listeners.set('Page.screencastFrame', (p) => {
  send('Page.screencastFrameAck', { sessionId: p.sessionId }).catch(() => {});
  if (frames) frames.push({ t: p.metadata.timestamp, data: p.data });
});
async function startRecording() {
  frames = [];
  marks = [];
  await send('Page.startScreencast', { format: 'jpeg', quality, maxWidth: W, maxHeight: H, everyNthFrame: 1 });
}
const mark = (name, rect) => {
  marks.push({ name, wall: Date.now() / 1000, ...(rect ? { rect } : {}) });
  console.log(`  mark ${name}`);
};
// Marks a moment together with where an element is on screen, so the edit can zoom onto it.
async function markAt(name, selector, text) {
  const r = await js(`(() => {
    const el = [...document.querySelectorAll(${JSON.stringify(selector)})]
      .find(e => ${text ? `e.textContent.includes(${JSON.stringify(text)})` : 'true'});
    if (!el) return null;
    const b = el.getBoundingClientRect();
    return {x: Math.round(b.left), y: Math.round(b.top), w: Math.round(b.width), h: Math.round(b.height)};
  })()`);
  mark(name, r ?? undefined);
}
async function stopRecording(name) {
  await sleep(300);
  await send('Page.stopScreencast');
  await sleep(300);
  const got = frames;
  frames = null;
  if (!got.length) throw new Error('no frames recorded');
  const dir = mkdtempSync(join(tmpdir(), `gj-frames-${name}-`));
  const t0 = got[0].t;
  let list = '';
  got.forEach((f, i) => {
    const file = join(dir, `f${String(i).padStart(6, '0')}.jpg`);
    writeFileSync(file, Buffer.from(f.data, 'base64'));
    const next = got[i + 1]?.t ?? f.t + 0.5;
    list += `file '${file.replaceAll('\\', '/')}'\nduration ${Math.max(0.001, next - f.t).toFixed(4)}\n`;
  });
  list += `file '${join(dir, `f${String(got.length - 1).padStart(6, '0')}.jpg`).replaceAll('\\', '/')}'\n`;
  writeFileSync(join(dir, 'list.txt'), list);
  const length = got.at(-1).t - t0 + 0.5;
  const fps = (got.length / length).toFixed(1);
  const mp4 = join(out, `${name}.mp4`);
  const r = spawnSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', join(dir, 'list.txt'),
    '-vf', `fps=30,scale=${W}:${H}:flags=lanczos,format=yuv420p`, '-c:v', 'libx264', '-crf', '14', '-preset', 'slow', '-movflags', '+faststart', mp4], { stdio: 'inherit' });
  if (r.status !== 0) throw new Error('ffmpeg failed');
  writeFileSync(join(out, `${name}.marks.json`), JSON.stringify({
    shot: name, length: Number(length.toFixed(2)), capturedFps: Number(fps),
    marks: marks.map((m) => ({ name: m.name, t: Number((m.wall - t0).toFixed(2)), ...(m.rect ? { rect: m.rect } : {}) })),
  }, null, 2));
  console.log(`saved ${mp4} (${length.toFixed(1)} s, ${got.length} frames, about ${fps} fps captured)`);
  try { rmSync(dir, { recursive: true, force: true }); } catch { /* ignore */ }
}

// ---- The shots -------------------------------------------------------------------------------------
async function openConsole() {
  await send('Page.navigate', { url });
  await sleep(1500);
  await js(`sessionStorage.setItem('gj.token', ${JSON.stringify(token)}); localStorage.setItem('gj.toured', '1'); location.reload(); true`);
  await sleep(3500);
  await send('Input.dispatchMouseEvent', { type: 'mouseMoved', x: mx, y: my });
}
async function resetState() {
  // No locked agents left over from an earlier take.
  await js(`fetch('/v1/lockdowns', {headers: ${auth}}).then(r => r.json()).then(list => Promise.all((list.lockdowns || list || []).map(l =>
    fetch('/v1/lockdowns/' + encodeURIComponent(l.principal), {method: 'DELETE', headers: ${auth}}))))`).catch(() => {});
}
async function runAttackOffCamera(id) {
  await js(`fetch('/demo/scenarios/${id}/run', {method: 'POST', headers: ${auth}}).then(r => r.status)`);
}

// ---- Narration ---------------------------------------------------------------------------------------
// The shots act in time with the voice-over: say(id) marks when a line starts (the edit puts the
// line's audio there), word() waits for a word in it, done() waits for its end. Line lengths come
// from the generated audio (durations.json next to lines.json), or are estimated from the text.
const voiceDir = resolve(arg('voice', 'brag-output/voice'));
const voice = (() => {
  const lines = {};
  try {
    const spec = JSON.parse(readFileSync(join(voiceDir, 'lines.json'), 'utf8'));
    let durations = {};
    try {
      durations = JSON.parse(readFileSync(join(voiceDir, 'durations.json'), 'utf8'));
    } catch {
      // not generated yet
    }
    for (const l of spec.lines) lines[l.id] = { text: l.text, dur: durations[l.id] ?? 0.4 + l.text.length / 15 };
  } catch {
    console.log(`no narration found in ${voiceDir}; using estimated timings`);
  }
  return lines;
})();
const said = {};
function say(id) {
  if (!voice[id]) throw new Error(`no narration line ${id}`);
  said[id] = Date.now();
  mark(`vo:${id}`);
}
const until = async (at) => {
  const wait = at - Date.now();
  if (wait > 0) await sleep(wait);
};
// Kokoro leaves a little silence at each end; the words in between are spread by characters.
async function word(id, w) {
  const { text, dur } = voice[id];
  const i = text.toLowerCase().search(new RegExp(`\\b${w.toLowerCase()}\\b`));
  if (i < 0) throw new Error(`"${w}" is not in line ${id}`);
  const lead = 0.12;
  const tail = 0.25;
  await until(said[id] + (lead + (i / text.length) * (dur - lead - tail)) * 1000);
}
async function done(id, gap = 0.35) {
  await until(said[id] + (voice[id].dur + gap) * 1000);
}

const SHOTS = {
  // Debugging aid: records six seconds of the live airspace to measure the capture rate.
  async fps() {
    await startRecording();
    mark('start');
    await moveTo(W * 0.3, H * 0.3, 2000);
    await moveTo(W * 0.7, H * 0.6, 2000);
    await sleep(2000);
    await stopRecording('fps');
  },

  // The headline attack cut off, the incident, and a lockdown.
  async hook() {
    await key('Escape');
    await moveTo(W * 0.55, H * 0.6, 10);
    await sleep(800);
    await startRecording();
    mark('start');
    await sleep(1200);
    say('a1');
    // Point at each column as it is named.
    await word('a1', 'people');
    await aim('.column-title', 'People', 650);
    await word('a1', 'agents');
    await aim('.column-title', 'Agents', 500);
    await word('a1', 'tools');
    await aim('.column-title', 'Tools', 500);
    await word('a1', 'models');
    await aim('.column-title', 'Models', 500);
    await done('a1', 0.3);
    say('a2');
    await aim('.attack-btn', 'Refund borrowed', 1200, -0.25);
    await sleep(300);
    await markAt('hover-attack', '.hud.demo');
    await done('a2', 0.1);
    await click('.attack-btn', 'Refund borrowed', 150, -0.25);
    mark('click-attack');
    await sleep(200);
    say('a3');
    await waitFor(`!!document.querySelector('.intercept')`, 15000);
    await markAt('blocked', '.intercept', 'refunds-need-an-operator');
    await done('a3', 0.4);
    say('n1');
    await click('.intercept', 'refunds-need-an-operator', 900);
    await waitFor(`!!document.querySelector('.panel.incident')`);
    await sleep(400);
    await markAt('incident-open', '.panel.incident');
    await done('n1', 0.3);
    say('n2');
    await scrollTo('.panel.incident .copilot', 90, 1400);
    await markAt('copilot-visible', '.panel.incident .copilot');
    await done('n2', 0.4);
    say('n3');
    await scrollTo('.panel.incident .eyebrow', 0, 700);
    await click('.panel.incident .btn.danger', 'Lock', 700);
    await waitFor(`!!document.querySelector('.modal')`);
    await markAt('lock-dialog', '.modal');
    await word('n3', 'billing');
    await click('.modal .btn.danger', 'Lock now', 500);
    await sleep(300);
    await markAt('locked', '.node-label', 'billing');
    await word('n3', 'people');
    await key('Escape');
    await moveTo(W * 0.52, H * 0.74, 1200);
    mark('locked-airspace');
    await done('n3', 0.3);
    say('n4');
    await done('n4', 2.2);
    mark('end');
    await stopRecording('hook');
    await resetState();
  },

  // Remove a rule, watch an attack get through, and let the co-pilot close the gap.
  async copilot() {
    const before = await api('GET', '/v1/policy');
    if (before.status !== 200) throw new Error(`policy: ${before.status}`);
    try {
      await key('Escape');
      await sleep(500);
      await startRecording();
      mark('start');
      await sleep(1000);
      say('c1');
      await key('3');
      mark('policy-view');
      await word('c1', 'remove');
      await click('.rule-card', 'external-chains-email-only-via-support', 900);
      mark('rule-picked');
      await word('c1', 'email');
      // Remove the rule from the editor text, as an edit would.
      await js(`(() => {
        const el = document.querySelector('.policy-view textarea');
        const p = JSON.parse(el.value);
        p.rules = p.rules.filter(r => r.id !== 'external-chains-email-only-via-support');
        const set = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
        set.call(el, JSON.stringify(p, null, 2));
        el.dispatchEvent(new Event('input', {bubbles: true}));
        return true;
      })()`);
      await markAt('rule-removed', '.policy-view .editor');
      await done('c1', 0.2);
      say('c2');
      await waitFor(`!!document.querySelector('.simulation .safe')`, 20000);
      await markAt('replay-checked', '.simulation');
      await aim('.policy-view .btn.primary', 'Save', 900);
      await word('c2', 'so');
      await click('.policy-view .btn.primary', 'Save', 150);
      await sleep(500);
      if (await js(`!!document.querySelector('.modal')`)) await click('.modal .btn', 'Save', 400);
      mark('saved');
      await done('c2', 0.4);
      say('c3');
      await key('1');
      mark('airspace');
      await aim('.attack-btn', 'Forum post', 1000, -0.25);
      await word('c3', 'customer');
      await click('.attack-btn', 'Forum post', 150, -0.25);
      mark('click-attack');
      await waitFor(`[...document.querySelectorAll('.toast')].some(t => t.textContent.includes('Harm'))`, 20000);
      await markAt('harm', '.toast', 'Harm');
      await done('c3', 0.6);
      say('c4');
      await key('2');
      mark('table');
      await word('c4', 'table');
      await click('input[type=search]', null, 600);
      await typeText('email attack', 45);
      await markAt('filtered', '.table-view');
      await word('c4', 'open');
      await click('tbody tr', null, 600);
      await waitFor(`!!document.querySelector('.panel.incident')`);
      await markAt('incident-open', '.panel.incident');
      await word('c4', 'ask');
      await click('.suggest-bar .btn.primary', null, 700);
      mark('suggest');
      await done('c4', 0.2);
      say('c5');
      await waitFor(`document.querySelectorAll('.suggestion').length > 0`, 30000);
      mark('suggestions');
      await word('c5', 'every');
      await scrollTo('.suggestions', 70, 1400);
      await markAt('proven-cards', '.panel.incident');
      await done('c5', 0.3);
      say('c6');
      await done('c6', 0.4);
      say('c7');
      await scrollTo('.suggestion.rejected', 70, 1500);
      await markAt('rejected-cards', '.panel.incident');
      await done('c7', 0.6);
      say('c8');
      await scrollTo('.suggestions', 70, 900);
      await click('.suggestion.proven .btn.primary', 'Apply rule', 700);
      await waitFor(`!!document.querySelector('.modal')`);
      await markAt('apply-dialog', '.modal');
      await word('c8', 'server');
      await click('.modal .btn.primary', 'Apply', 500);
      await waitFor(`[...document.querySelectorAll('.suggestion .btn')].some(b => b.textContent.includes('Applied'))`, 20000);
      mark('applied');
      await done('c8', 0.5);
      await key('Escape');
      await key('1');
      mark('airspace-again');
      await aim('.attack-btn', 'Forum post', 900, -0.25);
      await sleep(300);
      say('c9');
      await click('.attack-btn', 'Forum post', 150, -0.25);
      mark('click-attack-again');
      await waitFor(`[...document.querySelectorAll('.toast')].some(t => /Stopped|Harm/.test(t.textContent))`, 20000);
      if (!(await js(`[...document.querySelectorAll('.toast')].some(t => t.textContent.includes('Stopped'))`))) {
        throw new Error('the attack was not stopped after the rule was applied');
      }
      await markAt('stopped', '.toast', 'Stopped');
      await markAt('stopped-marker', '.intercept');
      await done('c9', 2.6);
      mark('end');
      await stopRecording('copilot');
    } finally {
      // Put the original policy back.
      const now = await api('GET', '/v1/policy');
      const r = await api('PUT', '/v1/policy', JSON.parse(before.body), `{'If-Match': ${JSON.stringify(now.etag)}}`);
      console.log(`policy restored: ${r.status}`);
    }
  },

  // The rest of the console: replay, an agent's panel, the table and the policy editor.
  async tour() {
    await key('Escape');
    await sleep(400);
    await startRecording();
    mark('start');
    await sleep(900);
    say('t1');
    await aim('.timeline button', 'Replay last minute', 1000);
    await word('t1', 'replay');
    await click('.timeline button', 'Replay last minute', 150);
    mark('replay');
    await done('t1', 0.1);
    say('t2');
    await click('.timeline button', 'Live', 500);
    await click('.node-label', 'billing', 700);
    await sleep(300);
    await markAt('node-panel', '.panel');
    await word('t2', 'browse');
    await key('Escape');
    await key('2');
    mark('table');
    await scrollTo('tbody tr:nth-child(30)', 80, 1500);
    await word('t2', 'test');
    await key('3');
    mark('policy');
    await done('t2', 1.4);
    await key('1');
    mark('end');
    await sleep(600);
    await stopRecording('tour');
  },
};

try {
  mkdirSync(out, { recursive: true });
  await send('Page.enable');
  await send('Runtime.enable');
  await send('Page.setBypassCSP', { enabled: true });
  await send('Emulation.setDeviceMetricsOverride', { width: W, height: H, deviceScaleFactor: 1, mobile: false });
  await send('Page.addScriptToEvaluateOnNewDocument', { source: pointerScript });
  await openConsole();
  await resetState();
  await api('PUT', '/demo/traffic', { on: true });
  // Let normal work fill the airspace and the timeline.
  console.log('warming up with normal work...');
  await sleep(Number(arg('warm', '20')) * 1000);
  for (const name of shots) {
    if (!SHOTS[name]) throw new Error(`unknown shot ${name}`);
    console.log(`recording ${name}`);
    try {
      await SHOTS[name]();
    } catch (err) {
      // Keep a picture of what the page showed, to see what went wrong.
      const { data } = await send('Page.captureScreenshot', { format: 'png' });
      writeFileSync(join(out, `${name}.error.png`), Buffer.from(data, 'base64'));
      console.log(`page text: ${(await js('document.body.innerText')).slice(0, 1500)}`);
      throw err;
    }
    await sleep(1500);
  }
} finally {
  await api('PUT', '/demo/traffic', { on: false }).catch(() => {});
  ws.close();
  browser.kill();
  await sleep(500);
  try {
    rmSync(profile, { recursive: true, force: true });
  } catch {
    // the browser may still hold files on Windows
  }
}
