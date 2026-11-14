/** Whether this browser can draw the airspace. Kept apart from Airspace.ts so the check doesn't load Three.js. */
export function webglAvailable(): boolean {
  try {
    const c = document.createElement('canvas');
    return !!(c.getContext('webgl2') || c.getContext('webgl'));
  } catch {
    return false;
  }
}
