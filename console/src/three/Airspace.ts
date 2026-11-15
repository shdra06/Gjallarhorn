import * as THREE from 'three';
import { OrbitControls } from 'three/addons/controls/OrbitControls.js';
import { CSS2DObject, CSS2DRenderer } from 'three/addons/renderers/CSS2DRenderer.js';
import { Line2 } from 'three/addons/lines/Line2.js';
import { LineGeometry } from 'three/addons/lines/LineGeometry.js';
import { LineMaterial } from 'three/addons/lines/LineMaterial.js';
import type { Flight, FlightKind, NodeInfo, NodeRole } from '../model/flights';
import { columns, extent, layout, type Pos } from '../model/layout';

export const palette = {
  background: 0x060a13,
  grid: 0x16304a,
  gridMinor: 0x0c1a2b,
  allowed: 0x38e1ff,
  blocked: 0xff3b5c,
  flagged: 0xffb020,
  external: 0xff9f43,
  locked: 0xff3b5c,
  role: { origin: 0xb9a7ff, agent: 0x5eead4, tool: 0xcbd5e1, model: 0x9fb0ff } as Record<NodeRole, number>,
};

const kindColor: Record<FlightKind, number> = { allowed: palette.allowed, blocked: palette.blocked, flagged: palette.flagged };

export interface AirspaceOptions {
  onSelectNode: (id: string) => void;
  onSelectFlight: (seq: number) => void;
  reducedMotion: boolean;
}

interface NodeView {
  info: NodeInfo;
  group: THREE.Group;
  marker: THREE.Mesh;
  pad: THREE.Mesh;
  beam: THREE.Mesh;
  label: CSS2DObject;
  button: HTMLButtonElement;
  shield: THREE.Mesh | null;
  ring: THREE.Mesh | null;
  pulse: number;
  /** A red flash when this node's call was refused. */
  alarm: number;
  locked: boolean;
}

interface FlightView {
  flight: Flight;
  start: number;
  duration: number;
  curve: THREE.QuadraticBezierCurve3;
  trail: Line2;
  rest: Line2 | null;
  head: THREE.Sprite;
  segments: number;
  stopAt: number;
  arrived: boolean;
  life: number;
  marker: CSS2DObject | null;
  burst: THREE.Sprite | null;
  burstStart: number;
  /** A lasting glow where a blocked call was stopped. */
  flare: THREE.Sprite | null;
}

interface Corridor {
  line: THREE.Line;
  heat: number;
  red: number;
}

const SEGMENTS = 48;
const MAX_FLIGHTS = 160;

