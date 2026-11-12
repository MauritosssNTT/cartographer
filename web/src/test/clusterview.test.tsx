import { act, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import type { ClusterGraph, GraphSnapshot } from "../api/types";
import { clusterWeight, edgeStrokes, GraphView, type ClusterView } from "../components/GraphView";
import { Legend } from "../components/Legend";
import { snapshotCommunities } from "../lib/communities";
import { graph, json, overview, stubApi } from "./fixtures";
import { sceneStub } from "./sceneStub";

const NONE = new Set<string>();
const clusters = [
  { id: "c:infra/a", label: "Alpha", collection: "infra", size: 3400, slot: 1, internal_edges: 9000 },
  { id: "c:~tail", label: "Other", size: 900, slot: 0, internal_edges: 40, communities: 300 },
];
const members: GraphSnapshot = { ...graph, nodes: graph.nodes.slice(0, 2), edges: graph.edges.slice(0, 1) };

function view(over: Partial<ClusterView> = {}): ClusterView {
  return {
    clusters,
    edges: [{ source: "c:infra/a", target: "c:~tail", weight: 40 }],
    unfolded: [],
    totalNodes: 30_000,
    onUnfold: vi.fn(),
    onFold: vi.fn(),
    ...over,
  };
}

function props(snapshot: GraphSnapshot, extra: Record<string, unknown> = {}) {
  return {
    snapshot,
    layoutKey: "kb-a\u0000",
    kbName: "kb-a",
    communities: snapshotCommunities(snapshot),
    colorBy: "community" as const,
    selected: null,
    hiddenIds: NONE,
    themeKey: "dark",
    live: false,
    occludedRight: 0,
    onSelect: () => {},
    ...extra,
  };
}

const lastData = () => sceneStub.calls.data.at(-1)!;

describe("the cluster view", () => {
  beforeEach(() => sceneStub.reset());

  it("sizes clusters on a log scale and draws heavier edges with more strokes", () => {
    expect(clusterWeight(20_000, 20_000)).toBe(1);
    expect(clusterWeight(1, 20_000)).toBeGreaterThan(0.35);
    expect(clusterWeight(200, 20_000)).toBeLessThan(clusterWeight(2000, 20_000));
    expect([1, 3, 4, 31, 32, 9999].map(edgeStrokes)).toEqual([1, 1, 2, 2, 3, 3]);
  });

  it("draws every cluster beside the unfolded concepts, and a weighted edge as overlaid strokes", async () => {
    render(<GraphView {...props(members, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    expect([...lastData().positions.keys()]).toEqual(["infra/a", "infra/b", "c:infra/a", "c:~tail"]);
    // Weight 40: three strokes of the one edge.
    expect(lastData().links.filter((l) => l === "c:infra/a>c:~tail")).toHaveLength(3);
    expect(lastData().links).toContain("infra/a>infra/b");
    expect(screen.getByText("2 clusters, 30,000 concepts; 0 unfolded")).toBeInTheDocument();
  });

  it("unfolds a cluster on click, never selects it", async () => {
    const onSelect = vi.fn();
    const v = view();
    render(<GraphView {...props(members, { clusterView: v, onSelect })} />);
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    act(() => sceneStub.callbacks!.onSelect("c:infra/a"));
    expect(v.onUnfold).toHaveBeenCalledWith("c:infra/a");
    expect(onSelect).not.toHaveBeenCalled();
    act(() => sceneStub.callbacks!.onSelect("infra/a"));
    expect(onSelect).toHaveBeenCalledWith("infra/a");
  });

  it("grows a cluster's members out of where it stood", async () => {
    const { rerender } = render(<GraphView {...props({ ...members, nodes: [], edges: [] }, { clusterView: view() })} />);
    await waitFor(() => expect(sceneStub.calls.data.length).toBeGreaterThan(0));
    const at = lastData().positions.get("c:infra/a")!;
    const opened = view({
      clusters: clusters.slice(1),
      edges: [],
      unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 2, total: 3400 }],
    });
    rerender(<GraphView {...props(members, { clusterView: opened })} />);
    await waitFor(() => expect(lastData().positions.has("infra/a")).toBe(true));
    const p = lastData().positions.get("infra/a")!;
    expect(Math.hypot(p.x! - at.x!, p.y! - at.y!, p.z! - at.z!)).toBeLessThan(Math.hypot(at.x!, at.y!, at.z!) + 1);
  });

  it("says how much of an unfolded cluster is shown, and folds it from its chip or Escape", async () => {
    const v = view({ unfolded: [{ id: "c:infra/a", label: "Alpha", shown: 120, total: 3400 }] });
    render(<GraphView {...props(members, { clusterView: v })} />);
    const chips = screen.getByRole("list", { name: "Unfolded clusters" });
    expect(chips).toHaveTextContent("Showing 120 of 3,400");
    const fold = within(chips).getByRole("button", { name: "Fold Alpha" });
    await userEvent.setup().click(fold);
    expect(v.onFold).toHaveBeenCalledTimes(1);
    fold.focus();
    await userEvent.setup().keyboard("{Escape}");
    expect(v.onFold).toHaveBeenCalledTimes(2);
  });

  it("offers the level switch only when asked, and the replay is off in the cluster view", async () => {
    const onChange = vi.fn();
    const loadBirths = () => Promise.resolve({});
    const { rerender } = render(
      <GraphView {...props(members, { clusterView: view(), loadBirths, graphLevel: { mode: "clusters", onChange } })}>
        <Legend snapshot={members} communities={snapshotCommunities(members)} colorBy="community" onColorBy={() => {}} />
      </GraphView>,
    );
    const level = screen.getByRole("group", { name: "Graph level" });
    expect(within(level).getByRole("button", { name: "Clusters" })).toHaveAttribute("aria-pressed", "true");
    await userEvent.setup().click(within(level).getByRole("button", { name: "Nodes" }));
    expect(onChange).toHaveBeenCalledWith("nodes");
    expect(screen.getByRole("button", { name: "Replay growth" })).toBeDisabled();
    rerender(<GraphView {...props(members, { loadBirths })} />);
    expect(screen.queryByRole("group", { name: "Graph level" })).toBeNull();
  });

  it("lists the clusters in the legend, each a button that unfolds it", async () => {
    const onUnfold = vi.fn();
    render(
      <Legend
        snapshot={members}
        communities={snapshotCommunities(members)}
        colorBy="community"
        onColorBy={() => {}}
        clusters={clusters}
        onUnfold={onUnfold}
      />,
    );
    expect(screen.getByText(/2 clusters ·/)).toBeInTheDocument();
    await userEvent.setup().click(screen.getByRole("button", { name: "Unfold Alpha, 3,400 concepts" }));
    expect(onUnfold).toHaveBeenCalledWith("c:infra/a");
  });
});

describe("the cluster view in the Atlas", () => {
  beforeEach(() => {
    window.history.replaceState(null, "", "/ui/");
    localStorage.clear();
    sessionStorage.clear();
    sceneStub.reset();
  });
  afterEach(() => vi.unstubAllGlobals());

  const large = { ...overview, concepts: { ...overview.concepts, total: 30_000 } };
  const answer: ClusterGraph = {
    mode: "clusters",
    threshold: 2000,
    clusters,
    nodes: [],
    edges: [{ source: "c:infra/a", target: "c:~tail", weight: 5 }],
    unfolded: [],
    dropped_unfolds: [],
    communities: [],
    total_nodes: 30_000,
    total_edges: 90_000,
  };

  it("draws a fully folded KB, unfolds into the URL and keeps the Nodes choice there", async () => {
    stubApi({ "/overview": () => json(large), "/graph/clusters": () => json(answer) });
    render(<App />);
    await screen.findByTestId("graph-view");
    await waitFor(() => expect(sceneStub.callbacks).not.toBeNull());
    await waitFor(() => expect(lastData().positions.has("c:~tail")).toBe(true));
    act(() => sceneStub.callbacks!.onSelect("c:~tail"));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("unfold")).toBe("c:~tail"));
    const level = await screen.findByRole("group", { name: "Graph level" });
    await userEvent.setup().click(within(level).getByRole("button", { name: "Nodes" }));
    await waitFor(() => expect(new URLSearchParams(window.location.search).get("graph")).toBe("nodes"));
  });
});
