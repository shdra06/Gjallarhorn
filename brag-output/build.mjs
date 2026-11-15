// Builds the Gjallarhorn video's HyperFrames composition: a cinematic intro that hands over to
// the narrated demo. The voice is the clock: every scene is timed from the narration's real
// lengths (voice/durations.json) and the footage's marks (composition/assets/footage/*.marks.json,
// written by console/scripts/record.mjs), so re-recording or re-voicing only needs a rebuild.
//
//   node build.mjs            # writes composition/index.html and composition/compositions/*.html
import { execFileSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const comp = join(here, 'composition');
const readJson = (p) => JSON.parse(readFileSync(p, 'utf8'));
const { lines } = readJson(join(here, 'voice', 'lines.json'));
const durations = readJson(join(here, 'voice', 'durations.json'));
const text = Object.fromEntries(lines.map((l) => [l.id, l.text]));
const dur = (id) => {
  if (durations[id] == null) throw new Error(`no audio for ${id}; run voice/tts.mjs`);
  return durations[id];
};
const footage = (name) => readJson(join(comp, 'assets', 'footage', `${name}.marks.json`));
const r2 = (n) => Math.round(n * 100) / 100;
const esc = (s) => s.replaceAll('&', '&amp;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');

// Where a word falls inside a narration line (same estimate the recorder uses).
function wordAt(id, w) {
  const t = text[id];
  const i = t.toLowerCase().search(new RegExp(`\\b${w.toLowerCase()}\\b`));
  if (i < 0) throw new Error(`"${w}" is not in ${id}`);
  const lead = 0.12;
  const tail = 0.25;
  return lead + (i / t.length) * (dur(id) - lead - tail);
}

// Everything placed on the root: narration, sound effects, music.
const vo = []; // { id, t }
const sfx = []; // { file, t, vol }
const scenes = []; // { id, start, duration, html }
const W = 1920;
const H = 1080;

// The music's beat grid (bundled cue preset for vol-12, 110 BPM).
const cues = readJson(join(here, 'composition', 'assets', 'music', 'bed.music-cues.json'));
const strong = cues.strongCues.map((c) => c.time).sort((a, b) => a - b);

// ---- Shared look ----------------------------------------------------------------------------------
const PALETTE = `
      --ground: #060a13; --ground-2: #0a1120; --panel: #0c1424; --line: #1c2a40; --line-2: #26384f;
      --ink: #e6edf7; --muted: #93a5bc; --cyan: #38e1ff; --red: #ff4d6a; --amber: #ffb020; --green: #3ddc97;
      --display: 'Oswald', sans-serif; --body: 'Roboto', sans-serif; --mono: 'IBM Plex Mono', monospace;`;
const LOGO = (size, id) => `<svg id="${id}" width="${size}" height="${size}" viewBox="0 0 64 64" aria-hidden="true">
        <rect width="64" height="64" rx="14" fill="#0b1424" stroke="#26384f" stroke-width="1.5" />
        <path d="M14 40c8-16 28-22 38-20-6 3-9 8-10 14l-6-2c-1 5-4 9-8 12l-2-6c-4 1-8 2-12 2z" fill="#38e1ff" />
        <circle cx="46" cy="20" r="4" fill="#ff3b5c" />
      </svg>`;

function sceneFile(id, duration, css, body, script, { fadeOut = true } = {}) {
  return `<template>
  <style>
    #root { position: absolute; inset: 0; overflow: hidden; background: var(--ground); color: var(--ink); font-family: var(--body); ${PALETTE} }
    #root * { box-sizing: border-box; }
    #${id}-fade { position: absolute; inset: 0; background: #060a13; z-index: 50; pointer-events: none; }
${css}
  </style>
  <div id="root" data-composition-id="${id}" data-width="${W}" data-height="${H}">
${body}
    <div id="${id}-fade" data-layout-ignore></div>
  </div>
  <script>
    const tl = gsap.timeline({ paused: true });
    // Dip from and to the ground at the scene's edges.
    tl.set("#${id}-fade", { opacity: 1 }, 0);
    tl.to("#${id}-fade", { opacity: 0, duration: 0.35, ease: "power2.out" }, 0);
${fadeOut ? `    tl.fromTo("#${id}-fade", { opacity: 0 }, { opacity: 1, duration: 0.3, ease: "power2.in", immediateRender: false }, ${r2(duration - 0.32)});` : '    // The film ends on the end card.'}
${script}
    window.__timelines["${id}"] = tl;
  </script>
</template>
`;
}

// A chapter tag that lands top-centre for a few seconds.
const chapterCss = (id) => `
    #${id}-chapter { position: absolute; top: 74px; left: 0; width: 100%; display: flex; justify-content: center; z-index: 30; }
    #${id}-chapter span { font-family: var(--display); font-weight: 600; font-size: 30px; letter-spacing: 0.22em; text-transform: uppercase;
      color: var(--ink); background: rgba(6, 10, 19, 0.86); border: 1.5px solid var(--line-2); border-radius: 999px; padding: 10px 30px; }
    #${id}-chapter b { color: var(--cyan); font-weight: 600; margin-right: 18px; }`;
const chapterHtml = (id, n, label) =>
  `    <div id="${id}-chapter" class="clip" data-start="0.25" data-duration="3.4"><span><b>${n}</b>${esc(label)}</span></div>`;
const chapterJs = (id) =>
  `    tl.fromTo("#${id}-chapter span", { opacity: 0, filter: "blur(8px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.25, ease: "power3.out" }, 0.3);
    tl.to("#${id}-chapter span", { opacity: 0, filter: "blur(6px)", duration: 0.2, ease: "power2.in" }, 3.4);`;

// ---- Footage scenes ---------------------------------------------------------------------------------
// Plays one recording, puts each narration line where the recorder said it, adds a click sound
// for every click, and punches in on the part of the screen that matters.
function zoomTo(rect, maxScale = 1.8, fill = 0.82) {
  if (!rect) return { s: 1, x: 0, y: 0 };
  const s = Math.max(1, Math.min(maxScale, (fill * W) / rect.w, (fill * H) / rect.h));
  const cx = rect.x + rect.w / 2;
  const cy = rect.y + rect.h / 2;
  const x = Math.min(0, Math.max(W * (1 - s), W / 2 - s * cx));
  const y = Math.min(0, Math.max(H * (1 - s), H / 2 - s * cy));
  return { s: r2(s), x: Math.round(x), y: Math.round(y) };
}
function footageScene({ id, clip, chapter, lead = 0.9, tailAfter = 'end', moves, extraSfx = [] }) {
  const f = footage(clip);
  const m = (name) => {
    const k = f.marks.find((x) => x.name === name);
    if (!k) throw new Error(`${clip}: no mark ${name}`);
    return k;
  };
  const first = f.marks.find((x) => x.name.startsWith('vo:'));
  const from = Math.max(0, (first ? first.t : 1) - lead);
  const to = m(tailAfter).t + 0.2;
  const duration = r2(to - from);
  const local = (name) => r2(m(name).t - from);
  for (const k of f.marks) {
    if (k.name.startsWith('vo:')) vo.push({ id: k.name.slice(3), t: k.t - from, scene: id });
    if (k.name === 'click' && k.t > from && k.t < to) sfx.push({ file: 'click1.ogg', t: k.t - from + 0.03, vol: 0.32, scene: id });
  }
  for (const e of extraSfx) sfx.push({ ...e, t: local(e.at) + (e.offset ?? 0), scene: id });
  // Camera moves: each one eases from the previous framing to the next.
  let state = { s: 1, x: 0, y: 0 };
  let free = 0.05; // the next move starts after the last one ends
  const js = [`    tl.set("#${id}-cam", { scale: 1, x: 0, y: 0 }, 0);`];
  for (const mv of moves({ m, local })) {
    const next = mv.rect ? zoomTo(mv.rect, mv.max, mv.fill) : { s: 1, x: 0, y: 0 };
    const t = r2(Math.max(free, mv.t));
    const d = mv.d ?? 0.8;
    js.push(`    tl.fromTo("#${id}-cam", { scale: ${state.s}, x: ${state.x}, y: ${state.y} }, { scale: ${next.s}, x: ${next.x}, y: ${next.y}, duration: ${d}, ease: "power2.inOut", immediateRender: false }, ${t});`);
    state = next;
    free = r2(t + d + 0.02);
  }
  const css = `
    #${id}-cam { position: absolute; left: 0; top: 0; width: ${W}px; height: ${H}px; transform-origin: 0 0; }
    #${id}-v { position: absolute; left: 0; top: 0; width: ${W}px; height: ${H}px; object-fit: cover; }${chapter ? chapterCss(id) : ''}`;
  const body = `    <div id="${id}-cam">
      <video id="${id}-v" class="clip" data-start="0" data-duration="${duration}" data-media-start="${r2(from)}" data-track-index="0" muted playsinline src="assets/footage/${clip}.mp4"></video>
    </div>
${chapter ? chapterHtml(id, chapter[0], chapter[1]) : ''}`;
  const script = `${js.join('\n')}
${chapter ? chapterJs(id) : ''}`;
  return { id, duration, html: sceneFile(id, duration, css, body, script) };
}

// ---- Intro (music forward, about 22 s) ------------------------------------------------------------
function intro() {
  const id = 'intro';
  const hook = footage('hook');
  const cop = footage('copilot');
  const hm = (n) => hook.marks.find((x) => x.name === n);
  const cm = (n) => cop.marks.find((x) => x.name === n);
  const t = {};
  t.i1 = 0.5;
  t.i2 = t.i1 + dur('i1') + 0.25;
  const i2end = t.i2 + dur('i2');
  // The name lands on the first strong beat after "caught it mid-air".
  const earliest = i2end + 0.3 + dur('i3') + 0.2;
  t.name = strong.find((c) => c >= earliest) ?? cues.beats.map((b) => b.time).find((c) => c >= earliest);
  t.i3 = r2(t.name - 0.2 - dur('i3'));
  t.cut = r2(Math.max(i2end + 0.15, t.i3 - 0.35)); // the airspace footage comes in
  t.i4 = r2(t.name + 2.9);
  t.i5 = r2(t.i4 + dur('i4') + 0.35);
  t.i6 = r2(t.i5 + dur('i5') + 0.35);
  t.i7 = r2(t.i6 + dur('i6') + 0.5);
  const end = r2(t.i7 + dur('i7') + 0.55);
  for (const k of ['i1', 'i2', 'i3', 'i4', 'i5', 'i6', 'i7']) vo.push({ id: k, t: t[k], scene: id });

  // Footage windows: the cut-off lands just before the name; the lockdown; the co-pilot's cards.
  const blocked = hm('blocked');
  const cutLen = r2(t.name - t.cut);
  const cutFrom = r2(blocked.t - (cutLen - 0.55)); // the flight stops 0.55 s before the name
  const lockFrom = r2(hm('locked').t + 0.2);
  const copFrom = r2((cm('rejected-cards') ?? cm('proven-cards')).t - 1.6);
  const h1 = t.i4 - 0.15;
  const h1d = r2(t.i5 - 0.15 - h1);
  const h2 = t.i5 - 0.15;
  const h2d = r2(t.i6 - 0.15 - h2);
  const z1 = zoomTo(blocked.rect ? { x: blocked.rect.x - 260, y: blocked.rect.y - 170, w: blocked.rect.w + 520, h: blocked.rect.h + 340 } : null, 2.2, 0.9);
  const lockRect = hm('locked').rect;
  const z2 = zoomTo(lockRect ? { x: lockRect.x - 330, y: lockRect.y - 140, w: lockRect.w + 660, h: lockRect.h + 420 } : null, 2, 0.95);
  const copRect = (cm('rejected-cards') ?? cm('proven-cards')).rect;
  const z3 = zoomTo(copRect ? { x: copRect.x, y: copRect.y + 60, w: copRect.w, h: 560 } : null, 1.9, 0.95);

  sfx.push({ file: 'impactSoft_medium_001.ogg', t: r2(t.cut + cutLen - 0.55), vol: 0.6, scene: id });
  sfx.push({ file: 'impactBell_heavy_000.ogg', t: r2(t.name - 0.02), vol: 0.62, scene: id });
  sfx.push({ file: 'drop_001.ogg', t: r2(h1 + 0.35), vol: 0.5, scene: id });
  sfx.push({ file: 'drop_002.ogg', t: r2(h2 + 0.35), vol: 0.5, scene: id });
  sfx.push({ file: 'impactSoft_medium_003.ogg', t: r2(t.i6 + wordAt('i6', 'zero') - 0.05), vol: 0.6, scene: id });
  const pills = ['customer-chat', 'support', 'billing', 'billing.refund'];
  const pillT = pills.map((_, i) => r2(0.7 + i * 0.42));
  const keys = ['keypress-003.wav', 'keypress-011.wav', 'keypress-019.wav', 'keypress-027.wav'];
  pillT.forEach((pt, i) => sfx.push({ file: keys[i], t: pt, vol: 0.45, scene: id }));
  const amountT = r2(t.i2 + wordAt('i2', 'fifty') - 0.1);
  sfx.push({ file: 'impactSoft_medium_003.ogg', t: amountT, vol: 0.45, scene: id });

  const css = `
    #intro-glow { position: absolute; inset: 0; background: radial-gradient(60% 55% at 50% 48%, rgba(56, 225, 255, 0.16), rgba(6, 10, 19, 0) 70%); }
    #intro-hook { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 56px; }
    #intro-chain { display: flex; align-items: center; gap: 22px; font-family: var(--mono); font-size: 46px; }
    .intro-pill { padding: 14px 30px; border: 2px solid var(--line-2); border-radius: 999px; background: var(--panel); color: var(--ink); }
    .intro-pill.target { border-color: var(--red); color: #ffd3da; }
    .intro-sep { color: var(--muted); font-size: 40px; }
    #intro-amount { display: flex; flex-direction: column; align-items: center; gap: 8px; }
    #intro-amount b { font-family: var(--display); font-weight: 700; font-size: 168px; line-height: 1; color: var(--red); letter-spacing: 0.02em; }
    #intro-amount span { font-size: 34px; color: var(--muted); }
    .intro-cam { position: absolute; left: 0; top: 0; width: ${W}px; height: ${H}px; transform-origin: 0 0; }
    .intro-cam video { position: absolute; left: 0; top: 0; width: ${W}px; height: ${H}px; object-fit: cover; }
    #intro-name { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 30px; background: var(--ground); }
    #intro-name-row { display: flex; align-items: center; gap: 44px; }
    #intro-word { font-family: var(--display); font-weight: 700; font-size: 188px; letter-spacing: 0.1em; line-height: 1; color: var(--ink); }
    #intro-tag { width: 1320px; text-align: center; font-size: 44px; line-height: 1.4; color: var(--muted); }
    #intro-tag em { font-style: normal; color: var(--cyan); }
    .intro-label { position: absolute; left: 110px; top: 120px; width: 900px; padding: 34px 44px; border-radius: 22px; background: rgba(6, 10, 19, 0.88); border: 1.5px solid var(--line-2); }
    .intro-label b { display: block; font-family: var(--display); font-weight: 700; font-size: 132px; line-height: 1; }
    .intro-label span { display: block; margin-top: 14px; font-size: 36px; line-height: 1.35; color: var(--ink); }
    #intro-l1 b { color: var(--red); }
    #intro-l2 { width: 600px; }
    #intro-l2 b { color: var(--green); font-size: 92px; }
    #intro-count { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 46px; background: var(--ground); }
    #intro-count h2 { font-family: var(--display); font-weight: 600; font-size: 54px; letter-spacing: 0.16em; text-transform: uppercase; color: var(--muted); margin: 0; }
    #intro-nums { display: flex; gap: 220px; }
    .intro-num { display: flex; flex-direction: column; align-items: center; gap: 10px; }
    .intro-num b { font-family: var(--display); font-weight: 700; font-size: 260px; line-height: 1; }
    .intro-num span { font-size: 38px; color: var(--ink); }
    #intro-n12 b { color: var(--red); }
    #intro-n0 b { color: var(--cyan); }
    #intro-count p { margin: 0; font-size: 36px; color: var(--muted); }`;
  const body = `    <div id="intro-glow"></div>
    <div id="intro-hook" class="clip" data-start="0" data-duration="${r2(t.cut + 0.05)}">
      <div id="intro-chain">
${pills.map((p, i) => `        ${i ? `<span id="intro-sep${i}" class="intro-sep">${i === 3 ? '→' : '›'}</span>` : ''}<span id="intro-p${i}" class="intro-pill${i === 3 ? ' target' : ''}">${p}</span>`).join('\n')}
      </div>
      <div id="intro-amount"><b>₹50,000</b><span>a refund, to the attacker</span></div>
    </div>
    <div id="intro-cam1" class="intro-cam">
      <video id="intro-v1" class="clip" data-start="${t.cut}" data-duration="${r2(cutLen + 0.05)}" data-media-start="${cutFrom}" data-track-index="0" muted playsinline src="assets/footage/hook.mp4"></video>
    </div>
    <div id="intro-name" class="clip" data-start="${r2(t.name)}" data-duration="${r2(h1 - t.name + 0.05)}">
      <div id="intro-name-row">${LOGO(150, 'intro-logo')}
        <div id="intro-word">GJALLARHORN</div>
      </div>
      <p id="intro-tag">Built into the <em>Bifrost</em> AI gateway</p>
    </div>
    <div id="intro-cam2" class="intro-cam">
      <video id="intro-v2" class="clip" data-start="${r2(h1)}" data-duration="${h1d}" data-media-start="${lockFrom}" data-track-index="0" muted playsinline src="assets/footage/hook.mp4"></video>
    </div>
    <div id="intro-l1w" class="clip" data-start="${r2(h1 + 0.12)}" data-duration="${r2(h1d - 0.12)}"><div id="intro-l1" class="intro-label"><b>1.8 ms</b><span>median time to refuse a locked agent</span></div></div>
    <div id="intro-cam3" class="intro-cam">
      <video id="intro-v3" class="clip" data-start="${r2(h2)}" data-duration="${h2d}" data-media-start="${copFrom}" data-track-index="0" muted playsinline src="assets/footage/copilot.mp4"></video>
    </div>
    <div id="intro-l2w" class="clip" data-start="${r2(h2 + 0.12)}" data-duration="${r2(h2d - 0.12)}"><div id="intro-l2" class="intro-label"><b>Proven first</b><span>every rule is replayed against real traffic</span></div></div>
    <div id="intro-count" class="clip" data-start="${r2(t.i6 - 0.15)}" data-duration="${r2(end - t.i6 + 0.15)}">
      <h2>31 attacks through a real Bifrost</h2>
      <div id="intro-nums">
        <div id="intro-n12" class="intro-num"><b>12</b><span>caused real harm without Gjallarhorn</span></div>
        <div id="intro-n0" class="intro-num"><b>0</b><span>with Gjallarhorn</span></div>
      </div>
      <p>results/attacklab-20261115T055939Z.json</p>
    </div>`;
  const script = `    // The chain types in, pill by pill, then the amount.
${pillT.map((pt, i) => `    tl.fromTo("#intro-p${i}", { opacity: 0, filter: "blur(6px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.16, ease: "power3.out" }, ${pt});${i ? `\n    tl.fromTo("#intro-sep${i}", { opacity: 0 }, { opacity: 1, duration: 0.12 }, ${r2(pt - 0.08)});` : ''}`).join('\n')}
    tl.fromTo("#intro-amount b", { opacity: 0, scale: 1.08, filter: "blur(10px)" }, { opacity: 1, scale: 1, filter: "blur(0px)", duration: 0.22, ease: "power4.out" }, ${amountT});
    tl.fromTo("#intro-amount span", { opacity: 0 }, { opacity: 1, duration: 0.3, ease: "power2.out" }, ${r2(amountT + 0.25)});
    tl.to("#intro-chain, #intro-amount", { opacity: 0, filter: "blur(10px)", duration: 0.2, ease: "power2.in" }, ${r2(t.cut - 0.18)});
    // The cut-off, pushed in on the marker.
    tl.fromTo("#intro-cam1", { scale: 1, x: 0, y: 0 }, { scale: ${z1.s}, x: ${z1.x}, y: ${z1.y}, duration: ${r2(Math.max(0.6, cutLen - 0.2))}, ease: "power2.inOut" }, ${r2(t.cut)});
    // beat-locked: ${r2(t.name)}s — the name lands on the strong cue.
    tl.fromTo("#intro-logo", { opacity: 0, scale: 0.7, rotation: -12 }, { opacity: 1, scale: 1, rotation: 0, duration: 0.45, ease: "back.out(1.6)" }, ${r2(t.name)});
    tl.fromTo("#intro-word", { opacity: 0, scale: 1.12, filter: "blur(12px)" }, { opacity: 1, scale: 1, filter: "blur(0px)", duration: 0.5, ease: "expo.out" }, ${r2(t.name + 0.05)});
    tl.fromTo("#intro-tag", { opacity: 0, filter: "blur(6px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.3, ease: "power3.out" }, ${r2(t.name + 0.55)});
    // Highlights: the lockdown, then the co-pilot's proven and rejected rules.
    tl.fromTo("#intro-cam2", { scale: ${z2.s}, x: ${z2.x}, y: ${z2.y} }, { scale: ${r2(z2.s * 1.04)}, x: ${Math.round(z2.x - 0.02 * W)}, y: ${Math.round(z2.y - 0.02 * H)}, duration: ${h1d}, ease: "none" }, ${r2(h1)});
    tl.fromTo("#intro-l1", { opacity: 0, x: -30, filter: "blur(8px)" }, { opacity: 1, x: 0, filter: "blur(0px)", duration: 0.3, ease: "expo.out" }, ${r2(h1 + 0.12)});
    tl.fromTo("#intro-cam3", { scale: ${z3.s}, x: ${z3.x}, y: ${z3.y} }, { scale: ${z3.s}, x: ${z3.x}, y: ${Math.round(Math.max(H * (1 - z3.s), z3.y - 160))}, duration: ${h2d}, ease: "power1.inOut" }, ${r2(h2)});
    tl.fromTo("#intro-l2", { opacity: 0, x: -30, filter: "blur(8px)" }, { opacity: 1, x: 0, filter: "blur(0px)", duration: 0.3, ease: "expo.out" }, ${r2(h2 + 0.12)});
    // 12, then 0.
    tl.fromTo("#intro-count h2", { opacity: 0, filter: "blur(6px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.25, ease: "power3.out" }, ${r2(t.i6 - 0.1)});
    tl.fromTo("#intro-n12", { opacity: 0, y: 30 }, { opacity: 1, y: 0, duration: 0.35, ease: "expo.out" }, ${r2(t.i6 + 0.2)});
    tl.fromTo("#intro-n0", { opacity: 0, scale: 1.3, filter: "blur(14px)" }, { opacity: 1, scale: 1, filter: "blur(0px)", duration: 0.3, ease: "power4.out" }, ${r2(t.i6 + wordAt('i6', 'zero') - 0.05)});
    tl.fromTo("#intro-count p", { opacity: 0 }, { opacity: 1, duration: 0.3 }, ${r2(t.i6 + wordAt('i6', 'zero') + 0.4)});`;
  return { id, duration: end, html: sceneFile(id, end, css, body, script), marks: t };
}

// ---- Card scenes ----------------------------------------------------------------------------------
// Lines play one after another; a scene's visuals key off when each line (or a word in it) starts.
function sequence(ids, start = 0.6, gap = 0.4) {
  const at = {};
  let t = start;
  for (const id of ids) {
    at[id] = r2(t);
    t += dur(id) + gap;
  }
  return { at, end: r2(t - gap) };
}

function problem() {
  const id = 'problem';
  const { at, end } = sequence(['p1', 'p2', 'p3', 'p4'], 0.7, 0.45);
  const duration = r2(end + 0.9);
  for (const k of Object.keys(at)) vo.push({ id: k, t: at[k], scene: id });
  const q = [
    ['Which agent did this?', 'With forwarded keys, an agent’s calls are logged as the human’s.', wordAt('p2', 'which')],
    ['Who asked whom?', 'No record of the chain from the person to the last agent.', wordAt('p2', 'who')],
    ['Was a permission borrowed?', 'An agent that may not refund asks one that can.', wordAt('p2', 'whether')],
  ];
  q.forEach((x, i) => sfx.push({ file: 'drop_001.ogg', t: r2(at.p2 + x[2]), vol: 0.4, scene: id }));
  const css = `${chapterCss(id)}
    #problem-head { position: absolute; top: 190px; left: 0; width: 100%; text-align: center; font-family: var(--display); font-weight: 600; font-size: 76px; letter-spacing: 0.04em; }
    #problem-head em { font-style: normal; color: var(--cyan); }
    #problem-cards { position: absolute; top: 335px; left: 150px; width: 1620px; display: flex; gap: 40px; }
    .problem-card { flex: 1; padding: 44px 42px; border-radius: 22px; background: var(--panel); border: 2px solid var(--line-2); min-height: 330px; }
    .problem-card b { display: block; font-family: var(--display); font-weight: 600; font-size: 50px; line-height: 1.15; color: var(--ink); }
    .problem-card span { display: block; margin-top: 22px; font-size: 32px; line-height: 1.4; color: var(--muted); }
    .problem-card i { display: block; margin-top: 26px; font-style: normal; font-family: var(--mono); font-size: 28px; color: var(--green); }
    #problem-c2 { border-color: var(--line-2); }
    #problem-tag { position: absolute; left: 0; width: 100%; text-align: center; top: 812px; font-family: var(--mono); font-size: 30px; color: var(--amber); }
    #problem-answer { position: absolute; left: 0; top: 806px; width: 100%; display: flex; justify-content: center; align-items: center; gap: 26px; }
    #problem-answer span { font-size: 44px; color: var(--ink); }
    #problem-answer b { font-family: var(--display); font-weight: 700; letter-spacing: 0.08em; color: var(--cyan); }`;
  const body = `${chapterHtml(id, '01', 'The problem')}
    <h1 id="problem-head">Many agents. <em>One gateway.</em></h1>
    <div id="problem-cards">
${q.map((x, i) => `      <div id="problem-c${i}" class="problem-card"><b>${esc(x[0])}</b><span>${esc(x[1])}</span><i id="problem-ok${i}">✓ answered on every call</i></div>`).join('\n')}
    </div>
    <div id="problem-tag">OWASP Agentic Top 10 · ASI03 · the confused deputy</div>
    <div id="problem-answer">${LOGO(84, 'problem-logo')}<span><b>GJALLARHORN</b> · a plugin for the Bifrost AI gateway</span></div>`;
  const p4 = at.p4;
  const script = `${chapterJs(id)}
    tl.fromTo("#problem-head", { opacity: 0, filter: "blur(8px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.2, ease: "power3.out" }, ${r2(at.p1 + 0.1)});
${q.map((x, i) => `    tl.fromTo("#problem-c${i}", { opacity: 0, y: 40 }, { opacity: 1, y: 0, duration: 0.45, ease: "expo.out" }, ${r2(at.p2 + x[2])});`).join('\n')}
    tl.fromTo("#problem-c2", { borderColor: "#26384f" }, { borderColor: "#ffb020", duration: 0.3 }, ${r2(at.p3 + 0.2)});
    tl.fromTo("#problem-tag", { opacity: 0, filter: "blur(6px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.25 }, ${r2(at.p3 + 0.3)});
    tl.to("#problem-tag", { opacity: 0, duration: 0.2 }, ${r2(p4 - 0.1)});
    tl.fromTo("#problem-answer", { opacity: 0, y: 24 }, { opacity: 1, y: 0, duration: 0.45, ease: "expo.out" }, ${r2(p4 + 0.1)});
${q.map((_, i) => `    tl.fromTo("#problem-ok${i}", { opacity: 0 }, { opacity: 1, duration: 0.25 }, ${r2(p4 + wordAt('p4', 'answers') + i * 0.12)});`).join('\n')}`;
  return { id, duration, html: sceneFile(id, duration, css, body, script.replace('<i ', '<i ')) };
}

function how() {
  const id = 'how';
  const { at, end } = sequence(['h1', 'h2', 'h3'], 0.7, 0.45);
  const duration = r2(end + 0.9);
  for (const k of Object.keys(at)) vo.push({ id: k, t: at[k], scene: id });
  const steps = ['identity', 'chain ticket', 'lockdowns', 'policy', 'flight recorder'];
  const stepT = steps.map((_, i) => r2(at.h3 + 0.3 + i * 0.32));
  sfx.push({ file: 'drop_002.ogg', t: r2(at.h2 + 0.3), vol: 0.4, scene: id });
  const css = `${chapterCss(id)}
    #how-row { position: absolute; top: 230px; left: 120px; width: 1680px; display: flex; align-items: center; gap: 34px; }
    .how-end { width: 300px; padding: 30px; border-radius: 20px; background: var(--panel); border: 2px solid var(--line-2); text-align: center; font-size: 32px; line-height: 1.35; color: var(--ink); }
    .how-end small { display: block; margin-top: 8px; font-size: 28px; color: var(--muted); }
    .how-arrow { font-size: 46px; color: var(--muted); }
    #how-bifrost { flex: 1; padding: 30px 36px 36px; border-radius: 24px; border: 2px solid var(--line-2); background: var(--ground-2); }
    #how-bifrost h3 { margin: 0 0 18px; font-family: var(--display); font-weight: 600; font-size: 38px; letter-spacing: 0.14em; color: var(--muted); }
    #how-hooks { display: flex; gap: 14px; font-family: var(--mono); font-size: 28px; }
    #how-hooks span { padding: 10px 16px; border-radius: 10px; border: 1.5px solid var(--line-2); color: var(--ink); }
    #how-plugin { margin-top: 22px; padding: 18px 24px; border-radius: 14px; border: 2px solid var(--cyan); color: var(--cyan); font-size: 32px; font-weight: 700; display: flex; justify-content: space-between; }
    #how-plugin small { color: var(--muted); font-weight: 400; font-size: 28px; }
    #how-ticket { position: absolute; top: 560px; left: 120px; width: 760px; padding: 30px 36px; border-radius: 20px; background: var(--panel); border: 2px dashed var(--line-2); }
    #how-ticket h4 { margin: 0 0 16px; font-family: var(--display); font-weight: 600; font-size: 32px; letter-spacing: 0.12em; color: var(--muted); }
    #how-ticket code { display: block; font-family: var(--mono); font-size: 30px; color: var(--ink); }
    #how-ticket p { margin: 18px 0 0; font-size: 30px; color: var(--ink); }
    #how-ticket p b { color: var(--green); }
    #how-ticket p i { font-style: normal; color: var(--red); }
    #how-pipe { position: absolute; top: 560px; left: 940px; width: 860px; }
    #how-pipe h4 { margin: 0 0 18px; font-family: var(--display); font-weight: 600; font-size: 32px; letter-spacing: 0.12em; color: var(--muted); }
    #how-steps { display: flex; flex-wrap: wrap; gap: 14px; }
    #how-steps span { padding: 14px 22px; border-radius: 999px; background: var(--panel); border: 2px solid var(--line-2); font-size: 30px; }
    #how-steps span.rec { border-color: var(--cyan); color: var(--cyan); }
    #how-time { margin-top: 26px; font-size: 32px; color: var(--ink); }
    #how-time b { font-family: var(--display); font-size: 54px; color: var(--cyan); margin-right: 14px; }`;
  const body = `${chapterHtml(id, '05', 'How it works')}
    <div id="how-row">
      <div id="how-caller" class="how-end">people, apps<br />and agents<small>calls with their own keys</small></div>
      <div class="how-arrow">→</div>
      <div id="how-bifrost">
        <h3>BIFROST · NOT FORKED</h3>
        <div id="how-hooks"><span>HTTP transport</span><span>PreLLM</span><span>PreMCP</span><span>PreA2A</span></div>
        <div id="how-plugin">Gjallarhorn plugin<small>official plugin hooks · Go</small></div>
      </div>
      <div class="how-arrow">→</div>
      <div id="how-up" class="how-end">models, tools<br />and agents<small>upstream</small></div>
    </div>
    <div id="how-ticket">
      <h4>CHAIN TICKET · SIGNED BY THE GATEWAY</h4>
      <code>customer-chat › support › billing</code>
      <p><b>Agents can drop it.</b> <i>They can’t forge it.</i></p>
    </div>
    <div id="how-pipe">
      <h4>EVERY MODEL, TOOL AND AGENT CALL</h4>
      <div id="how-steps">${steps.map((s, i) => `<span id="how-s${i}"${i === steps.length - 1 ? ' class="rec"' : ''}>${s}</span>`).join('')}</div>
      <div id="how-time"><b>45–56 µs</b>median decision inside the running gateway</div>
    </div>`;
  const script = `${chapterJs(id)}
    tl.fromTo("#how-caller", { opacity: 0, x: -30 }, { opacity: 1, x: 0, duration: 0.4, ease: "expo.out" }, ${r2(at.h1 + 0.2)});
    tl.fromTo("#how-bifrost", { opacity: 0, scale: 0.96 }, { opacity: 1, scale: 1, duration: 0.45, ease: "expo.out" }, ${r2(at.h1 + 0.5)});
    tl.fromTo("#how-up", { opacity: 0, x: 30 }, { opacity: 1, x: 0, duration: 0.4, ease: "expo.out" }, ${r2(at.h1 + 0.8)});
    tl.fromTo("#how-plugin", { opacity: 0, filter: "blur(8px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.3, ease: "power3.out" }, ${r2(at.h1 + wordAt('h1', 'plugin'))});
    tl.fromTo("#how-ticket", { opacity: 0, y: 30 }, { opacity: 1, y: 0, duration: 0.45, ease: "expo.out" }, ${r2(at.h2 + 0.3)});
    tl.fromTo("#how-ticket p", { opacity: 0 }, { opacity: 1, duration: 0.3 }, ${r2(at.h2 + wordAt('h2', 'drop') - 0.1)});
    tl.fromTo("#how-pipe h4", { opacity: 0 }, { opacity: 1, duration: 0.3 }, ${r2(at.h3 + 0.1)});
${stepT.map((st, i) => `    tl.fromTo("#how-s${i}", { opacity: 0, y: 16 }, { opacity: 1, y: 0, duration: 0.3, ease: "expo.out" }, ${st});`).join('\n')}
    tl.fromTo("#how-time", { opacity: 0, filter: "blur(6px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.3 }, ${r2(at.h3 + wordAt('h3', 'fifty') - 0.1)});`;
  return { id, duration, html: sceneFile(id, duration, css, body, script) };
}

function proof() {
  const id = 'proof';
  const { at, end } = sequence(['r1', 'r2', 'r3', 'r4'], 0.7, 0.45);
  const duration = r2(end + 1.0);
  for (const k of Object.keys(at)) vo.push({ id: k, t: at[k], scene: id });
  const t12 = r2(at.r2 + wordAt('r2', 'twelve') - 0.05);
  const t0 = r2(at.r2 + wordAt('r2', 'none') - 0.05);
  sfx.push({ file: 'impactSoft_medium_001.ogg', t: t12, vol: 0.45, scene: id });
  sfx.push({ file: 'impactBell_heavy_003.ogg', t: t0, vol: 0.5, scene: id });
  const files = [
    ['results/attacklab-20261115T055939Z.json', 'attacks, normal work, co-pilot drill'],
    ['results/measure-20261115T055411Z/', 'lockdown speed, decision time'],
    ['results/coverage-20261115T053752Z.txt', 'engine test coverage 95.3%'],
    ['docs/threat-model.md', 'what it does not protect against'],
  ];
  const css = `${chapterCss(id)}
    #proof-head { position: absolute; top: 160px; left: 0; width: 100%; text-align: center; font-family: var(--display); font-weight: 600; font-size: 64px; letter-spacing: 0.06em; }
    #proof-nums { position: absolute; top: 265px; left: 0; width: 100%; display: flex; justify-content: center; gap: 120px; }
    .proof-num { width: 560px; padding: 30px 40px 36px; border-radius: 24px; background: var(--panel); border: 2px solid var(--line-2); text-align: center; }
    .proof-num b { display: block; font-family: var(--display); font-weight: 700; font-size: 180px; line-height: 1.05; }
    .proof-num span { display: block; font-size: 34px; color: var(--ink); }
    #proof-without b { color: var(--red); }
    #proof-with b { color: var(--cyan); }
    #proof-normal { position: absolute; top: 612px; left: 0; width: 100%; text-align: center; font-size: 42px; color: var(--ink); }
    #proof-normal b { color: var(--green); font-family: var(--display); font-weight: 700; font-size: 56px; margin: 0 8px; }
    #proof-files { position: absolute; top: 712px; left: 140px; width: 1640px; display: grid; grid-template-columns: 1fr 1fr; gap: 8px 40px; }
    #proof-files div { font-size: 28px; color: var(--muted); }
    #proof-files code { display: block; font-family: var(--mono); font-size: 28px; color: var(--ink); }`;
  const body = `${chapterHtml(id, '06', 'Proof')}
    <h1 id="proof-head">31 attacks through the real gateway</h1>
    <div id="proof-nums">
      <div id="proof-without" class="proof-num"><b>12</b><span>caused real harm without Gjallarhorn</span></div>
      <div id="proof-with" class="proof-num"><b>0</b><span>caused harm with Gjallarhorn</span></div>
    </div>
    <p id="proof-normal"><b>11 / 11</b> pieces of normal work done · <b>0</b> false blocks</p>
    <div id="proof-files">
${files.map((f, i) => `      <div id="proof-f${i}"><code>${f[0]}</code>${esc(f[1])}</div>`).join('\n')}
    </div>`;
  const script = `${chapterJs(id)}
    tl.fromTo("#proof-head", { opacity: 0, filter: "blur(8px)" }, { opacity: 1, filter: "blur(0px)", duration: 0.25, ease: "power3.out" }, ${r2(at.r1 + 0.2)});
    tl.fromTo("#proof-without", { opacity: 0, y: 40 }, { opacity: 1, y: 0, duration: 0.45, ease: "expo.out" }, ${t12});
    tl.fromTo("#proof-with", { opacity: 0, scale: 1.12, filter: "blur(12px)" }, { opacity: 1, scale: 1, filter: "blur(0px)", duration: 0.35, ease: "power4.out" }, ${t0});
    tl.fromTo("#proof-normal", { opacity: 0, y: 20 }, { opacity: 1, y: 0, duration: 0.4, ease: "expo.out" }, ${r2(at.r3 + 0.1)});
${files.map((_, i) => `    tl.fromTo("#proof-f${i}", { opacity: 0 }, { opacity: 1, duration: 0.3 }, ${r2(at.r4 + 0.3 + i * 0.25)});`).join('\n')}`;
  return { id, duration, html: sceneFile(id, duration, css, body, script) };
}

function close() {
  const id = 'close';
  const { at, end } = sequence(['e1', 'e2'], 0.6, 0.6);
  const duration = r2(end + 4.3);
  for (const k of Object.keys(at)) vo.push({ id: k, t: at[k], scene: id });
  const cmds = ['git clone https://github.com/shdra06/Gjallarhorn.git', 'cd Gjallarhorn', './demo.sh'];
  const cmdT = [r2(at.e1 + wordAt('e1', 'clone') - 0.1), r2(at.e1 + wordAt('e1', 'run') - 0.25), r2(at.e1 + wordAt('e1', 'command') - 0.05)];
  cmdT.forEach((ct, i) => sfx.push({ file: ['keypress-011.wav', 'keypress-019.wav', 'keypress-027.wav'][i], t: ct, vol: 0.45, scene: id }));
  const endT = r2(at.e2 - 0.1);
  sfx.push({ file: 'impactBell_heavy_000.ogg', t: r2(endT + 0.05), vol: 0.5, scene: id });
  const css = `
    #close-term { position: absolute; top: 250px; left: 360px; width: 1200px; border-radius: 18px; background: #050811; border: 2px solid var(--line-2); overflow: hidden; }
    #close-term .bar { height: 46px; background: var(--panel); display: flex; align-items: center; gap: 10px; padding: 0 18px; }
    #close-term .bar i { width: 14px; height: 14px; border-radius: 50%; background: var(--line-2); }
    #close-term pre { margin: 0; padding: 30px 36px 40px; font-family: var(--mono); font-size: 34px; line-height: 1.7; color: var(--ink); }
    #close-term pre span { display: block; }
    #close-term pre span::before { content: "$ "; color: var(--green); }
    #close-sub { position: absolute; top: 640px; left: 0; width: 100%; text-align: center; font-size: 34px; color: var(--muted); }
    #close-end { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 30px; background: var(--ground); }
    #close-row { display: flex; align-items: center; gap: 40px; }
    #close-word { font-family: var(--display); font-weight: 700; font-size: 150px; letter-spacing: 0.1em; line-height: 1; }
    #close-line { font-size: 44px; color: var(--ink); }
    #close-url { font-family: var(--mono); font-size: 38px; color: var(--cyan); }
    #close-event { font-size: 28px; color: var(--muted); }`;
  const body = `    <div id="close-termw" class="clip" data-start="0" data-duration="${r2(endT + 0.05)}">
      <div id="close-term">
        <div class="bar"><i></i><i></i><i></i></div>
        <pre>${cmds.map((c, i) => `<span id="close-c${i}">${c}</span>`).join('')}</pre>
      </div>
    </div>
    <p id="close-sub" class="clip" data-start="0" data-duration="${r2(endT + 0.05)}">Podman or Docker · works offline</p>
    <div id="close-end" class="clip" data-start="${endT}" data-duration="${r2(duration - endT)}">
      <div id="close-row">${LOGO(130, 'close-logo')}<div id="close-word">GJALLARHORN</div></div>
      <p id="close-line">The horn that warns you when your agents go wrong.</p>
      <p id="close-url">github.com/shdra06/Gjallarhorn</p>
      <p id="close-event">Geek Room Global AI Hackathon 2026 · Bifrost track (PS04) · Apache 2.0</p>
    </div>`;
  const script = `    tl.fromTo("#close-term", { opacity: 0, y: 30 }, { opacity: 1, y: 0, duration: 0.45, ease: "expo.out" }, ${r2(at.e1 + 0.1)});
${cmdT.map((ct, i) => `    tl.fromTo("#close-c${i}", { opacity: 0, clipPath: "inset(0 100% 0 0)" }, { opacity: 1, clipPath: "inset(0 0% 0 0)", duration: ${i === 0 ? 0.7 : 0.35}, ease: "none" }, ${ct});`).join('\n')}
    tl.fromTo("#close-sub", { opacity: 0 }, { opacity: 1, duration: 0.4 }, ${r2(cmdT[0] + 0.8)});
    tl.fromTo("#close-logo", { opacity: 0, scale: 0.7, rotation: -12 }, { opacity: 1, scale: 1, rotation: 0, duration: 0.45, ease: "back.out(1.6)" }, ${r2(endT + 0.05)});
    tl.fromTo("#close-word", { opacity: 0, scale: 1.1, filter: "blur(10px)" }, { opacity: 1, scale: 1, filter: "blur(0px)", duration: 0.5, ease: "expo.out" }, ${r2(endT + 0.1)});
    tl.fromTo("#close-line", { opacity: 0 }, { opacity: 1, duration: 0.4 }, ${r2(at.e2 + wordAt('e2', 'horn') - 0.1)});
    tl.fromTo("#close-url", { opacity: 0 }, { opacity: 1, duration: 0.4 }, ${r2(at.e2 + dur('e2') + 0.2)});
    tl.fromTo("#close-event", { opacity: 0 }, { opacity: 1, duration: 0.4 }, ${r2(at.e2 + dur('e2') + 0.5)});`;
  return { id, duration, html: sceneFile(id, duration, css, body, script, { fadeOut: false }) };
}

// ---- Assemble -------------------------------------------------------------------------------------
const parts = [];
parts.push(intro());
parts.push(problem());
parts.push(
  footageScene({
    id: 'attack',
    clip: 'hook',
    chapter: ['02', 'An attack, live'],
    extraSfx: [{ at: 'blocked', file: 'impactSoft_medium_001.ogg', vol: 0.55, offset: 0.02 }],
    moves: ({ m, local }) => {
      const demo = m('hover-attack').rect;
      const marker = m('blocked').rect;
      const panel = m('incident-open').rect;
      const cop = m('copilot-visible').rect;
      const modal = m('lock-dialog').rect;
      const billing = m('locked').rect;
      return [
        { t: local('hover-attack') - 0.5, rect: demo && { x: demo.x - 10, y: demo.y - 40, w: demo.w + 700, h: demo.h + 80 }, max: 1.6 },
        { t: local('click-attack') + 0.15, rect: null, d: 0.7 },
        { t: local('blocked') + 0.2, rect: marker && { x: marker.x - 420, y: marker.y - 240, w: marker.w + 840, h: marker.h + 480 }, max: 1.6, d: 0.9 },
        { t: local('incident-open') - 0.1, rect: panel && { x: panel.x - 30, y: panel.y, w: panel.w + 60, h: 600 }, max: 1.75, d: 0.8 },
        { t: local('copilot-visible') - 1.2, rect: cop && { x: cop.x - 30, y: Math.max(60, cop.y - 30), w: cop.w + 60, h: 600 }, max: 1.75, d: 1.0 },
        { t: local('lock-dialog') - 0.15, rect: modal && { x: modal.x - 80, y: modal.y - 60, w: modal.w + 160, h: modal.h + 120 }, max: 1.6, d: 0.6 },
        { t: local('locked') + 0.25, rect: billing && { x: billing.x - 520, y: billing.y - 160, w: billing.w + 1040, h: 620 }, max: 1.5, d: 0.9 },
        { t: local('vo:n4') + 0.4, rect: null, d: 1.2 },
      ];
    },
  }),
);
parts.push(
  footageScene({
    id: 'copilot',
    clip: 'copilot',
    chapter: ['03', 'The AI co-pilot closes a gap'],
    extraSfx: [{ at: 'stopped', file: 'impactSoft_medium_001.ogg', vol: 0.55, offset: -0.6 }],
    moves: ({ m, local }) => {
      const sim = m('replay-checked').rect;
      const harm = m('harm').rect;
      const panel = m('incident-open').rect;
      const modal = m('apply-dialog').rect;
      const stopped = m('stopped').rect;
      const right = panel && { x: panel.x - 30, y: 80, w: panel.w + 60, h: 600 };
      return [
        { t: local('replay-checked') - 0.2, rect: sim && { x: sim.x - 40, y: sim.y - 20, w: sim.w + 80, h: 480 }, max: 1.8, d: 0.8 },
        { t: local('saved') + 0.2, rect: null, d: 0.7 },
        { t: local('harm') + 0.15, rect: harm && { x: harm.x - 380, y: harm.y - 260, w: harm.w + 420, h: harm.h + 300 }, max: 1.9, d: 0.8 },
        { t: local('vo:c4') - 0.3, rect: null, d: 0.7 },
        { t: local('incident-open') + 0.1, rect: right, max: 1.7, d: 0.8 },
        { t: local('apply-dialog') - 0.1, rect: modal && { x: modal.x - 80, y: modal.y - 60, w: modal.w + 160, h: modal.h + 120 }, max: 1.6, d: 0.6 },
        { t: local('applied') + 0.1, rect: right, max: 1.7, d: 0.6 },
        { t: local('airspace-again') - 0.2, rect: null, d: 0.6 },
        { t: local('stopped') + 0.1, rect: stopped && { x: stopped.x - 380, y: stopped.y - 260, w: stopped.w + 420, h: stopped.h + 300 }, max: 1.9, d: 0.8 },
      ];
    },
  }),
);
parts.push(
  footageScene({
    id: 'tour',
    clip: 'tour',
    chapter: ['04', 'Everything is recorded'],
    moves: ({ m, local }) => {
      const node = m('node-panel').rect;
      return [
        { t: local('replay') + 0.1, rect: { x: 0, y: 560, w: 1920, h: 520 }, max: 1.35, d: 0.9 },
        { t: local('vo:t2') - 0.2, rect: null, d: 0.6 },
        { t: local('node-panel') + 0.05, rect: node && { x: node.x - 40, y: node.y, w: node.w + 80, h: 560 }, max: 1.7, d: 0.7 },
        { t: local('table') + 0.05, rect: null, d: 0.5 },
      ];
    },
  }),
);
parts.push(how());
parts.push(proof());
parts.push(close());

// Lay the scenes end to end.
let clock = 0;
const placed = parts.map((p) => {
  const s = { ...p, start: r2(clock) };
  clock += p.duration;
  return s;
});
const total = r2(clock);
const startOf = Object.fromEntries(placed.map((p) => [p.id, p.start]));
const voAbs = vo.map((v) => ({ ...v, abs: r2(startOf[v.scene] + v.t) })).sort((a, b) => a.abs - b.abs);
// A line re-voiced longer than its slot in the footage nudges the next one later, never over it.
for (let i = 1; i < voAbs.length; i++) {
  const free = r2(voAbs[i - 1].abs + dur(voAbs[i - 1].id) + 0.12);
  if (voAbs[i].abs < free) {
    console.log(`  ${voAbs[i].id} moved ${r2(free - voAbs[i].abs)} s later so it doesn't overlap ${voAbs[i - 1].id}`);
    voAbs[i].abs = free;
  }
}
const sfxAbs = sfx.map((s) => ({ ...s, abs: r2(startOf[s.scene] + s.t) })).sort((a, b) => a.abs - b.abs);
const introEnd = placed[0].duration;
const nameT = placed[0].marks.name;

// Music: one track, in pieces that cross-fade at the same point in the file, so the level can
// change (forward in the intro, swelling on the name, ducked under the demo) without a seam. The
// demo is longer than the track, so it loops once, cross-faded.
const TRACK = cues.duration;
const levels = [
  [0, nameT - 0.3, 0.2],
  [nameT - 0.3, nameT + 2.3, 0.4],
  [nameT + 2.3, introEnd - 0.4, 0.22],
  [introEnd - 0.4, total, 0.1],
];
const music = [];
let loopOffset = 0; // timeline time where the current pass of the track started
for (const [a, b, vol] of levels) {
  let s = a;
  while (s < b - 0.05) {
    const media = s - loopOffset;
    const room = TRACK - 2.5 - media;
    const e = Math.min(b, s + room);
    music.push({ start: r2(s), dur: r2(e - s + 0.4), media: r2(media), vol });
    if (e < b) loopOffset = e - cues.beats[0].time; // restart near the track's first beat
    s = e;
  }
}

const audio = [];
music.forEach((mu, i) => {
  const last = i === music.length - 1;
  audio.push(`      <audio id="music-${i}" class="clip" data-start="${mu.start}" data-duration="${last ? r2(total - mu.start) : mu.dur}" data-media-start="${mu.media}" data-track-index="${10 + (i % 2)}" data-volume="${mu.vol}" data-fade-in="${i === 0 ? 0.6 : 0.4}" data-fade-out="${last ? 3 : 0.4}" src="assets/music/bed.mp3"></audio>`);
});
voAbs.forEach((v) => {
  audio.push(`      <audio id="vo-${v.id}" class="clip" data-start="${v.abs}" data-duration="${Math.floor(dur(v.id) * 100) / 100}" data-track-index="3" data-volume="1" src="assets/vo/${v.id}.wav"></audio>`);
});
const sfxLen = {};
const lengthOf = (file) =>
  (sfxLen[file] ??= Math.floor(Number(execFileSync('ffprobe', ['-v', 'error', '-show_entries', 'format=duration', '-of', 'csv=p=0', join(comp, 'assets', 'sfx', file)]).toString()) * 100) / 100);
sfxAbs.forEach((s, i) => {
  audio.push(`      <audio id="sfx-${i}" class="clip" data-start="${s.abs}" data-duration="${lengthOf(s.file)}" data-track-index="${20 + (i % 4)}" data-volume="${s.vol}" src="assets/sfx/${s.file}"></audio>`);
});

// Captions: each line in groups of up to eight words, timed by characters.
const groups = [];
for (const v of voAbs) {
  const words = text[v.id].split(/\s+/);
  const chunks = [];
  let cur = [];
  for (const w of words) {
    cur.push(w);
    if (cur.length >= 8 || (/[.,:;?!]$/.test(w) && cur.length >= 4)) {
      chunks.push(cur.join(' '));
      cur = [];
    }
  }
  if (cur.length) {
    if (cur.length < 3 && chunks.length) chunks[chunks.length - 1] += ' ' + cur.join(' ');
    else chunks.push(cur.join(' '));
  }
  const total = chunks.reduce((a, c) => a + c.length, 0);
  const lead = 0.1;
  const span = dur(v.id) - lead - 0.15;
  let t = v.abs + lead;
  for (const c of chunks) {
    const d = (c.length / total) * span;
    groups.push({ text: c, start: r2(t - 0.05), end: r2(t + d + 0.1) });
    t += d;
  }
}
// No overlaps: each group ends before the next starts.
for (let i = 0; i < groups.length - 1; i++) groups[i].end = Math.min(groups[i].end, r2(groups[i + 1].start - 0.02));

const captionsHtml = `<template>
  <style>
    #root { position: absolute; inset: 0; pointer-events: none; ${PALETTE} }
    .cap { position: absolute; left: 0; bottom: 64px; width: 100%; display: flex; justify-content: center; }
    .cap span { max-width: 1500px; padding: 12px 28px; border-radius: 14px; background: rgba(6, 10, 19, 0.82); color: #f3f7fc;
      font-family: var(--body); font-weight: 500; font-size: 40px; line-height: 1.3; text-align: center; }
  </style>
  <div id="root" data-composition-id="captions" data-width="${W}" data-height="${H}" data-layout-allow-caption-zone>
${groups.map((g, i) => `    <div id="cap-${i}" class="cap clip" data-start="${g.start}" data-duration="${r2(g.end - g.start)}"><span>${esc(g.text)}</span></div>`).join('\n')}
  </div>
  <script>
    const tl = gsap.timeline({ paused: true });
${groups.map((g, i) => `    tl.fromTo("#cap-${i} span", { opacity: 0 }, { opacity: 1, duration: 0.1, ease: "power2.out" }, ${g.start});`).join('\n')}
    window.__timelines["captions"] = tl;
  </script>
</template>
`;

const hosts = placed
  .map((p, i) => `      <div id="${p.id}" data-composition-id="${p.id}" data-composition-src="compositions/${p.id}.html" data-start="${p.start}" data-duration="${p.duration}" data-track-index="1" data-track-kind="graphics" data-width="${W}" data-height="${H}" style="z-index: ${i + 1}"></div>`)
  .join('\n');

const index = `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=${W}, height=${H}" />
    <script src="https://cdn.jsdelivr.net/npm/gsap@3.15.0/dist/gsap.min.js"></script>
    <style>
      * { margin: 0; padding: 0; box-sizing: border-box; }
      html, body { margin: 0; width: ${W}px; height: ${H}px; overflow: hidden; background: #060a13; }
      #ground { position: absolute; inset: 0; background: #060a13; }
    </style>
  </head>
  <body>
    <div id="root" data-composition-id="main" data-start="0" data-duration="${total}" data-width="${W}" data-height="${H}">
      <div id="ground"></div>
${hosts}
      <div id="captions" data-composition-id="captions" data-composition-src="compositions/captions.html" data-start="0" data-duration="${total}" data-track-index="2" data-track-kind="captions" data-width="${W}" data-height="${H}" style="z-index: 40"></div>
${audio.join('\n')}
    </div>
    <script>
      const tl = gsap.timeline({ paused: true });
      window.__timelines["main"] = tl;
    </script>
  </body>
</html>
`;

mkdirSync(join(comp, 'compositions'), { recursive: true });
for (const p of placed) writeFileSync(join(comp, 'compositions', `${p.id}.html`), p.html);
writeFileSync(join(comp, 'compositions', 'captions.html'), captionsHtml);
writeFileSync(join(comp, 'index.html'), index);
const mmss = (s) => `${Math.floor(s / 60)}:${String(Math.round(s % 60)).padStart(2, '0')}`;
for (const p of placed) console.log(`${p.id.padEnd(8)} ${mmss(p.start)}  ${p.duration.toFixed(1)} s`);
console.log(`total    ${mmss(total)} (${total} s), ${voAbs.length} lines, ${sfxAbs.length} sounds, ${groups.length} captions, name at ${nameT} s`);
