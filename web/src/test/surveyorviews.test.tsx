import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { App } from "../App";
import { componentRow, componentsAnswer, json, stubApi } from "./fixtures";

const SETTINGS = {
  version: "v1.2.3",
  server: [{ key: "git.sync", value: true, origin: "default", description: "Sync.", default: "true" }],
  kbs: [{ name: "kb-a", entries: [], yaml: "" }],
  server_yaml: "",
};
const work = { total: 0, open_concepts: 0, by_status: {}, by_map: {}, open_items: 0, entries: [] };
const run = {
  run_id: "r1",
  replica: "w-1",
  task: "doctor",
  kb: "kb-a",
  started_at: "2026-01-01T00:00:00Z",
  duration_ms: 10,
  outcome: "ok",
  tokens_spent: 42,
  received_at: new Date().toISOString(),
};

function open(panel: string) {
  window.history.replaceState(null, "", `/ui/?kb=kb-a&panel=${panel}`);
  localStorage.clear();
  localStorage.setItem("cartographer.panel.rail", "0");
  sessionStorage.clear();
}

afterEach(() => vi.unstubAllGlobals());

describe("Activity: Surveyor runs", () => {
  beforeEach(() => open("activity"));

  it("is absent with no runs", async () => {
    stubApi({ "/components": () => json(componentsAnswer(componentRow({}, [{}]))) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    expect(screen.queryByRole("heading", { name: /Surveyor runs/ })).not.toBeInTheDocument();
  });

  it("is absent when the read fails", async () => {
    stubApi({ "/components": () => json({ error: { code: "not_found", message: "no" } }, 404) });
    render(<App />);
    await screen.findByRole("region", { name: /activity/i });
    expect(screen.queryByRole("heading", { name: /Surveyor runs/ })).not.toBeInTheDocument();
  });

  it("lists a reported run of this KB and a replica's last run", async () => {
    const replica = { last_run: { at: new Date().toISOString(), units_done: 3, tokens_spent: 9, commits: 2 } };
    stubApi({ "/components": () => json(componentsAnswer(componentRow({ runs: [run, { ...run, run_id: "r2", kb: "kb-b" }] }, [replica]))) });
    render(<App />);
    const list = await screen.findByRole("list", { name: "Surveyor runs" });
    expect(within(list).getAllByRole("listitem")).toHaveLength(2);
    expect(within(list).getByText("42 tokens")).toBeInTheDocument();
    expect(within(list).getByText(/3 units, 2 commits/)).toBeInTheDocument();
  });
});

describe("Work: alarms", () => {
  beforeEach(() => open("work"));

  it("shows an expected-but-silent alarm with no live Surveyor", async () => {
    stubApi({ "/work": () => json(work), "/components": () => json(componentsAnswer(componentRow({ declared: true }))) });
    render(<App />);
    const alarms = await screen.findByRole("list", { name: "Surveyor alarms" });
    expect(within(alarms).getByText(/Surveyor team-a is expected and has not reported/)).toBeInTheDocument();
  });

  it("shows a budget-exhausted Surveyor once", async () => {
    stubApi({
      "/work": () => json(work),
      "/components": () => json(componentsAnswer(componentRow({}, [{ state: "budget_exhausted" }]))),
    });
    render(<App />);
    const alarms = await screen.findByRole("list", { name: "Surveyor alarms" });
    expect(within(alarms).getAllByRole("listitem")).toHaveLength(1);
    expect(within(alarms).getByText(/has spent its budget/)).toBeInTheDocument();
  });

  it("shows no alarm for a healthy Surveyor, and none of another KB's reported alarm", async () => {
    const other = { code: "x", kb: "kb-b", detail: "secret", origin: "reported" };
    stubApi({ "/work": () => json(work), "/components": () => json(componentsAnswer(componentRow({ alarms: [other] }, [{}]))) });
    render(<App />);
    await screen.findByRole("region", { name: "Surveyors and feeds" });
    expect(screen.queryByRole("list", { name: "Surveyor alarms" })).not.toBeInTheDocument();
    expect(screen.queryByText(/secret/)).not.toBeInTheDocument();
  });
});

describe("Settings: Sources", () => {
  beforeEach(() => open("settings"));

  it("counts the KB's Sources by status and lists a feed in error", async () => {
    const src = (id: string, s: string) => ({ id, ingest_status: s, cited_by: 0, cited_by_ids: [] });
    stubApi({
      "/settings": () => json(SETTINGS),
      "/sources": () => json({ sources: [src("a", "pending"), src("b", "pending"), src("c", "ingested"), src("d", "skipped")] }),
      "/feeds": () => json({ feeds: [{ id: "feeds/news", title: "News feed", feed_error: "HTTP 500" }], total: 1 }),
    });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: "Surveyor" }));
    const counts = await screen.findByRole("list", { name: "Sources by status" });
    expect(within(counts).getByText("pending").nextSibling).toHaveTextContent("2");
    expect(within(counts).getByText("ingested").nextSibling).toHaveTextContent("1");
    expect(await screen.findByText("HTTP 500")).toBeInTheDocument();
  });

  it("is absent when the read fails", async () => {
    stubApi({ "/settings": () => json(SETTINGS), "/sources": () => json({ error: { code: "not_found", message: "no" } }, 404) });
    const user = userEvent.setup();
    render(<App />);
    const tabs = await screen.findByRole("tablist", { name: "Settings of" });
    await user.click(within(tabs).getByRole("tab", { name: "Surveyor" }));
    await waitFor(() => expect(screen.getByText(/No Surveyor has reported/)).toBeInTheDocument());
    expect(screen.queryByRole("list", { name: "Sources by status" })).not.toBeInTheDocument();
  });
});
