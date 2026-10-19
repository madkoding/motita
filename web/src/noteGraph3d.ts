// The map of the notes in 3D: neon spheres joined by their links, floating over a glowing grid in
// a dark fog, turning slowly. Dragging turns it, the wheel brings it closer, and a label over each
// note opens it. A search lights the notes it finds and turns the camera to the first. Where the
// bloom effect loads, everything glows. three.js is loaded only when the map opens, and only where
// WebGL works; anywhere else the window keeps the flat drawing.
import type {
  Camera,
  Group,
  LineSegments,
  Material,
  Mesh,
  Points,
  Scene,
  WebGLRenderer,
} from "three";
import type { EffectComposer } from "three/addons/postprocessing/EffectComposer.js";
import { choose } from "./choose";
import {
  GOLDEN_ANGLE,
  layoutGraph3D,
  noteGroup,
  type NoteGraph,
  type PlacedNode3D,
} from "./notesApi";

type ThreeLib = typeof import("three");

// Fx is the bloom: the post-processing passes of three.js, loaded apart from it.
export interface Fx {
  EffectComposer: typeof import("three/addons/postprocessing/EffectComposer.js").EffectComposer;
  RenderPass: typeof import("three/addons/postprocessing/RenderPass.js").RenderPass;
  UnrealBloomPass: typeof import("three/addons/postprocessing/UnrealBloomPass.js").UnrealBloomPass;
  OutputPass: typeof import("three/addons/postprocessing/OutputPass.js").OutputPass;
}

// The neon palette: a colour per top folder.
const CYAN = 0x00f0ff;
const MAGENTA = 0xff2bd6;
const YELLOW = 0xf5f500;
const LIME = 0x7cff4f;
const VIOLET = 0x9d4dff;
const ORANGE = 0xff8a00;
export const NEON = [CYAN, MAGENTA, YELLOW, LIME, VIOLET, ORANGE];
const FOG = { color: 0x07010f, density: 0.0011 };
const LENS = { fov: 55, near: 1, far: 3000, rise: 0.42 };
const GRID = {
  size: 1400,
  cells: 48,
  axis: MAGENTA,
  lines: 0x6a1fb0,
  drop: 1.3,
};
const GLOW = { resolution: 256, strength: 0.75, radius: 0.4, threshold: 0.12 };
// The stars lie on a sphere, spread by the golden angle.
const SKY = { radius: 900 };
const ORB = {
  size: 3.4,
  growth: 0.6,
  most: 8,
  halo: 2.4,
  segments: 20,
  rings: 14,
};
// How the map answers the hand and how it breathes: turn per pixel dragged, tilt limit, zoom per
// wheel step, how fast the camera glides, the size of a found note and the pulse of every note.
const MOTION = {
  turn: 0.008,
  tilt: 1.2,
  zoom: 0.4,
  glide: 0.08,
  found: 1.8,
  pulse: 0.08,
  beat: 0.05,
};
const RADIUS = 120;
const CAMERA_DISTANCE = 330;
const NEAREST = 140;
const FARTHEST = 700;
// How many notes, the most linked, keep a full label; the rest show a faint one until searched or
// pointed at.
const LABELS = 14;
const SPIN = 0.0016;
const STARS = 600;
// CROWD is how many notes fit the map at its smallest; past it the map grows.
const CROWD = 20;
// Each point takes three numbers: x, y and z.
const XYZ = 3;

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
  loadFx: async (): Promise<Fx> => {
    const [c, r, b, o] = await Promise.all([
      import("three/addons/postprocessing/EffectComposer.js"),
      import("three/addons/postprocessing/RenderPass.js"),
      import("three/addons/postprocessing/UnrealBloomPass.js"),
      import("three/addons/postprocessing/OutputPass.js"),
    ]);
    return {
      EffectComposer: c.EffectComposer,
      RenderPass: r.RenderPass,
      UnrealBloomPass: b.UnrealBloomPass,
      OutputPass: o.OutputPass,
    };
  },
};

export interface Graph3D {
  // find lights the notes whose title holds query, turns to the first and answers how many it
  // found; an empty query clears it.
  find(query: string): number;
  stop(): void;
}

// bloom builds the glow over the scene, or answers null without the passes.
function bloom(
  T: ThreeLib,
  fx: Fx | undefined,
  renderer: WebGLRenderer,
  scene: Scene,
  camera: Camera,
): EffectComposer | null {
  if (!fx) {
    return null;
  }
  const composer = new fx.EffectComposer(renderer);
  composer.addPass(new fx.RenderPass(scene, camera));
  composer.addPass(
    new fx.UnrealBloomPass(
      new T.Vector2(GLOW.resolution, GLOW.resolution),
      GLOW.strength,
      GLOW.radius,
      GLOW.threshold,
    ),
  );
  composer.addPass(new fx.OutputPass());
  return composer;
}

