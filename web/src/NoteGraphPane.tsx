// The map of the notes: the notes as glowing points joined by their links, in 3D where the browser
// can and flat where it cannot.
import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "preact/hooks";
import { plural, t, tf } from "./i18n";
import { choose } from "./choose";
import { useApp } from "./appStore";
import { MapLegend, MapPeek } from "./NoteMapPanels";
import { SMALL_BTN } from "./NotesLibrary";
import {
  graph3d,
  mountGraph3D,
  NEON_CSS,
  reducedMotion,
  type Graph3D,
} from "./noteGraph3d";
import {
  layoutGraph,
  noteColours,
  type NoteGraph,
  type NoteView,
} from "./notesApi";

const FIELD =
  "w-full min-h-[40px] px-3 rounded-lg bg-black/30 border border-white/10 text-sm text-[#e8e8ea] placeholder:text-[#7a7a8c]";

// DOT sizes a note on the flat map: its radius grows with the links to it, up to most, and its
// name sits lift above it.
const DOT = { size: 5, most: 6, lift: 10 };

// GRAPH_W and GRAPH_H are the box the graph is laid out in; the picture scales to the window.
const GRAPH_W = 640;
const GRAPH_H = 440;

// useMap3D draws the graph in 3D into host where WebGL works, and answers whether it did; until
// then, or anywhere else, the flat drawing stays.
function useMap3D(graph: NoteGraph, open: (path: string) => void) {
  const host = useRef<HTMLDivElement>(null);
  const map = useRef<Graph3D | null>(null);
  const [deep, setDeep] = useState(false);
  useEffect(() => {
    if (graph.nodes.length === 0 || !graph3d.webgl()) {
      return undefined;
    }
    let live = true;
    graph3d
      .load()
      .then((T) => {
        if (live) {
          map.current = mountGraph3D(
            T,
            host.current as HTMLDivElement,
            graph,
            open,
            reducedMotion(),
          );
          setDeep(true);
        }
      })
      .catch(() => setDeep(false));
    return () => {
      live = false;
      map.current?.stop();
      map.current = null;
    };
  }, [graph]);
  return { host, map, deep };
}

// GraphPane draws the notes as glowing points joined by their links, in 3D where it can; a click
// on a note shows it beside the map, and from there it opens.
export function GraphPane({ graph }: { graph: NoteGraph }) {
  const { openNote, peekNote, closeGraph } = useApp().notes;
  const nodes = useMemo(() => layoutGraph(graph, GRAPH_W, GRAPH_H), [graph]);
  const at = new Map(nodes.map((n) => [n.path, n]));
  const colours = useMemo(
    () => noteColours(graph.nodes, NEON_CSS.length),
    [graph],
  );
  const [peek, setPeek] = useState<NoteView | null>(null);
  const show = useCallback(
    (path: string) => void peekNote(path).then(setPeek),
    [peekNote],
  );
  const leaveFor = useCallback(
    (path: string) => void openNote(path),
    [openNote],
  );
  const unpeek = useCallback(() => setPeek(null), []);
  const { host, map, deep } = useMap3D(graph, show);
  const [query, setQuery] = useState("");
  const q = query.trim().toLowerCase();
  const hit = (title: string) => q !== "" && title.toLowerCase().includes(q);
  const found = graph.nodes.filter((n) => hit(n.title)).length;
  useEffect(() => {
    map.current?.find(query);
  }, [query, deep]);
  return (
    <div class="flex flex-col flex-1 min-h-0" data-testid="note-graph">
      <div class="flex items-center gap-2 mb-2 flex-wrap">
        <button
          class={SMALL_BTN + " md:hidden"}
          data-testid="graph-back"
          onClick={closeGraph}
        >
          {t("Back to the list")}
        </button>
        <div class="flex-1 min-w-0 text-xs text-[#9a9aaa]">
          {tf(
            "{notes} notes · {links} links · {missing} to notes not written yet",
            {
              notes: String(graph.nodes.length),
              links: String(graph.edges.length),
              missing: String(graph.missing),
            },
          )}
        </div>
        {deep && (
          <div class="text-xs text-accent" data-testid="graph-3d-hint">
            {t("Drag to turn, scroll to zoom, click a name to look at it.")}
          </div>
        )}
      </div>
      {nodes.length > 0 && (
        <div class="flex items-center gap-2 mb-2">
          <input
            type="search"
            class={FIELD + " md:max-w-[18rem]"}
            value={query}
            placeholder={t("Find a note on the map")}
            aria-label={t("Find a note on the map")}
            data-testid="graph-search"
            onInput={(e) => setQuery((e.target as HTMLInputElement).value)}
          />
          {q !== "" && (
            <span
              class="text-xs text-[#f5f500] whitespace-nowrap"
              data-testid="graph-found"
            >
              {plural(found, "1 found", "{n} found")}
            </span>
          )}
        </div>
      )}
      {nodes.length === 0 && (
        <p class="text-sm text-[#9a9aaa]">{t("Nothing to draw yet.")}</p>
      )}
      <div class="graph3d relative flex-1 min-h-0 rounded-xl overflow-hidden">
        <div ref={host} class="absolute inset-0" data-testid="graph-3d" />
        {nodes.length > 0 && <MapLegend nodes={graph.nodes} />}
        {peek && (
          <MapPeek
            note={peek}
            onPeek={show}
            onOpen={leaveFor}
            onClose={unpeek}
          />
        )}
        {!deep && (
          <svg
            viewBox={`0 0 ${GRAPH_W} ${GRAPH_H}`}
            class="relative w-full h-full"
            role="img"
            aria-label={t("Graph of linked notes")}
          >
            {graph.edges.map((e) => (
              <line
                key={e.from + ">" + e.to}
                x1={at.get(e.from)?.x}
                y1={at.get(e.from)?.y}
                x2={at.get(e.to)?.x}
                y2={at.get(e.to)?.y}
                stroke="#00f0ff"
                stroke-opacity="0.4"
              />
            ))}
            {nodes.map((n) => (
              <g
                key={n.path}
                class={
                  "cursor-pointer graph2d-node" +
                  choose(hit(n.title), " is-hit", "") +
                  choose(q !== "" && !hit(n.title), " is-dim", "")
                }
                role="button"
                tabIndex={0}
                data-testid="graph-node"
                aria-label={n.title}
                onClick={() => show(n.path)}
                onKeyDown={(e) => e.key === "Enter" && show(n.path)}
              >
                <circle
                  cx={n.x}
                  cy={n.y}
                  r={DOT.size + Math.min(n.in, DOT.most)}
                  fill={NEON_CSS[colours.get(n.path) as number]}
                />
                <text
                  x={n.x}
                  y={n.y - DOT.lift - Math.min(n.in, DOT.most)}
                  text-anchor="middle"
                  font-size="12"
                  fill="#e8e8ea"
                >
                  {n.title}
                </text>
              </g>
            ))}
          </svg>
        )}
      </div>
    </div>
  );
}
