// Takes the README screenshots from a running demo, with a headless Chromium browser (Chrome or
// Edge) driven over the DevTools protocol. No packages needed: Node 22+ has WebSocket built in.
//
//   bash deploy/demo/run-console.sh            # start the demo first
//   node console/scripts/screenshots.mjs [--browser path] [--url http://localhost:18788] [--out docs/images]
//
// The token is the demo's (GJ_ADMIN_TOKEN, or the default the demo script prints).
import { spawn } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

const arg = (name, fallback) => {
  const i = process.argv.indexOf(`--${name}`);
  return i > 0 ? process.argv[i + 1] : fallback;
};
const url = arg('url', 'http://localhost:18788');
const out = arg('out', 'docs/images');
const token = process.env.GJ_ADMIN_TOKEN ?? 'demo-admin-token-0123456789abcdef';
const candidates = [
  arg('browser', process.env.BROWSER),
  'C:/Program Files/Google/Chrome/Application/chrome.exe',
  'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',
  '/usr/bin/google-chrome',
  '/usr/bin/chromium',
  '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome',
].filter(Boolean);
const browserPath = candidates.find((p) => existsSync(p));
if (!browserPath) throw new Error('No Chrome or Edge found; pass --browser <path>');

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const port = 9333;
const profile = mkdtempSync(join(tmpdir(), 'gj-shots-'));
const browser = spawn(browserPath, [
  '--headless=new',
  `--remote-debugging-port=${port}`,
  `--user-data-dir=${profile}`,
  '--window-size=1440,900',
  '--force-device-scale-factor=1',
  '--hide-scrollbars',
  '--use-angle=swiftshader',
  '--enable-unsafe-swiftshader',
  '--ignore-gpu-blocklist',
  'about:blank',
]);

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
ws.addEventListener('message', (m) => {
  const msg = JSON.parse(m.data);
  if (msg.id && pending.has(msg.id)) {
    pending.get(msg.id)(msg);
    pending.delete(msg.id);
  }
});
const send = (method, params = {}) =>
  new Promise((resolve, reject) => {
    const n = ++id;
    pending.set(n, (msg) => (msg.error ? reject(new Error(`${method}: ${msg.error.message}`)) : resolve(msg.result)));
    ws.send(JSON.stringify({ id: n, method, params }));
  });
const js = async (expression) => (await send('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true })).result?.value;
const api = (method, path, body) =>
  js(`fetch(${JSON.stringify(path)}, {method: ${JSON.stringify(method)}, headers: {Authorization: 'Bearer ${token}', 'Content-Type': 'application/json'}${
    body ? `, body: ${JSON.stringify(JSON.stringify(body))}` : ''
  }}).then(r => r.status)`);
mkdirSync(out, { recursive: true });
const shot = async (name) => {
  const { data } = await send('Page.captureScreenshot', { format: 'png' });
  writeFileSync(join(out, `${name}.png`), Buffer.from(data, 'base64'));
  console.log(`saved ${join(out, `${name}.png`)}`);
};

try {
  await send('Page.enable');
  await send('Runtime.enable');
  await send('Emulation.setDeviceMetricsOverride', { width: 1440, height: 900, deviceScaleFactor: 1, mobile: false });
  await send('Page.navigate', { url });
  await sleep(1500);
  // Sign in as the console would, and skip the first-run tour.
  await js(`sessionStorage.setItem('gj.token', ${JSON.stringify(token)}); localStorage.setItem('gj.toured', '1'); location.reload(); true`);
  await sleep(4000);
  await api('PUT', '/demo/traffic', { on: true });
  await sleep(9000);
  // Launch the headline attack and catch the moment it is cut off.
  js(`fetch('/demo/scenarios/borrowed-refund/run', {method: 'POST', headers: {Authorization: 'Bearer ${token}'}})`);
  await sleep(2600);
  await shot('airspace');
  await js(`document.querySelector('.intercept')?.click(); true`);
  await sleep(3500);
  await shot('incident');
  await js(`window.dispatchEvent(new KeyboardEvent('keydown', {key: 'Escape'})); window.dispatchEvent(new KeyboardEvent('keydown', {key: '2'})); true`);
  await sleep(1500);
  await shot('table');
  await js(`window.dispatchEvent(new KeyboardEvent('keydown', {key: '3'})); true`);
  await sleep(2000);
  await shot('policy');
  await api('PUT', '/demo/traffic', { on: false });
} finally {
  ws.close();
  browser.kill();
  await sleep(500);
  try {
    rmSync(profile, { recursive: true, force: true });
  } catch {
    // the browser may still hold files on Windows
  }
}