// starfield is a cloud of points far around the map, the same every time.
function starfield(T: ThreeLib): Points {
  const pos: number[] = [];
  for (let i = 0; i < STARS; i++) {
    const a = i * GOLDEN_ANGLE;
    const y = 1 - (2 * (i + 0.5)) / STARS;
    const r = Math.sqrt(1 - y * y) * SKY.radius;
    pos.push(Math.cos(a) * r, y * SKY.radius, Math.sin(a) * r);
  }
  const geo = new T.BufferGeometry();
  geo.setAttribute("position", new T.Float32BufferAttribute(pos, XYZ));
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
): LineSegments {
  const pos: number[] = [];
  for (const e of graph.edges) {
    const a = at.get(e.from) as PlacedNode3D;
    const b = at.get(e.to) as PlacedNode3D;
    pos.push(a.x, a.y, a.z, b.x, b.y, b.z);
  }
  const geo = new T.BufferGeometry();
  geo.setAttribute("position", new T.Float32BufferAttribute(pos, XYZ));
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
function orb(T: ThreeLib, node: PlacedNode3D): Group {
  const color = NEON[noteGroup(node.path, NEON.length)];
  const size = ORB.size + Math.min(node.in, ORB.most) * ORB.growth;
  const core = new T.Mesh(
    new T.SphereGeometry(size, ORB.segments, ORB.rings),
    new T.MeshBasicMaterial({ color }),
  );
  const halo = new T.Mesh(
    new T.SphereGeometry(size * ORB.halo, ORB.segments, ORB.rings),
    new T.MeshBasicMaterial({
      color,
      transparent: true,
      opacity: 0.09,
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
  still: boolean,
  fx?: Fx,
): Graph3D {
  const renderer = new T.WebGLRenderer({ antialias: true, alpha: true });
  renderer.setPixelRatio(Math.min(window.devicePixelRatio, 2));
  renderer.domElement.className = "graph3d-canvas";
  host.appendChild(renderer.domElement);

  const scene = new T.Scene();
  scene.fog = new T.FogExp2(FOG.color, FOG.density);
  const camera = new T.PerspectiveCamera(LENS.fov, 1, LENS.near, LENS.far);
  // A bigger vault spreads wider, and the camera stands farther back to see it.
  const scale = Math.max(1, Math.cbrt(graph.nodes.length / CROWD));
  camera.position.set(
    0,
    CAMERA_DISTANCE * scale * LENS.rise,
    CAMERA_DISTANCE * scale,
  );
  camera.lookAt(0, 0, 0);

  const composer = bloom(T, fx, renderer, scene, camera);

  const grid = new T.GridHelper(GRID.size, GRID.cells, GRID.axis, GRID.lines);
  grid.position.y = -RADIUS * scale * GRID.drop;
  scene.add(grid, starfield(T));

  const world = new T.Group();
  scene.add(world);
  const placed = layoutGraph3D(graph, RADIUS * scale);
  const ranked = new Set(
    [...placed].sort((a, b) => b.in - a.in).slice(0, LABELS),
  );
  const at = new Map(placed.map((p) => [p.path, p]));
  world.add(links(T, graph, at));
  const layer = document.createElement("div");
  layer.className = "graph3d-labels";
  host.appendChild(layer);
  const marks = placed.map((node) => {
    const o = orb(T, node);
    world.add(o);
    const b = label(node, onOpen);
    b.classList.toggle("is-minor", !ranked.has(node));
    layer.appendChild(b);
    return { o, b };
  });

  const resize = () => {
    const w = host.clientWidth || 1;
    const h = host.clientHeight || 1;
    renderer.setSize(w, h, false);
    composer?.setSize(w, h);
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
    world.rotation.y += (e.clientX - drag.x) * MOTION.turn;
    world.rotation.x = Math.max(
      -MOTION.tilt,
      Math.min(
        MOTION.tilt,
        world.rotation.x + (e.clientY - drag.y) * MOTION.turn,
      ),
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
      Math.min(
        FARTHEST * scale,
        camera.position.length() + e.deltaY * MOTION.zoom,
      ),
    );
    camera.position.setLength(d);
  };
  host.addEventListener("pointerdown", down);
  window.addEventListener("pointermove", move);
  window.addEventListener("pointerup", up);
  host.addEventListener("wheel", wheel, { passive: false });

  const v = new T.Vector3();
  // The camera looks at target, which glides to the note found, or back to the middle.
  const target = new T.Vector3();
  const aim = new T.Vector3();
  let focus: Group | null = null;
  let frame = 0;
  let t = 0;
  const draw = () => {
    t += 1;
    if (!drag && !still && !focus) {
      world.rotation.y += SPIN;
    }
    world.updateMatrixWorld();
    aim.set(0, 0, 0);
    focus?.getWorldPosition(aim);
    target.lerp(aim, MOTION.glide);
    camera.lookAt(target);
    const w = host.clientWidth;
    const h = host.clientHeight;
    for (const [i, { o, b }] of marks.entries()) {
      o.scale.setScalar(
        choose(o === focus, MOTION.found, 1) +
          Math.sin(t * MOTION.beat + i) * MOTION.pulse,
      );
      o.getWorldPosition(v);
      v.project(camera);
      b.style.visibility = choose(v.z > 1, "hidden", "visible");
      b.style.transform = `translate(${((v.x + 1) / 2) * w}px, ${((1 - v.y) / 2) * h}px) translate(-50%, -150%)`;
    }
    if (composer) {
      composer.render();
    } else {
      renderer.render(scene, camera);
    }
    frame = requestAnimationFrame(draw);
  };
  draw();

  return {
    find(query) {
      const q = query.trim().toLowerCase();
      focus = null;
      let found = 0;
      for (const [i, { o, b }] of marks.entries()) {
        const hit = q !== "" && placed[i].title.toLowerCase().includes(q);
        b.classList.toggle("is-hit", hit);
        b.classList.toggle("is-dim", q !== "" && !hit);
        if (hit) {
          found++;
          focus ??= o;
        }
      }
      return found;
    },
    stop() {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", resize);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      host.removeEventListener("pointerdown", down);
      host.removeEventListener("wheel", wheel);
      scene.traverse((obj) => {
        const mesh = obj as Mesh;
        mesh.geometry?.dispose();
        (mesh.material as Material | undefined)?.dispose();
      });
      composer?.dispose();
      renderer.dispose();
      host.replaceChildren();
    },
  };
}

// reducedMotion says whether the person asked their system for less movement.
export function reducedMotion(): boolean {
  return !!window.matchMedia?.("(prefers-reduced-motion: reduce)").matches;
}
