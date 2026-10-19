// The map of the notes in 3D: neon spheres joined by their links, floating over a glowing grid in
// a dark fog, turning slowly. Dragging turns it, the wheel brings it closer, and a label over each
// note opens it. three.js is loaded only when the map opens, and only where WebGL works; anywhere
// else the window keeps the flat drawing.
import type * as Three from "three";
import { choose } from "./choose";
import {
  layoutGraph3D,
  noteGroup,
  type NoteGraph,
  type PlacedNode3D,
} from "./notesApi";

type ThreeLib = typeof Three;

// The neon palette: a colour per top folder.
export const NEON = [
  0x00f0ff, 0xff2bd6, 0xf5f500, 0x7cff4f, 0x9d4dff, 0xff8a00,
];
const RADIUS = 120;
const CAMERA_DISTANCE = 330;
const NEAREST = 140;
const FARTHEST = 700;
const SPIN = 0.0016;
const STARS = 600;

// graph3d is how the window reaches WebGL and three.js; the tests replace them.
export const graph3d = {
  webgl(): boolean {
    try {
      const canvas = document.createElement("canvas");
      return !!(canvas.getContext("webgl2") ?? canvas.getContext("webgl"));
    } catch {
      return false;
    }
  },
  load: (): Promise<ThreeLib> => import("three"),
};

export interface Graph3D {
  stop(): void;
}

// starfield is a cloud of points far around the map, the same every time.
function starfield(T: ThreeLib): Three.Points {
  const pos: number[] = [];
  for (let i = 0; i < STARS; i++) {
    const a = i * 2.399963;
    const y = 1 - (2 * (i + 0.5)) / STARS;
    const r = Math.sqrt(1 - y * y) * 900;
    pos.push(Math.cos(a) * r, y * 900, Math.sin(a) * r);
  }
  const geo = new T.BufferGeometry();
  geo.setAttribute("position", new T.Float32BufferAttribute(pos, 3));
  return new T.Points(
    geo,
    new T.PointsMaterial({
      color: 0x6b5cff,
      size: 2,
      transparent: true,
      opacity: 0.7,
    }),
  );
}

// links draws every link as a line between the two notes it joins.
function links(
  T: ThreeLib,
  graph: NoteGraph,
  at: Map<string, PlacedNode3D>,
): Three.LineSegments {
  const pos: number[] = [];
  for (const e of graph.edges) {
    const a = at.get(e.from) as PlacedNode3D;
    const b = at.get(e.to) as PlacedNode3D;
    pos.push(a.x, a.y, a.z, b.x, b.y, b.z);
  }
  const geo = new T.BufferGeometry();
  geo.setAttribute("position", new T.Float32BufferAttribute(pos, 3));
  return new T.LineSegments(
    geo,
    new T.LineBasicMaterial({
      color: 0x00f0ff,
      transparent: true,
      opacity: 0.35,
      blending: T.AdditiveBlending,
    }),
  );
}

// orb is one note: a bright core and a soft halo around it, the bigger the more notes link to it.
function orb(T: ThreeLib, node: PlacedNode3D): Three.Group {
  const color = NEON[noteGroup(node.path, NEON.length)];
  const size = 2.2 + Math.min(node.in, 8) * 0.45;
  const core = new T.Mesh(
    new T.SphereGeometry(size, 20, 14),
    new T.MeshBasicMaterial({ color }),
  );
  const halo = new T.Mesh(
    new T.SphereGeometry(size * 2.4, 20, 14),
    new T.MeshBasicMaterial({
      color,
      transparent: true,
      opacity: 0.16,
      blending: T.AdditiveBlending,
      depthWrite: false,
    }),
  );
  const g = new T.Group();
  g.add(core, halo);
  g.position.set(node.x, node.y, node.z);
  return g;
}

// label is the button over a note that names it and opens it.
function label(
  node: PlacedNode3D,
  onOpen: (path: string) => void,
): HTMLButtonElement {
  const b = document.createElement("button");
  b.type = "button";
  b.className = "graph3d-label";
  b.textContent = node.title;
  b.setAttribute("aria-label", node.title);
  b.dataset.testid = "graph-node";
  b.addEventListener("click", () => onOpen(node.path));
  return b;
}