function glowTexture(): THREE.Texture {
  const c = document.createElement('canvas');
  c.width = c.height = 64;
  const g = c.getContext('2d')!;
  const grad = g.createRadialGradient(32, 32, 0, 32, 32, 32);
  grad.addColorStop(0, 'rgba(255,255,255,1)');
  grad.addColorStop(0.25, 'rgba(255,255,255,0.75)');
  grad.addColorStop(1, 'rgba(255,255,255,0)');
  g.fillStyle = grad;
  g.fillRect(0, 0, 64, 64);
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

function ringTexture(): THREE.Texture {
  const c = document.createElement('canvas');
  c.width = c.height = 128;
  const g = c.getContext('2d')!;
  g.strokeStyle = 'rgba(255,255,255,1)';
  g.lineWidth = 7;
  g.beginPath();
  g.arc(64, 64, 52, 0, Math.PI * 2);
  g.stroke();
  const t = new THREE.CanvasTexture(c);
  t.colorSpace = THREE.SRGBColorSpace;
  return t;
}

function markerGeometry(info: NodeInfo): THREE.BufferGeometry {
  switch (info.role) {
    case 'agent':
      return new THREE.OctahedronGeometry(1.5);
    case 'tool':
      return new THREE.BoxGeometry(1.7, 1.1, 1.7);
    case 'model':
      return new THREE.IcosahedronGeometry(1.3);
    default:
      return info.kind === 'human' ? new THREE.SphereGeometry(1.15, 24, 16) : new THREE.ConeGeometry(1.2, 2, 6);
  }
}

const roleTag: Record<string, string> = { human: 'person', service: 'app', unknown: 'unknown', agent: 'agent', tool: 'tool', model: 'model' };

/**
 * The live airspace: nodes are the people, apps, agents, tools and models; every call is a
 * flight between them. Imperative Three.js, driven by the React view through a small API.
 */
export class Airspace {
  private readonly container: HTMLElement;
  private readonly opts: AirspaceOptions;
  private readonly renderer: THREE.WebGLRenderer;
  private readonly labels: CSS2DRenderer;
  private readonly scene = new THREE.Scene();
  private readonly camera: THREE.PerspectiveCamera;
  private readonly controls: OrbitControls;
  private lastFrame = performance.now() / 1000;
  private readonly glow = glowTexture();
  private readonly ring = ringTexture();
  private readonly nodes = new Map<string, NodeView>();
  private readonly flights: FlightView[] = [];
  private readonly corridors = new Map<string, Corridor>();
  private readonly columnTitles: CSS2DObject[] = [];
  private readonly resolution = new THREE.Vector2(1, 1);
  private readonly raycaster = new THREE.Raycaster();
  private readonly pointer = new THREE.Vector2();
  private positions = new Map<string, Pos>();
  private radar: THREE.Mesh | null = null;
  private highlight: Set<string> | null = null;
  private highlightLine: Line2 | null = null;
  private frame = 0;
  private reduced: boolean;
  private userMoved = false;
  private readonly observer: ResizeObserver;
  private readonly baseTarget = new THREE.Vector3(0, 0, 0);
  private disposed = false;

  constructor(container: HTMLElement, opts: AirspaceOptions) {
    this.container = container;
    this.opts = opts;
    this.reduced = opts.reducedMotion;

    this.renderer = new THREE.WebGLRenderer({ antialias: true, powerPreference: 'high-performance' });
    this.renderer.setPixelRatio(Math.min(window.devicePixelRatio || 1, 2));
    this.renderer.toneMapping = THREE.ACESFilmicToneMapping;
    this.renderer.domElement.setAttribute('aria-hidden', 'true');
    this.renderer.domElement.className = 'airspace-canvas';
    container.appendChild(this.renderer.domElement);

    this.labels = new CSS2DRenderer();
    this.labels.domElement.className = 'airspace-labels';
    container.appendChild(this.labels.domElement);

    this.scene.background = new THREE.Color(palette.background);
    this.scene.fog = new THREE.FogExp2(palette.background, 0.0085);

    this.camera = new THREE.PerspectiveCamera(42, 1, 0.1, 600);
    this.camera.position.set(0, 48, 70);
    this.controls = new OrbitControls(this.camera, this.renderer.domElement);
    this.controls.enableDamping = true;
    this.controls.dampingFactor = 0.08;
    this.controls.maxPolarAngle = 1.32;
    this.controls.minDistance = 25;
    this.controls.maxDistance = 170;
    this.controls.addEventListener('start', () => (this.userMoved = true));

    this.scene.add(new THREE.AmbientLight(0x8090c0, 1.3));
    const sun = new THREE.DirectionalLight(0xffffff, 1.4);
    sun.position.set(20, 60, 30);
    this.scene.add(sun);

    this.buildGround();
    this.buildColumnTitles();

    this.renderer.domElement.addEventListener('click', this.onClick);
    this.observer = new ResizeObserver(() => this.resize());
    this.observer.observe(container);
    this.resize();
    this.loop();
  }

  private buildGround(): void {
    const grid = new THREE.GridHelper(260, 52, palette.grid, palette.gridMinor);
    const gm = grid.material as THREE.Material;
    gm.transparent = true;
    gm.opacity = 0.55;
    this.scene.add(grid);

    // Range rings and a radar sweep, as on an air-traffic screen.
    for (const r of [24, 48, 72]) {
      const ring = new THREE.Mesh(
        new THREE.RingGeometry(r - 0.08, r + 0.08, 128),
        new THREE.MeshBasicMaterial({ color: palette.grid, transparent: true, opacity: 0.6, side: THREE.DoubleSide }),
      );
      ring.rotation.x = -Math.PI / 2;
      ring.position.y = 0.02;
      this.scene.add(ring);
    }
    const sweep = new THREE.Mesh(
      new THREE.CircleGeometry(72, 48, 0, 0.55),
      new THREE.MeshBasicMaterial({ color: palette.allowed, transparent: true, opacity: 0.045, side: THREE.DoubleSide, depthWrite: false }),
    );
    sweep.rotation.x = -Math.PI / 2;
    sweep.position.y = 0.03;
    this.scene.add(sweep);
    this.radar = sweep;
  }

  private buildColumnTitles(): void {
    for (const role of Object.keys(columns) as NodeRole[]) {
      const el = document.createElement('div');
      el.className = 'column-title';
      el.textContent = columns[role].title;
      const obj = new CSS2DObject(el);
      obj.position.set(columns[role].x, 0, 0);
      this.scene.add(obj);
      this.columnTitles.push(obj);
    }
  }

  private now(): number {
    return performance.now() / 1000;
  }

  setReducedMotion(on: boolean): void {
    this.reduced = on;
  }

  /** Adds new nodes and updates lock state; existing nodes glide to their new places. */
  setNodes(infos: NodeInfo[], locked: Set<string>): void {
    for (const info of infos) {
      if (!this.nodes.has(info.id)) this.addNode(info);
      else this.nodes.get(info.id)!.info = info;
    }
    this.positions = layout(infos);
    const ext = extent(this.positions);
    for (const t of this.columnTitles) t.position.z = -ext - 7;
    for (const [id, v] of this.nodes) {
      this.setLocked(v, locked.has(id));
      const p = this.positions.get(id);
      if (p && v.group.position.lengthSq() === 0) v.group.position.set(p.x, 0, p.z);
    }
    for (const c of this.corridors.values()) {
      this.scene.remove(c.line);
      c.line.geometry.dispose();
    }
    this.corridors.clear();
    this.frameCamera(ext);
  }

  /**
   * A high, radar-like angle: columns read left to right, nodes spread top to bottom. The
   * distance is chosen so the whole scene fits between the side panels.
   */
  private frameCamera(ext: number): void {
    if (this.userMoved) return;
    const w = Math.max(1, this.container.clientWidth);
    const free = Math.max(0.45, (w - 2 * 320) / w);
    const tanV = Math.tan(THREE.MathUtils.degToRad(this.camera.fov / 2));
    const tanH = tanV * this.camera.aspect;
    const halfWidth = 40; // outer columns plus their labels
    const halfDepth = ext + 9;
    const d = Math.max(60, halfWidth / (tanH * free), halfDepth / (tanV * 0.8));
    this.camera.position.set(this.baseTarget.x, d * 0.87, this.baseTarget.z + d * 0.5);
    this.controls.target.copy(this.baseTarget);
  }

  resetView(): void {
    this.userMoved = false;
    this.frameCamera(extent(this.positions));
  }

  private addNode(info: NodeInfo): void {
    const color = palette.role[info.role];
    const group = new THREE.Group();
    const pad = new THREE.Mesh(
      new THREE.CylinderGeometry(2.3, 2.6, 0.35, 6),
      new THREE.MeshStandardMaterial({ color: 0x0f1a2b, emissive: color, emissiveIntensity: 0.12, metalness: 0.4, roughness: 0.5 }),
    );
    pad.position.y = 0.18;
    group.add(pad);

    const marker = new THREE.Mesh(
      markerGeometry(info),
      new THREE.MeshStandardMaterial({ color, emissive: color, emissiveIntensity: 0.5, metalness: 0.2, roughness: 0.35, flatShading: true }),
    );
    marker.position.y = 2.3;
    marker.userData.nodeId = info.id;
    group.add(marker);

    const beam = new THREE.Mesh(
      new THREE.CylinderGeometry(0.07, 0.07, 9, 8, 1, true),
      new THREE.MeshBasicMaterial({ color, transparent: true, opacity: 0.16, blending: THREE.AdditiveBlending, depthWrite: false }),
    );
    beam.position.y = 4.5;
    group.add(beam);

    let ring: THREE.Mesh | null = null;
    if (info.external) {
      ring = new THREE.Mesh(
        new THREE.TorusGeometry(3.1, 0.09, 8, 64),
        new THREE.MeshBasicMaterial({ color: palette.external, transparent: true, opacity: 0.85 }),
      );
      ring.rotation.x = Math.PI / 2;
      ring.position.y = 0.4;
      group.add(ring);
    }

    const button = document.createElement('button');
    button.type = 'button';
    button.className = `node-label role-${info.role}${info.external ? ' external' : ''}`;
    button.addEventListener('click', (ev) => {
      ev.stopPropagation();
      this.opts.onSelectNode(info.id);
    });
    button.addEventListener('pointerdown', (ev) => ev.stopPropagation());
    const label = new CSS2DObject(button);
    label.position.y = 5.2;
    group.add(label);

    const v: NodeView = { info, group, marker, pad, beam, label, button, shield: null, ring, pulse: 0, alarm: 0, locked: false };
    this.renderLabel(v);
    this.nodes.set(info.id, v);
    this.scene.add(group);
  }

  private renderLabel(v: NodeView): void {
    const tag = roleTag[v.info.kind] ?? v.info.kind;
    const extra = [v.locked ? 'locked' : '', v.info.external ? 'outside party' : ''].filter(Boolean).join(', ');
    v.button.replaceChildren();
    const name = document.createElement('span');
    name.className = 'name';
    name.textContent = (v.locked ? '🔒 ' : '') + v.info.label;
    const t = document.createElement('span');
    t.className = 'tag';
    t.textContent = v.locked ? 'LOCKED' : tag;
    v.button.append(name, t);
    v.button.classList.toggle('locked', v.locked);
    v.button.setAttribute('aria-label', `${v.info.id}, ${tag}${extra ? `, ${extra}` : ''}. Open details.`);
  }

  private setLocked(v: NodeView, locked: boolean): void {
    if (v.locked === locked) return;
    v.locked = locked;
    const mat = v.marker.material as THREE.MeshStandardMaterial;
    const color = locked ? palette.locked : palette.role[v.info.role];
    mat.color.setHex(color);
    mat.emissive.setHex(color);
    if (locked && !v.shield) {
      v.shield = new THREE.Mesh(
        new THREE.IcosahedronGeometry(3.4, 1),
        new THREE.MeshBasicMaterial({ color: palette.locked, wireframe: true, transparent: true, opacity: 0.55 }),
      );
      v.shield.position.y = 2.3;
      v.group.add(v.shield);
    } else if (!locked && v.shield) {
      v.group.remove(v.shield);
      v.shield.geometry.dispose();
      (v.shield.material as THREE.Material).dispose();
      v.shield = null;
    }
    this.renderLabel(v);
  }

  /** Highlights the nodes of one chain (or clears with null). */
  setHighlight(route: string[] | null): void {
    this.highlight = route && route.length ? new Set(route) : null;
    for (const [id, v] of this.nodes) v.button.classList.toggle('dim', !!this.highlight && !this.highlight.has(id));
    if (this.highlightLine) {
      this.scene.remove(this.highlightLine);
      this.highlightLine.geometry.dispose();
      (this.highlightLine.material as LineMaterial).dispose();
      this.highlightLine = null;
    }
    if (!route || route.length < 2) return;
    const pts: number[] = [];
    let prev: THREE.Vector3 | null = null;
    for (const id of route) {
      const p = this.nodePoint(id);
      if (!p) continue;
      if (prev) {
        const c = this.curve(prev, p, 0);
        for (const q of c.getPoints(24)) pts.push(q.x, q.y, q.z);
      }
      prev = p;
    }
    if (pts.length < 6) return;
    const g = new LineGeometry();
    g.setPositions(pts);
    const m = new LineMaterial({ color: 0xffffff, linewidth: 4, transparent: true, opacity: 0.55, depthWrite: false });
    m.resolution.copy(this.resolution);
    this.highlightLine = new Line2(g, m);
    this.scene.add(this.highlightLine);
  }

  /** Where a node is (or is gliding to), at marker height. */
  private nodePoint(id: string): THREE.Vector3 | null {
    const p = this.positions.get(id);
    if (p) return new THREE.Vector3(p.x, 2.3, p.z);
    const v = this.nodes.get(id);
    return v ? new THREE.Vector3(v.group.position.x, 2.3, v.group.position.z) : null;
  }

  /**
   * Draws the routes recorded traffic has used as faint "air corridors", so the airspace shows
   * its shape even when it is quiet. Busier routes glow more; recently blocked ones are red.
   */
  seedCorridors(flights: Flight[]): void {
    const seen = new Map<string, { f: Flight; n: number; red: boolean }>();
    const recent = Date.now() - 10 * 60_000;
    for (const f of flights) {
      const k = `${f.from}>${f.to}`;
      const s = seen.get(k) ?? { f, n: 0, red: false };
      s.n++;
      if (f.kind !== 'allowed' && f.time > recent) s.red = true;
      seen.set(k, s);
    }
    for (const { f, n, red } of seen.values()) {
      const a = this.nodePoint(f.from);
      const b = this.nodePoint(f.to);
      if (!a || !b || f.from === f.to) continue;
      this.bumpCorridor(f, a, b);
      const c = this.corridors.get(`${f.from}>${f.to}`)!;
      c.heat = Math.min(1, 0.15 + n * 0.06);
      c.red = red ? 0.8 : 0;
    }
  }

  private curve(a: THREE.Vector3, b: THREE.Vector3, jitter: number): THREE.QuadraticBezierCurve3 {
    const mid = a.clone().add(b).multiplyScalar(0.5);
    const dist = a.distanceTo(b);
    mid.y += 5 + dist * 0.22;
    mid.z += jitter;
    return new THREE.QuadraticBezierCurve3(a, mid, b);
  }

  private material(color: number, width: number, opacity: number, dashed = false): LineMaterial {
    const m = new LineMaterial({ color, linewidth: width, transparent: true, opacity, depthWrite: false, dashed, dashSize: 0.9, gapSize: 0.7 });
    m.resolution.copy(this.resolution);
    return m;
  }

  /** Draws one call. Blocked calls are cut off mid-air; their marker opens the incident. */
  addFlight(f: Flight): void {
    const a = this.nodePoint(f.from);
    const b = this.nodePoint(f.to);
    if (!a || !b || f.from === f.to) return;
    this.bumpCorridor(f, a, b);

    const curve = this.curve(a, b, ((f.seq % 5) - 2) * 0.6);
    const pts: number[] = [];
    for (const p of curve.getPoints(SEGMENTS)) pts.push(p.x, p.y, p.z);
    const color = kindColor[f.kind];
    const stopAt = f.kind === 'blocked' ? 0.6 : 1;

    const g = new LineGeometry();
    g.setPositions(pts);
    const trail = new Line2(g, this.material(color, f.kind === 'allowed' ? 2 : 4, 1));
    trail.geometry.instanceCount = this.reduced ? Math.round(SEGMENTS * stopAt) : 0;
    this.scene.add(trail);

    let rest: Line2 | null = null;
    if (f.kind === 'blocked') {
      const cut = Math.round(SEGMENTS * stopAt);
      const restPts = pts.slice(cut * 3);
      const rg = new LineGeometry();
      rg.setPositions(restPts);
      rest = new Line2(rg, this.material(color, 1.5, 0, true));
      rest.computeLineDistances();
      this.scene.add(rest);
    }

    const head = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.glow, color, transparent: true, blending: THREE.AdditiveBlending, depthWrite: false }));
    head.scale.setScalar(f.kind === 'allowed' ? 2.2 : 3);
    head.position.copy(a);
    head.visible = !this.reduced;
    this.scene.add(head);

    const view: FlightView = {
      flight: f,
      start: this.now(),
      duration: f.plane === 'a2a' ? 1.7 : 1.3,
      curve,
      trail,
      rest,
      head,
      segments: SEGMENTS,
      stopAt,
      arrived: false,
      life: f.kind === 'blocked' ? 12 : f.kind === 'flagged' ? 7 : 3,
      marker: null,
      burst: null,
      burstStart: 0,
      flare: null,
    };
    if (this.reduced) this.arrive(view);
    this.flights.push(view);
    while (this.flights.length > MAX_FLIGHTS) this.removeFlight(this.flights[0]!);
  }

  private bumpCorridor(f: Flight, a: THREE.Vector3, b: THREE.Vector3): void {
    const key = `${f.from}>${f.to}`;
    let c = this.corridors.get(key);
    if (!c) {
      const geo = new THREE.BufferGeometry().setFromPoints(this.curve(a, b, 0).getPoints(SEGMENTS));
      const line = new THREE.Line(geo, new THREE.LineBasicMaterial({ color: palette.allowed, transparent: true, opacity: 0.1, depthWrite: false }));
      this.scene.add(line);
      c = { line, heat: 0, red: 0 };
      this.corridors.set(key, c);
    }
    c.heat = Math.min(1, c.heat + 0.25);
    if (f.kind !== 'allowed') c.red = 1;
  }

  private arrive(v: FlightView): void {
    v.arrived = true;
    v.head.visible = false;
    const f = v.flight;
    const at = v.curve.getPoint(v.stopAt);
    if (f.kind === 'blocked' || f.kind === 'flagged') {
      if (!this.reduced) {
        const burst = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.ring, color: kindColor[f.kind], transparent: true, blending: THREE.AdditiveBlending, depthWrite: false }));
        burst.position.copy(at);
        burst.scale.setScalar(1);
        this.scene.add(burst);
        v.burst = burst;
        v.burstStart = this.now();
      }
      const flare = new THREE.Sprite(new THREE.SpriteMaterial({ map: this.glow, color: kindColor[f.kind], transparent: true, opacity: 0.9, blending: THREE.AdditiveBlending, depthWrite: false }));
      flare.position.copy(at);
      flare.scale.setScalar(f.kind === 'blocked' ? 5.5 : 4);
      this.scene.add(flare);
      v.flare = flare;
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = `intercept ${f.kind}`;
      btn.textContent = `${f.kind === 'blocked' ? '✕' : '⚠'} ${f.key}`;
      btn.setAttribute('aria-label', `${f.kind === 'blocked' ? 'Blocked' : 'Flagged'}: ${f.actor} to ${f.target}, by ${f.key}. Open the incident.`);
      btn.addEventListener('click', (ev) => {
        ev.stopPropagation();
        this.opts.onSelectFlight(f.seq);
      });
      btn.addEventListener('pointerdown', (ev) => ev.stopPropagation());
      const marker = new CSS2DObject(btn);
      marker.position.copy(at);
      marker.position.y += 1.6;
      this.scene.add(marker);
      v.marker = marker;
    }
    if (f.kind === 'blocked') {
      const actor = this.nodes.get(f.from);
      if (actor) actor.alarm = 1;
    } else {
      const target = this.nodes.get(f.to);
      if (target) target.pulse = 1;
    }
  }

  private removeFlight(v: FlightView): void {
    const i = this.flights.indexOf(v);
    if (i >= 0) this.flights.splice(i, 1);
    for (const obj of [v.trail, v.rest]) {
      if (!obj) continue;
      this.scene.remove(obj);
      obj.geometry.dispose();
      (obj.material as LineMaterial).dispose();
    }
    this.scene.remove(v.head);
    (v.head.material as THREE.Material).dispose();
    if (v.burst) {
      this.scene.remove(v.burst);
      (v.burst.material as THREE.Material).dispose();
    }
    if (v.marker) {
      this.scene.remove(v.marker);
      v.marker.element.remove();
    }
    if (v.flare) {
      this.scene.remove(v.flare);
      (v.flare.material as THREE.Material).dispose();
    }
  }

  /** Removes every flight, e.g. when switching between live and replay. */
  clearFlights(): void {
    while (this.flights.length) this.removeFlight(this.flights[0]!);
  }

  private onClick = (ev: MouseEvent): void => {
    const rect = this.renderer.domElement.getBoundingClientRect();
    this.pointer.set(((ev.clientX - rect.left) / rect.width) * 2 - 1, -((ev.clientY - rect.top) / rect.height) * 2 + 1);
    this.raycaster.setFromCamera(this.pointer, this.camera);
    const markers = [...this.nodes.values()].map((v) => v.marker);
    const hit = this.raycaster.intersectObjects(markers, false)[0];
    const id = hit?.object.userData.nodeId as string | undefined;
    if (id) this.opts.onSelectNode(id);
  };

  private resize(): void {
    const w = Math.max(1, this.container.clientWidth);
    const h = Math.max(1, this.container.clientHeight);
    this.camera.aspect = w / h;
    this.camera.updateProjectionMatrix();
    this.renderer.setSize(w, h);
    this.labels.setSize(w, h);
    this.resolution.set(w, h);
    for (const f of this.flights) {
      (f.trail.material as LineMaterial).resolution.set(w, h);
      if (f.rest) (f.rest.material as LineMaterial).resolution.set(w, h);
    }
    if (this.highlightLine) (this.highlightLine.material as LineMaterial).resolution.set(w, h);
    this.frameCamera(extent(this.positions));
  }

  private loop = (): void => {
    if (this.disposed) return;
    this.frame = requestAnimationFrame(this.loop);
    const t = this.now();
    const dt = Math.min(0.1, Math.max(0.001, t - this.lastFrame));
    this.lastFrame = t;

    // Nodes: positions glide, agents turn slowly, arrivals pulse.
    for (const [id, v] of this.nodes) {
      const p = this.positions.get(id);
      if (p) {
        v.group.position.x += (p.x - v.group.position.x) * 0.08;
        v.group.position.z += (p.z - v.group.position.z) * 0.08;
      }
      if (!this.reduced) {
        if (v.info.role === 'agent') v.marker.rotation.y += dt * 0.6;
        if (v.shield) v.shield.rotation.y -= dt * 0.4;
      }
      v.pulse = Math.max(0, v.pulse - dt * 1.8);
      v.alarm = Math.max(0, v.alarm - dt * 0.5);
      const mat = v.marker.material as THREE.MeshStandardMaterial;
      const base = v.locked ? palette.locked : palette.role[v.info.role];
      if (v.alarm > 0) mat.emissive.setHex(palette.blocked);
      else mat.emissive.setHex(base);
      const s = 1 + (this.reduced ? 0 : v.pulse * 0.35);
      v.marker.scale.setScalar(s);
      const bm = v.beam.material as THREE.MeshBasicMaterial;
      bm.opacity = 0.14 + v.pulse * 0.5;
      const dim = this.highlight && !this.highlight.has(id);
      (v.marker.material as THREE.MeshStandardMaterial).emissiveIntensity = dim ? 0.08 : 0.5 + v.pulse * 0.7;
    }

    // Corridors cool down; red ones fade back to cyan.
    for (const c of this.corridors.values()) {
      c.heat = Math.max(0.12, c.heat - dt * 0.01);
      c.red = Math.max(0, c.red - dt * 0.05);
      const m = c.line.material as THREE.LineBasicMaterial;
      m.opacity = 0.06 + c.heat * 0.22 + c.red * 0.2;
      m.color.setHex(c.red > 0.05 ? palette.blocked : palette.allowed);
    }

    for (const v of [...this.flights]) {
      const age = t - v.start;
      if (!v.arrived) {
        const k = Math.min(1, age / v.duration);
        const eased = 1 - Math.pow(1 - k, 2);
        const along = eased * v.stopAt;
        v.head.position.copy(v.curve.getPoint(along));
        v.trail.geometry.instanceCount = Math.max(1, Math.round(v.segments * along));
        if (k >= 1) this.arrive(v);
      }
      const since = age - (this.reduced ? 0 : v.duration);
      if (v.burst) {
        const b = t - v.burstStart;
        v.burst.scale.setScalar(1 + b * 9);
        (v.burst.material as THREE.SpriteMaterial).opacity = Math.max(0, 1 - b / 0.9);
        if (b > 0.9) {
          this.scene.remove(v.burst);
          (v.burst.material as THREE.Material).dispose();
          v.burst = null;
        }
      }
      if (v.rest) (v.rest.material as LineMaterial).opacity = v.arrived ? Math.max(0, 0.45 - since / v.life) : 0;
      if (v.arrived) {
        const fade = Math.max(0, 1 - Math.max(0, since - v.life * 0.4) / (v.life * 0.6));
        (v.trail.material as LineMaterial).opacity = fade;
        if (v.flare) {
          const pulse = this.reduced ? 1 : 0.8 + 0.2 * Math.sin(t * 6);
          (v.flare.material as THREE.SpriteMaterial).opacity = 0.9 * fade * pulse;
        }
        if (v.marker) v.marker.element.classList.toggle('fading', fade < 0.35);
        if (fade <= 0) this.removeFlight(v);
      }
    }

    if (this.radar && !this.reduced) this.radar.rotation.z -= dt * 0.35;
    if (!this.userMoved && !this.reduced) {
      // A slow drift keeps the scene alive without making anyone seasick.
      const dx = this.camera.position.x - this.baseTarget.x;
      const dz = this.camera.position.z - this.baseTarget.z;
      const r = Math.hypot(dx, dz);
      const angle = Math.sin(t * 0.05) * 0.1;
      this.camera.position.x = this.baseTarget.x + Math.sin(angle) * r;
      this.camera.position.z = this.baseTarget.z + Math.cos(angle) * r;
    }
    this.controls.update();
    this.renderer.render(this.scene, this.camera);
    this.labels.render(this.scene, this.camera);
  };

  dispose(): void {
    this.disposed = true;
    cancelAnimationFrame(this.frame);
    this.observer.disconnect();
    this.renderer.domElement.removeEventListener('click', this.onClick);
    this.clearFlights();
    this.controls.dispose();
    this.scene.traverse((o) => {
      const mesh = o as THREE.Mesh;
      if (mesh.geometry) mesh.geometry.dispose();
      const m = mesh.material as THREE.Material | THREE.Material[] | undefined;
      if (Array.isArray(m)) m.forEach((x) => x.dispose());
      else m?.dispose();
    });
    this.glow.dispose();
    this.ring.dispose();
    this.renderer.dispose();
    this.renderer.domElement.remove();
    this.labels.domElement.remove();
  }
}

