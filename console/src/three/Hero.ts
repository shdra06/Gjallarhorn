import * as THREE from 'three';
import { CSS2DObject, CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';
import { Line2 } from 'three/addons/lines/Line2.js';
import { LineGeometry } from 'three/addons/lines/LineGeometry.js';
import { LineMaterial } from 'three/addons/lines/LineMaterial.js';

/**
 * The landing page's hero: a small airspace that runs by itself. Normal calls glide between
 * people, agents, tools and models; every few seconds an attack chain crosses it and its last
 * leg is cut off mid-air in red, the way the console shows it. Decorative only.
 */

type Role = 'origin' | 'agent' | 'tool' | 'model';
interface HeroNode {
  id: string;
  role: Role;
  pos: THREE.Vector3;
  mesh: THREE.Mesh;
  glow: THREE.Sprite;
  flash: number;
}
interface Flight {
  curve: THREE.QuadraticBezierCurve3;
  line: Line2;
  head: THREE.Sprite;
  start: number;
  duration: number;
  /** Where the flight stops (1 = it lands). */
  stopAt: number;
  red: boolean;
  /** For an attack: the next leg, launched when this one lands. */
  next: (() => void) | null;
  done: boolean;
  fadeFrom: number;
}

const DARK = {
  allowed: 0x38e1ff,
  blocked: 0xff3b5c,
  grid: 0x16304a,
  gridMinor: 0x0c1a2b,
  fog: 0x060a13,
  pad: 0x0f1a2b,
  ambient: 0x8090c0,
  ambientIntensity: 1.4,
  blend: THREE.AdditiveBlending as THREE.Blending,
  role: { origin: 0xb9a7ff, agent: 0x5eead4, tool: 0xcbd5e1, model: 0x9fb0ff } as Record<Role, number>,
};
const LIGHT: typeof DARK = {
  allowed: 0x0284c7,
  blocked: 0xdc2626,
  grid: 0xbfc9d6,
  gridMinor: 0xe2e7ee,
  fog: 0xf5f7fa,
  pad: 0xe2e8f0,
  ambient: 0xffffff,
  ambientIntensity: 2.2,
  blend: THREE.NormalBlending,
  role: { origin: 0x7c3aed, agent: 0x0d9488, tool: 0x64748b, model: 0x4f46e5 },
};
let C = DARK;

const NODES: { id: string; role: Role; x: number; z: number }[] = [
  { id: 'customer-chat', role: 'origin', x: -30, z: 10 },
  { id: 'priya', role: 'origin', x: -30, z: -8 },
  { id: 'copilot', role: 'origin', x: -30, z: -24 },
  { id: 'support', role: 'agent', x: -10, z: 14 },
  { id: 'billing', role: 'agent', x: -10, z: -6 },
  { id: 'research', role: 'agent', x: -10, z: -24 },
  { id: 'billing.refund', role: 'tool', x: 10, z: -2 },
  { id: 'email.send', role: 'tool', x: 10, z: 14 },
  { id: 'orders.lookup', role: 'tool', x: 10, z: -18 },
  { id: 'web.fetch', role: 'tool', x: 10, z: 28 },
  { id: 'standard-model', role: 'model', x: 30, z: 4 },
  { id: 'premium-model', role: 'model', x: 30, z: -16 },
];

/** Ordinary work: pairs that call each other. */
const NORMAL: [string, string][] = [
  ['priya', 'support'],
  ['priya', 'billing'],
  ['support', 'orders.lookup'],
  ['support', 'standard-model'],
  ['billing', 'billing.refund'],
  ['research', 'web.fetch'],
  ['research', 'standard-model'],
  ['customer-chat', 'support'],
  ['support', 'email.send'],
  ['copilot', 'premium-model'],
  ['billing', 'standard-model'],
  ['priya', 'research'],
];

/** The attack: the outside chat → support → billing → refund, cut off on the last leg. */
const ATTACK = ['customer-chat', 'support', 'billing', 'billing.refund'];

function glowTexture(): THREE.Texture {
  const c = document.createElement('canvas');
  c.width = c.height = 64;
  const g = c.getContext('2d')!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, 'rgba(255,255,255,1)');
  grad.addColorStop(0.25, 'rgba(255,255,255,0.7)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

export interface HeroOptions {
  reducedMotion: boolean;
  theme?: 'dark' | 'light';
  /** Called when the attack is cut off, with the point on screen (0..1) so the page can label it. */
  onCut?: (x: number, y: number) => void;
}

export class Hero {
  private renderer: THREE.WebGLRenderer;
  private scene = new THREE.Scene();
  private camera = new THREE.PerspectiveCamera(38, 1, 0.1, 500);
  private nodes = new Map<string, HeroNode>();
  private flights: Flight[] = [];
  private glow = glowTexture();
  private frame = 0;
  private last = 0;
  private nextNormal = 0;
  private nextAttack = 1.6;
  private pointer = new THREE.Vector2();
  private observer: ResizeObserver;
  private radar: THREE.Mesh;
  private gone = false;
  private labels = new CSS2DRenderer();
  private resolution = new THREE.Vector2(1, 1);

  constructor(
    private host: HTMLElement,
    private opts: HeroOptions,
  ) {
    C = opts.theme === 'light' ? LIGHT : DARK;
    this.renderer = new THREE.WebGLRenderer({ antialias: true, alpha: true, powerPreference: 'high-performance' });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.setClearColor(0x000000, 0);
    this.renderer.domElement.setAttribute('aria-hidden', 'true');
    host.appendChild(this.renderer.domElement);
    this.labels.domElement.className = 'hero-labels';
    host.appendChild(this.labels.domElement);

    this.scene.fog = new THREE.FogExp2(C.fog, opts.theme === "light" ? 0.0035 : 0.0052);
    this.scene.add(new THREE.AmbientLight(C.ambient, C.ambientIntensity));
    const sun = new THREE.DirectionalLight(0xffffff, 1.5);
    sun.position.set(20, 60, 30);
    this.scene.add(sun);

    const grid = new THREE.GridHelper(220, 44, C.grid, C.gridMinor);
    (grid.material as THREE.Material).transparent = true;
    (grid.material as THREE.Material).opacity = 0.5;
    this.scene.add(grid);
    for (const r of [22, 44, 66]) {
      const ring = new THREE.Mesh(
        new THREE.RingGeometry(r - 0.08, r + 0.08, 128),
        new THREE.MeshBasicMaterial({ color: C.grid, transparent: true, opacity: 0.7, side: THREE.DoubleSide }),
      );
      ring.rotation.x = -Math.PI / 2;
      ring.position.y = 0.02;
      this.scene.add(ring);
    }
    this.radar = new THREE.Mesh(
      new THREE.CircleGeometry(66, 48, 0, 0.6),
      new THREE.MeshBasicMaterial({ color: C.allowed, transparent: true, opacity: opts.theme === 'light' ? 0.03 : 0.05, side: THREE.DoubleSide, depthWrite: false }),
    );
    this.radar.rotation.x = -Math.PI / 2;
    this.radar.position.y = 0.03;
    this.scene.add(this.radar);

    for (const n of NODES) this.addNode(n.id, n.role, n.x, n.z);

    window.addEventListener('pointermove', this.onPointer);
    this.observer = new ResizeObserver(() => this.resize());
    this.observer.observe(host);
    this.resize();
    if (opts.reducedMotion) {
      // One still frame, with a few calls in the air and the attack already stopped.
      for (const [a, b] of NORMAL.slice(0, 5)) this.launch(a, b, false, 1, null, -0.8);
      this.attack(-10);
      this.update(0);
      this.draw();
    } else {
      this.loop(0);
    }
  }

  private addNode(id: string, role: Role, x: number, z: number) {
    const color = C.role[role];
    const pad = new THREE.Mesh(
      new THREE.CylinderGeometry(2.2, 2.5, 0.35, 6),
      new THREE.MeshStandardMaterial({ color: C.pad, emissive: color, emissiveIntensity: 0.15, metalness: 0.4, roughness: 0.5 }),
    );
    pad.position.set(x, 0.18, z);
    this.scene.add(pad);
    const geo =
      role === 'origin'
        ? new THREE.OctahedronGeometry(1.5)
        : role === 'agent'
          ? new THREE.CylinderGeometry(1.4, 1.4, 2.2, 6)
          : role === 'tool'
            ? new THREE.BoxGeometry(2, 1.6, 2)
            : new THREE.IcosahedronGeometry(1.5);
    const mesh = new THREE.Mesh(
      geo,
      new THREE.MeshStandardMaterial({ color, emissive: color, emissiveIntensity: 0.5, metalness: 0.2, roughness: 0.35, flatShading: true }),
    );
    mesh.position.set(x, 2.4, z);
    this.scene.add(mesh);
    const glow = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.glow, color, transparent: true, opacity: 0.35, blending: C.blend, depthWrite: false }));
    glow.scale.setScalar(7);
    glow.position.copy(mesh.position);
    this.scene.add(glow);
    const tag = document.createElement('div');
    tag.className = `hero-label${ATTACK.includes(id) ? ' chain' : ''}${id === 'customer-chat' ? ' outside' : ''}`;
    tag.textContent = id;
    const label = new CSS2DObject(tag);
    label.position.set(x, -0.6, z + 3.4);
    this.scene.add(label);
    this.nodes.set(id, { id, role, pos: mesh.position.clone(), mesh, glow, flash: 0 });
  }

  private launch(from: string, to: string, red: boolean, stopAt: number, next: (() => void) | null, age = 0) {
    const a = this.nodes.get(from)!.pos;
    const b = this.nodes.get(to)!.pos;
    const mid = a.clone().lerp(b, 0.5);
    mid.y += 5 + a.distanceTo(b) * 0.28;
    const curve = new THREE.QuadraticBezierCurve3(a.clone(), mid, b.clone());
    const pts = curve.getPoints(48);
    const geo = new LineGeometry();
    geo.setPositions(pts.flatMap((q) => [q.x, q.y, q.z]));
    geo.instanceCount = 0;
    const mat = new LineMaterial({ color: red ? C.blocked : C.allowed, linewidth: red ? 3.5 : 2.2, transparent: true, opacity: 0.85, depthWrite: false });
    mat.resolution.copy(this.resolution);
    const line = new Line2(geo, mat);
    this.scene.add(line);
    const head = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.glow, color: red ? C.blocked : C.allowed, transparent: true, blending: C.blend, depthWrite: false }));
    head.scale.setScalar(red ? 3.6 : 2.8);
    this.scene.add(head);
    this.flights.push({ curve, line, head, start: this.last + age, duration: 1.3, stopAt, red, next, done: false, fadeFrom: 0 });
  }

  /** Customer chat → support → billing → refund; the last leg stops halfway, in red. */
  private attack(age = 0) {
    const legs = ATTACK.slice(1).map((to, i) => [ATTACK[i]!, to] as const);
    const fly = (i: number, a: number) => {
      const [from, to] = legs[i]!;
      const last = i === legs.length - 1;
      this.launch(from, to, last, last ? 0.55 : 1, last ? null : () => fly(i + 1, 0), a);
    };
    fly(0, age);
    if (age < 0) {
      // For the still frame: draw every leg at once.
      fly(1, age);
      fly(2, age);
    }
  }

  private onPointer = (e: PointerEvent) => {
    this.pointer.set((e.clientX / window.innerWidth) * 2 - 1, (e.clientY / window.innerHeight) * 2 - 1);
  };

  private resize() {
    const w = Math.max(1, this.host.clientWidth);
    const h = Math.max(1, this.host.clientHeight);
    this.renderer.setSize(w, h, false);
    this.labels.setSize(w, h);
    this.resolution.set(w, h);
    for (const f of this.flights) f.line.material.resolution.copy(this.resolution);
    this.renderer.domElement.style.width = '100%';
    this.renderer.domElement.style.height = '100%';
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    if (this.opts.reducedMotion) this.draw();
  }

  private update(t: number) {
    // A slow orbit, nudged by the pointer.
    const ang = 0.3 + (this.opts.reducedMotion ? 0 : Math.sin(t * 0.09) * 0.32) + this.pointer.x * 0.1;
    const r = 96;
    this.camera.position.set(Math.sin(ang) * r * 0.55 - 4, 64 - this.pointer.y * 5, Math.cos(ang) * r);
    this.camera.lookAt(-6, 0, 4);
    this.radar.rotation.z = -t * 0.6;

    for (const n of this.nodes.values()) {
      n.mesh.rotation.y = t * 0.4;
      n.flash = Math.max(0, n.flash - 0.02);
      const m = n.mesh.material as THREE.MeshStandardMaterial;
      if (n.flash > 0) m.emissive.setHex(C.blocked);
      else m.emissive.setHex(C.role[n.role]);
      m.emissiveIntensity = 0.5 + n.flash * 1.5;
    }

    for (const f of this.flights) {
      const p = Math.min(1, (t - f.start) / f.duration);
      if (p < 0) continue;
      const reach = Math.min(p, f.stopAt);
      f.line.geometry.instanceCount = Math.max(1, Math.floor(reach * 48));
      f.head.position.copy(f.curve.getPoint(reach));
      if (!f.done && p >= f.stopAt) {
        f.done = true;
        f.fadeFrom = t;
        if (f.red) {
          // Cut off: a red flare, the target agent flashes, the page shows the rule.
          f.head.scale.setScalar(9);
          this.nodes.get('billing')!.flash = 1;
          const v = f.head.position.clone().project(this.camera);
          this.opts.onCut?.((v.x + 1) / 2, (1 - v.y) / 2);
        }
        f.next?.();
      }
      if (f.done) {
        const age = t - f.fadeFrom;
        const life = f.red ? 3.2 : 1.6;
        const a = Math.max(0, 1 - age / life);
        f.line.material.opacity = 0.85 * a;
        (f.head.material as THREE.SpriteMaterial).opacity = f.red ? a : a * 0.6;
        if (f.red) f.head.scale.setScalar(4 + 5 * a);
      }
    }
    // Clean up finished flights.
    this.flights = this.flights.filter((f) => {
      const dead = f.done && t - f.fadeFrom > (f.red ? 3.2 : 1.6) && !this.opts.reducedMotion;
      if (dead) {
        this.scene.remove(f.line, f.head);
        f.line.geometry.dispose();
        f.line.material.dispose();
        (f.head.material as THREE.Material).dispose();
      }
      return !dead;
    });

    if (t >= this.nextNormal) {
      const [a, b] = NORMAL[Math.floor(Math.random() * NORMAL.length)]!;
      this.launch(a, b, false, 1, null);
      this.nextNormal = t + 0.16 + Math.random() * 0.24;
    }
    if (t >= this.nextAttack) {
      this.attack();
      this.nextAttack = t + 7.5;
    }
  }

  private loop = (ms: number) => {
    if (this.gone) return;
    this.last = ms / 1000;
    this.update(this.last);
    this.draw();
    this.frame = requestAnimationFrame(this.loop);
  };

  private draw() {
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
  }

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
