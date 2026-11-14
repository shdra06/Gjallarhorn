import type { NodeInfo, NodeRole } from './flights';

export interface Pos {
  x: number;
  z: number;
}

/** Columns from left to right follow a chain: who started it, the agents, what they used. */
export const columns: Record<NodeRole, { x: number; title: string }> = {
  origin: { x: -31, title: 'People and apps' },
  agent: { x: -10, title: 'Agents' },
  tool: { x: 11, title: 'Tools' },
  model: { x: 31, title: 'Models' },
};

const spacing: Record<NodeRole, number> = { origin: 11, agent: 12, tool: 9, model: 11 };

function order(a: NodeInfo, b: NodeInfo): number {
  // Tools of one family sit together; otherwise keep a stable alphabetical order.
  return a.id.localeCompare(b.id);
}

/**
 * Places nodes in four gently curved columns. The curve keeps the scene from looking like a
 * spreadsheet and gives the camera depth.
 */
export function layout(nodes: Iterable<NodeInfo>): Map<string, Pos> {
  const byRole = new Map<NodeRole, NodeInfo[]>();
  for (const n of nodes) {
    const list = byRole.get(n.role) ?? [];
    list.push(n);
    byRole.set(n.role, list);
  }
  const out = new Map<string, Pos>();
  for (const [role, list] of byRole) {
    list.sort(order);
    const gap = spacing[role];
    const span = (list.length - 1) * gap;
    list.forEach((n, i) => {
      const z = -span / 2 + i * gap;
      const x = columns[role].x - 0.008 * z * z;
      out.set(n.id, { x, z });
    });
  }
  return out;
}

/** The z extent of the layout, used to frame the camera. */
export function extent(pos: Map<string, Pos>): number {
  let m = 10;
  for (const p of pos.values()) m = Math.max(m, Math.abs(p.z));
  return m;
}
