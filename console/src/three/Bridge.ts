import * as THREE from 'three';
import { CSS2DObject, CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';

/**
 * The landing page's hero. In the myth, Heimdall guards Bifrost, the rainbow bridge, and sounds
 * Gjallarhorn when danger comes. Here the bridge is the gateway: calls stream across it as
 * comets and pass through the gate. Every few seconds a red comet (an attack) reaches the gate:
 * the shield lights up, the horn sounds, a shockwave rolls out, and the comet breaks into sparks.
 * Decorative only.
 */

export interface BridgeOptions {
  reducedMotion: boolean;
  theme?: 'dark' | 'light';
  /** Called when an attack is stopped, with the gate's place on screen (0..1). */
  onBlock?: (x: number, y: number) => void;
}

interface Comet {
  band: number;
  u: number;
  speed: number;
  red: boolean;
  head: THREE.Sprite;
  trail: THREE.Sprite[];
  history: THREE.Vector3[];
  stopped: boolean;
}
interface Wave {
  mesh: THREE.Mesh;
  born: number;
  life: number;
  from: number;
  to: number;
  drift: THREE.Vector3;
}

const SAMPLES = 1000;
const GATE_U = 0.6;

const THEMES = {
  dark: {
    bands: [0xff4d6a, 0xff9f43, 0xffd84d, 0x3ddc97, 0x38e1ff, 0x6c8cff, 0xb9a7ff],
    bandOpacity: 0.6,
    halo: 0x38e1ff,
    haloOpacity: 0.05,
    comet: 0xe6fbff,
    red: 0xff3b5c,
    shield: 0x38e1ff,
    frame: 0xd9a441,
    stars: 0.7,
    blend: THREE.AdditiveBlending as THREE.Blending,
    ambient: 0x8090c0,
    ambientIntensity: 1.2,
  },
  light: {
    bands: [0xe11d48, 0xea580c, 0xd97706, 0x059669, 0x0284c7, 0x4f46e5, 0x7c3aed],
    bandOpacity: 0.75,
    halo: 0x0284c7,
    haloOpacity: 0.035,
    comet: 0x0369a1,
    red: 0xdc2626,
    shield: 0x0284c7,
    frame: 0xb7791f,
    stars: 0,
    blend: THREE.NormalBlending,
    ambient: 0xffffff,
    ambientIntensity: 2,
  },
};

function glowTexture(): THREE.Texture {
  const c = document.createElement('canvas');
  c.width = c.height = 64;
  const g = c.getContext('2d')!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, 'rgba(255,255,255,1)');
  grad.addColorStop(0.3, 'rgba(255,255,255,0.6)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

/** A curved horn: rings of growing radius along a bent path, from the tip to the mouth. */
function hornGeometry(path: THREE.Curve<THREE.Vector3>, tip: number, mouth: number): THREE.BufferGeometry {
  const steps = 64;
  const around = 28;
  const frames = path.computeFrenetFrames(steps, false);
  const pos: number[] = [];
  const idx: number[] = [];
  for (let i = 0; i <= steps; i++) {
    const t = i / steps;
    const r = tip + (mouth - tip) * Math.pow(t, 2.6);
    const p = path.getPointAt(t);
    const n = frames.normals[i]!;
    const b = frames.binormals[i]!;
    for (let j = 0; j < around; j++) {
      const a = (j / around) * Math.PI * 2;
      pos.push(p.x + r * (Math.cos(a) * n.x + Math.sin(a) * b.x), p.y + r * (Math.cos(a) * n.y + Math.sin(a) * b.y), p.z + r * (Math.cos(a) * n.z + Math.sin(a) * b.z));
    }
  }
  for (let i = 0; i < steps; i++) {
    for (let j = 0; j < around; j++) {
      const a = i * around + j;
      const b = i * around + ((j + 1) % around);
      idx.push(a, a + around, b, b, a + around, b + around);
    }
  }
  const g = new THREE.BufferGeometry();
  g.setAttribute('position', new THREE.Float32BufferAttribute(pos, 3));
  g.setIndex(idx);
  g.computeVertexNormals();
  return g;
}

export class Bridge {
  private renderer: THREE.WebGLRenderer;
  private labels = new CSS2DRenderer();
  private scene = new THREE.Scene();
  private camera = new THREE.PerspectiveCamera(40, 1, 0.1, 600);
  private world = new THREE.Group();
  private c: (typeof THEMES)['dark'];
  private glow = glowTexture();
  private lanes: THREE.Vector3[][] = [];
  private flow!: THREE.Points;
  private flowU: Float32Array = new Float32Array(0);
  private flowBand: Uint8Array = new Uint8Array(0);
  private comets: Comet[] = [];
  private waves: Wave[] = [];
  private sparks: { points: THREE.Points; vel: Float32Array; born: number } | null = null;
  private shield!: THREE.LineSegments;
  private shieldFill!: THREE.Mesh;
  private horn!: THREE.Mesh;
  private hornMouth = new THREE.Vector3();
  private hornDir = new THREE.Vector3();
  private gatePos = new THREE.Vector3();
  private gateQuat = new THREE.Quaternion();
  private alarm = 0;
  private stars: THREE.Points | null = null;
  private pointer = new THREE.Vector2();
  private nextComet = 0;
  private nextAttack = 1.8;
  private now = 0;
  private frame = 0;
  private gone = false;
  private observer: ResizeObserver;

  constructor(
    private host: HTMLElement,
    private opts: BridgeOptions,
  ) {
    this.c = THEMES[opts.theme ?? 'dark'];
    this.renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.setClearColor(0x000000, 0);
    this.renderer.domElement.setAttribute('aria-hidden', 'true');
    host.appendChild(this.renderer.domElement);
    this.labels.domElement.className = 'hero-labels';
    host.appendChild(this.labels.domElement);

    this.scene.add(new THREE.AmbientLight(this.c.ambient, this.c.ambientIntensity));
    const key = new THREE.DirectionalLight(0xffffff, 2.2);
    key.position.set(-30, 50, 60);
    this.scene.add(key);

    // The bridge leans away from the viewer, on the right of the words.
    this.world.position.set(16, -10, -6);
    this.world.rotation.y = -0.82;
    this.scene.add(this.world);

    this.buildBridge();
    this.buildGate();
    this.buildHorn();
    if (this.c.stars) this.buildStars();
    this.buildLabels();

    window.addEventListener('pointermove', this.onPointer);
    this.observer = new ResizeObserver(() => this.resize());
    this.observer.observe(host);
    this.resize();

    if (opts.reducedMotion) {
      // One still frame: calls on the bridge, and an attack already stopped at the gate.
      for (let i = 0; i < 10; i++) this.launch(false, 0.05 + i * 0.09);
      this.launch(true, GATE_U);
      this.update(0, 0);
      this.draw();
    } else {
      this.loop(0);
    }
  }

  private lane(band: number, u: number, out = new THREE.Vector3()): THREE.Vector3 {
    const pts = this.lanes[band]!;
    const f = Math.min(SAMPLES - 1e-6, Math.max(0, u * SAMPLES));
    const i = Math.floor(f);
    return out.copy(pts[i]!).lerp(pts[i + 1]!, f - i);
  }

  private buildBridge() {
    const spine = new THREE.CatmullRomCurve3([
      new THREE.Vector3(-78, -10, 0),
      new THREE.Vector3(-42, 14, 0),
      new THREE.Vector3(0, 24, 0),
      new THREE.Vector3(42, 14, 0),
      new THREE.Vector3(78, -10, 0),
    ]);
    const centre = spine.getSpacedPoints(SAMPLES);
    const bands = this.c.bands.length;
    for (let b = 0; b < bands; b++) {
      const off = (b - (bands - 1) / 2) * 0.95;
      const lane = centre.map((p, i) => {
        const t = spine.getTangentAt(i / SAMPLES);
        // Offset within the arc's plane: the bands stack like a rainbow.
        return new THREE.Vector3(p.x - t.y * off, p.y + t.x * off, p.z);
      });
      this.lanes.push(lane);
      const tube = new THREE.TubeGeometry(new THREE.CatmullRomCurve3(lane.filter((_, i) => i % 10 === 0)), 220, 0.16, 6, false);
      const mat = new THREE.MeshBasicMaterial({ color: this.c.bands[b], transparent: true, opacity: this.c.bandOpacity, blending: this.c.blend, depthWrite: false });
      this.world.add(new THREE.Mesh(tube, mat));
    }
    // A soft halo around the whole bridge.
    const halo = new THREE.Mesh(
      new THREE.TubeGeometry(spine, 200, 5, 16, false),
      new THREE.MeshBasicMaterial({ color: this.c.halo, transparent: true, opacity: this.c.haloOpacity, blending: this.c.blend, depthWrite: false }),
    );
    this.world.add(halo);

    // Light flowing along every band: the steady traffic of a gateway.
    const n = 900;
    this.flowU = new Float32Array(n);
    this.flowBand = new Uint8Array(n);
    const pos = new Float32Array(n * 3);
    const col = new Float32Array(n * 3);
    const tmp = new THREE.Color();
    for (let i = 0; i < n; i++) {
      this.flowU[i] = Math.random();
      this.flowBand[i] = Math.floor(Math.random() * bands);
      tmp.setHex(this.c.bands[this.flowBand[i]!]!);
      col.set([tmp.r, tmp.g, tmp.b], i * 3);
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    g.setAttribute('color', new THREE.BufferAttribute(col, 3));
    this.flow = new THREE.Points(
      g,
      new THREE.PointsMaterial({ size: 0.95, map: this.glow, vertexColors: true, transparent: true, opacity: 0.9, blending: this.c.blend, depthWrite: false }),
    );
    this.world.add(this.flow);
  }

  private buildGate() {
    const mid = (this.lanes.length - 1) / 2;
    this.lane(mid, GATE_U, this.gatePos);
    const ahead = this.lane(mid, GATE_U + 0.002);
    const dir = ahead.sub(this.gatePos).normalize();
    this.gateQuat.setFromUnitVectors(new THREE.Vector3(0, 0, 1), dir);

    const gate = new THREE.Group();
    gate.position.copy(this.gatePos);
    gate.quaternion.copy(this.gateQuat);
    this.world.add(gate);

    // A honeycomb shield across the bridge.
    const s = 1.05;
    const R = 8.2;
    const seg: number[] = [];
    for (let q = -8; q <= 8; q++) {
      for (let r = -8; r <= 8; r++) {
        const x = s * 1.5 * q;
        const y = s * Math.sqrt(3) * (r + q / 2);
        if (Math.hypot(x, y) > R) continue;
        for (let k = 0; k < 6; k++) {
          const a0 = (Math.PI / 3) * k;
          const a1 = (Math.PI / 3) * (k + 1);
          seg.push(x + s * Math.cos(a0) * 0.92, y + s * Math.sin(a0) * 0.92, 0, x + s * Math.cos(a1) * 0.92, y + s * Math.sin(a1) * 0.92, 0);
        }
      }
    }
    const sg = new THREE.BufferGeometry();
    sg.setAttribute('position', new THREE.Float32BufferAttribute(seg, 3));
    this.shield = new THREE.LineSegments(sg, new THREE.LineBasicMaterial({ color: this.c.shield, transparent: true, opacity: 0.22, blending: this.c.blend, depthWrite: false }));
    gate.add(this.shield);
    this.shieldFill = new THREE.Mesh(
      new THREE.CircleGeometry(R + 0.6, 6),
      new THREE.MeshBasicMaterial({ color: this.c.shield, transparent: true, opacity: 0.04, side: THREE.DoubleSide, blending: this.c.blend, depthWrite: false }),
    );
    this.shieldFill.rotation.z = Math.PI / 6;
    gate.add(this.shieldFill);
    // A golden frame.
    const frame = new THREE.Mesh(
      new THREE.TorusGeometry(R + 0.8, 0.22, 10, 6),
      new THREE.MeshStandardMaterial({ color: this.c.frame, metalness: 0.75, roughness: 0.3, emissive: this.c.frame, emissiveIntensity: 0.25 }),
    );
    frame.rotation.z = Math.PI / 6;
    gate.add(frame);
  }

  private buildHorn() {
    // The horn hangs above the gate, its mouth turned toward the traffic coming in.
    const base = this.gatePos.clone().add(new THREE.Vector3(0, 12.5, 0));
    const path = new THREE.CubicBezierCurve3(
      base.clone().add(new THREE.Vector3(7, 2.5, 0)),
      base.clone().add(new THREE.Vector3(3, 5, 0)),
      base.clone().add(new THREE.Vector3(-2, 1.5, 0)),
      base.clone().add(new THREE.Vector3(-7, 0.5, 0)),
    );
    this.horn = new THREE.Mesh(
      hornGeometry(path, 0.25, 2.6),
      new THREE.MeshStandardMaterial({ color: this.c.frame, metalness: 0.7, roughness: 0.28, emissive: this.c.frame, emissiveIntensity: 0.18, side: THREE.DoubleSide }),
    );
    this.world.add(this.horn);
    this.hornMouth.copy(path.getPointAt(1));
    this.hornDir.copy(path.getTangentAt(1)).normalize();
    const rim = new THREE.Mesh(
      new THREE.TorusGeometry(2.6, 0.2, 10, 40),
      new THREE.MeshStandardMaterial({ color: this.c.frame, metalness: 0.8, roughness: 0.25, emissive: this.c.frame, emissiveIntensity: 0.3 }),
    );
    rim.position.copy(this.hornMouth);
    rim.quaternion.setFromUnitVectors(new THREE.Vector3(0, 0, 1), this.hornDir);
    this.world.add(rim);
    const light = new THREE.PointLight(0xffd59a, 40, 40);
    light.position.copy(base).add(new THREE.Vector3(0, 6, 8));
    this.world.add(light);
  }

  private buildStars() {
    const n = 700;
    const pos = new Float32Array(n * 3);
    for (let i = 0; i < n; i++) {
      pos.set([(Math.random() - 0.5) * 400, Math.random() * 160 - 40, -60 - Math.random() * 200], i * 3);
    }
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    this.stars = new THREE.Points(g, new THREE.PointsMaterial({ size: 0.9, map: this.glow, color: 0xbfd8ff, transparent: true, opacity: this.c.stars, depthWrite: false }));
    this.scene.add(this.stars);
  }

  private buildLabels() {
    const mid = (this.lanes.length - 1) / 2;
    const add = (text: string, at: THREE.Vector3, cls: string) => {
      const el = document.createElement('div');
      el.className = `hero-label ${cls}`;
      el.textContent = text;
      const o = new CSS2DObject(el);
      o.position.copy(at);
      this.world.add(o);
    };
    add('your agents and apps', this.lane(mid, 0.03).add(new THREE.Vector3(0, -5, 0)), 'end');
    add('models · tools · other agents', this.lane(mid, 0.97).add(new THREE.Vector3(0, -5, 0)), 'end');
    add('GJALLARHORN', this.gatePos.clone().add(new THREE.Vector3(0, -11.5, 0)), 'gate');
  }

  private comet(color: number, size: number): THREE.Sprite {
    const s = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.glow, color, transparent: true, blending: this.c.blend, depthWrite: false }));
    s.scale.setScalar(size);
    this.world.add(s);
    return s;
  }

  private launch(red: boolean, u = 0) {
    const band = red ? Math.floor(this.lanes.length / 2) : Math.floor(Math.random() * this.lanes.length);
    const color = red ? this.c.red : this.c.comet;
    const head = this.comet(color, red ? 4.6 : 2.6);
    const trail = Array.from({ length: 9 }, (_, i) => {
      const s = this.comet(red ? this.c.red : this.c.bands[band]!, (red ? 3.6 : 2) * (1 - i / 10));
      (s.material as THREE.SpriteMaterial).opacity = 0.7 * (1 - i / 9);
      return s;
    });
    // Start on the bridge, so nothing is drawn at the scene's origin before the next update.
    const start = this.lane(band, u);
    head.position.copy(start);
    for (const s of trail) s.position.copy(start);
    this.comets.push({ band, u, speed: red ? 0.17 : 0.2 + Math.random() * 0.12, red, head, trail, history: [start.clone()], stopped: false });
  }

  private block(c: Comet) {
    c.stopped = true;
    this.alarm = 1;
    const at = this.lane(c.band, GATE_U);
    // Shockwaves from the gate, and the horn sounding.
    for (let i = 0; i < 3; i++) {
      this.wave(at, this.gateQuat, i * 0.18, 1.6, 1, 22, new THREE.Vector3());
    }
    const hq = new THREE.Quaternion().setFromUnitVectors(new THREE.Vector3(0, 0, 1), this.hornDir);
    for (let i = 0; i < 4; i++) {
      this.wave(this.hornMouth, hq, 0.1 + i * 0.22, 1.4, 2.6, 9, this.hornDir.clone().multiplyScalar(14));
    }
    // The comet breaks into sparks, thrown back the way it came.
    const n = 90;
    const pos = new Float32Array(n * 3);
    const vel = new Float32Array(n * 3);
    const back = this.lane(c.band, GATE_U - 0.01).sub(at).normalize();
    for (let i = 0; i < n; i++) {
      pos.set([at.x, at.y, at.z], i * 3);
      const v = new THREE.Vector3().randomDirection().multiplyScalar(6 + Math.random() * 10).addScaledVector(back, 9);
      vel.set([v.x, v.y, v.z], i * 3);
    }
    if (this.sparks) this.disposeSparks();
    const g = new THREE.BufferGeometry();
    g.setAttribute('position', new THREE.BufferAttribute(pos, 3));
    const points = new THREE.Points(g, new THREE.PointsMaterial({ size: 0.9, map: this.glow, color: this.c.red, transparent: true, blending: this.c.blend, depthWrite: false }));
    this.world.add(points);
    this.sparks = { points, vel, born: this.now };
    const screen = at.clone().applyMatrix4(this.world.matrixWorld).project(this.camera);
    this.opts.onBlock?.((screen.x + 1) / 2, (1 - screen.y) / 2);
  }

  private wave(at: THREE.Vector3, q: THREE.Quaternion, delay: number, life: number, from: number, to: number, drift: THREE.Vector3) {
    const m = new THREE.Mesh(
      new THREE.RingGeometry(0.92, 1, 64),
      new THREE.MeshBasicMaterial({ color: this.c.red, transparent: true, opacity: 0, side: THREE.DoubleSide, blending: this.c.blend, depthWrite: false }),
    );
    m.position.copy(at);
    m.quaternion.copy(q);
    this.world.add(m);
    this.waves.push({ mesh: m, born: this.now + delay, life, from, to, drift });
  }

  private disposeSparks() {
    if (!this.sparks) return;
    this.world.remove(this.sparks.points);
    this.sparks.points.geometry.dispose();
    (this.sparks.points.material as THREE.Material).dispose();
    this.sparks = null;
  }

  private onPointer = (e: PointerEvent) => {
    this.pointer.set((e.clientX / window.innerWidth) * 2 - 1, (e.clientY / window.innerHeight) * 2 - 1);
  };

  private resize() {
    const w = Math.max(1, this.host.clientWidth);
    const h = Math.max(1, this.host.clientHeight);
    this.renderer.setSize(w, h, false);
    this.renderer.domElement.style.width = '100%';
    this.renderer.domElement.style.height = '100%';
    this.labels.setSize(w, h);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    if (this.opts.reducedMotion) this.draw();
  }

  private update(t: number, dt: number) {
    this.now = t;
    const sway = this.opts.reducedMotion ? 0 : Math.sin(t * 0.12) * 5;
    this.camera.position.set(sway + this.pointer.x * 6, 14 - this.pointer.y * 4, 112);
    this.camera.lookAt(10, 4, 0);
    this.world.updateMatrixWorld();
    if (this.stars) this.stars.rotation.y = t * 0.004;

    // The steady flow along the bands.
    const pos = this.flow.geometry.getAttribute('position') as THREE.BufferAttribute;
    const v = new THREE.Vector3();
    for (let i = 0; i < this.flowU.length; i++) {
      this.flowU[i] = (this.flowU[i]! + dt * 0.06) % 1;
      this.lane(this.flowBand[i]!, this.flowU[i]!, v);
      pos.setXYZ(i, v.x, v.y, v.z);
    }
    pos.needsUpdate = true;

    // Comets.
    for (const c of this.comets) {
      if (!c.stopped) {
        c.u += dt * c.speed;
        if (c.red && c.u >= GATE_U) {
          c.u = GATE_U;
          this.block(c);
        } else if (!c.red && Math.abs(c.u - GATE_U) < dt * c.speed) {
          // An allowed call passes the gate: a faint ripple of the shield.
          (this.shield.material as THREE.LineBasicMaterial).opacity = Math.max((this.shield.material as THREE.LineBasicMaterial).opacity, 0.34);
        }
      }
      const p = this.lane(c.band, Math.min(c.u, 1));
      c.head.position.copy(p);
      c.history.unshift(p.clone());
      if (c.history.length > 40) c.history.pop();
      c.trail.forEach((s, i) => {
        const h = c.history[Math.min(c.history.length - 1, (i + 1) * 3)];
        if (h) s.position.copy(h);
        // A stopped comet's trail catches up and vanishes into the flash.
        if (c.stopped) (s.material as THREE.SpriteMaterial).opacity *= 0.9;
      });
      if (c.stopped) {
        const m = c.head.material as THREE.SpriteMaterial;
        m.opacity *= 0.93;
        c.head.scale.multiplyScalar(1.01);
      }
    }
    this.comets = this.comets.filter((c) => {
      const dead = c.u >= 1 || (c.stopped && (c.head.material as THREE.SpriteMaterial).opacity < 0.02);
      if (dead && !this.opts.reducedMotion) {
        for (const s of [c.head, ...c.trail]) {
          this.world.remove(s);
          (s.material as THREE.Material).dispose();
        }
      }
      return !dead || this.opts.reducedMotion;
    });

    // Shield and horn glow, then settle.
    const sm = this.shield.material as THREE.LineBasicMaterial;
    const fm = this.shieldFill.material as THREE.MeshBasicMaterial;
    const hm = this.horn.material as THREE.MeshStandardMaterial;
    if (this.alarm > 0) {
      sm.color.setHex(this.c.red);
      fm.color.setHex(this.c.red);
      sm.opacity = 0.22 + 0.7 * this.alarm;
      fm.opacity = 0.04 + 0.22 * this.alarm;
      hm.emissive.setHex(this.c.red);
      hm.emissiveIntensity = 0.18 + 0.9 * this.alarm;
      if (!this.opts.reducedMotion) this.alarm = Math.max(0, this.alarm - dt * 0.55);
    } else {
      sm.color.setHex(this.c.shield);
      fm.color.setHex(this.c.shield);
      sm.opacity = Math.max(0.22, sm.opacity - dt * 0.6);
      fm.opacity = 0.04;
      hm.emissive.setHex(this.c.frame);
      hm.emissiveIntensity = 0.18;
    }

    // Shockwaves.
    for (const w of this.waves) {
      const age = (t - w.born) / w.life;
      const m = w.mesh.material as THREE.MeshBasicMaterial;
      if (age < 0) continue;
      const k = Math.min(1, age);
      w.mesh.scale.setScalar(w.from + (w.to - w.from) * (1 - Math.pow(1 - k, 2)));
      m.opacity = 0.85 * (1 - k);
      if (this.opts.reducedMotion) {
        w.mesh.scale.setScalar(w.from + (w.to - w.from) * 0.5);
        m.opacity = 0.5;
      }
      w.mesh.position.addScaledVector(w.drift, dt);
    }
    this.waves = this.waves.filter((w) => {
      const dead = t - w.born > w.life && !this.opts.reducedMotion;
      if (dead) {
        this.world.remove(w.mesh);
        w.mesh.geometry.dispose();
        (w.mesh.material as THREE.Material).dispose();
      }
      return !dead;
    });

    // Sparks.
    if (this.sparks) {
      const age = t - this.sparks.born;
      const sp = this.sparks.points.geometry.getAttribute('position') as THREE.BufferAttribute;
      const step = this.opts.reducedMotion ? 0.35 : dt;
      for (let i = 0; i < sp.count; i++) {
        const vel = this.sparks.vel;
        vel[i * 3 + 1]! -= 9 * step;
        sp.setXYZ(i, sp.getX(i) + vel[i * 3]! * step, sp.getY(i) + vel[i * 3 + 1]! * step, sp.getZ(i) + vel[i * 3 + 2]! * step);
        vel[i * 3]! *= 0.97;
        vel[i * 3 + 2]! *= 0.97;
      }
      sp.needsUpdate = true;
      (this.sparks.points.material as THREE.PointsMaterial).opacity = this.opts.reducedMotion ? 0.9 : Math.max(0, 1 - age / 1.6);
      if (age > 1.7 && !this.opts.reducedMotion) this.disposeSparks();
    }

    if (!this.opts.reducedMotion) {
      if (t >= this.nextComet) {
        this.launch(false);
        this.nextComet = t + 0.22 + Math.random() * 0.3;
      }
      if (t >= this.nextAttack) {
        this.launch(true);
        this.nextAttack = t + 7;
      }
    }
  }

  private draw() {
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
  }

  private loop = (ms: number) => {
    if (this.gone) return;
    const t = ms / 1000;
    const dt = Math.min(0.05, t - this.now || 0.016);
    this.update(t, dt);
    this.draw();
    this.frame = requestAnimationFrame(this.loop);
  };

  dispose() {
    this.gone = true;
    cancelAnimationFrame(this.frame);
    window.removeEventListener('pointermove', this.onPointer);
    this.observer.disconnect();
    this.renderer.dispose();
    this.renderer.domElement.remove();
    this.labels.domElement.remove();
  }
}