// mountGraph3D draws graph into host and keeps it moving until stop.
export function mountGraph3D(
  T: ThreeLib,
  host: HTMLElement,
  graph: NoteGraph,
  onOpen: (path: string) => void,
  reducedMotion: boolean,
): Graph3D {
  const renderer = new T.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.domElement.className = "graph3d-canvas";
  host.appendChild(renderer.domElement);

  const scene = new T.Scene();
  scene.fog = new T.FogExp2(0x07010f, 0.0016);
  const camera = new T.PerspectiveCamera(55, 1, 1, 3000);
  camera.position.set(0, 60, CAMERA_DISTANCE);
  camera.lookAt(0, 0, 0);

  const grid = new T.GridHelper(900, 36, 0xff2bd6, 0x2a0f4a);
  grid.position.y = -RADIUS * 1.3;
  scene.add(grid, starfield(T));

  const world = new T.Group();
  scene.add(world);
  const placed = layoutGraph3D(graph, RADIUS);
  const at = new Map(placed.map((p) => [p.path, p]));
  world.add(links(T, graph, at));
  const layer = document.createElement("div");
  layer.className = "graph3d-labels";
  host.appendChild(layer);
  const marks = placed.map((node) => {
    const o = orb(T, node);
    world.add(o);
    const b = label(node, onOpen);
    layer.appendChild(b);
    return { o, b };
  });

  const resize = () => {
    const w = host.clientWidth || 1;
    const h = host.clientHeight || 1;
    renderer.setSize(w, h, false);
    camera.aspect = w / h;
    camera.updateProjectionMatrix();
  };
  resize();
  window.addEventListener("resize", resize);

  // Dragging turns the map; the wheel moves the camera closer or farther.
  let drag: { x: number; y: number } | null = null;
  const down = (e: PointerEvent) => {
    drag = { x: e.clientX, y: e.clientY };
  };
  const move = (e: PointerEvent) => {
    if (!drag) {
      return;
    }
    world.rotation.y += (e.clientX - drag.x) * 0.008;
    world.rotation.x = Math.max(
      -1.2,
      Math.min(1.2, world.rotation.x + (e.clientY - drag.y) * 0.008),
    );
    drag = { x: e.clientX, y: e.clientY };
  };
  const up = () => {
    drag = null;
  };
  const wheel = (e: WheelEvent) => {
    e.preventDefault();
    const d = Math.max(
      NEAREST,
      Math.min(FARTHEST, camera.position.length() + e.deltaY * 0.4),
    );
    camera.position.setLength(d);
  };
  host.addEventListener("pointerdown", down);
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  host.addEventListener("wheel", wheel, { passive: false });

  const v = new T.Vector3();
  let frame = 0;
  let t = 0;
  const draw = () => {
    t += 1;
    if (!drag && !reducedMotion) {
      world.rotation.y += SPIN;
    }
    world.updateMatrixWorld();
    const w = host.clientWidth;
    const h = host.clientHeight;
    for (const [i, { o, b }] of marks.entries()) {
      o.scale.setScalar(1 + Math.sin(t * 0.05 + i) * 0.08);
      o.getWorldPosition(v);
      v.project(camera);
      b.style.visibility = choose(v.z > 1, "hidden", "visible");
      b.style.transform = `translate(${((v.x + 1) / 2) * w}px, ${((1 - v.y) / 2) * h}px) translate(-50%, -150%)`;
    }
    renderer.render(scene, camera);
    frame = requestAnimationFrame(draw);
  };
  draw();

  return {
    stop() {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", resize);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      host.removeEventListener("pointerdown", down);
      host.removeEventListener("wheel", wheel);
      scene.traverse((obj) => {
        const mesh = obj as Three.Mesh;
        mesh.geometry?.dispose();
        (mesh.material as Three.Material | undefined)?.dispose();
      });
      renderer.dispose();
      host.replaceChildren();
    },
  };
}

// reducedMotion says whether the person asked their system for less movement.
export function reducedMotion(): boolean {
  return !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}
