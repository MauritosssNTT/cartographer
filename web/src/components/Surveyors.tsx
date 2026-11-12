import { Fragment, useEffect, useState } from "react";
import { fetchAllSources, fetchComponents, fetchFeeds, fetchMaintenanceSummary } from "../api/client";
import type { ComponentRow, FeedRow, MaintenanceSummary, ReplicaRow, SourceEntry } from "../api/types";
import { expectedSilent } from "../lib/health";
import { PageSection } from "./Page";

/** A surveyor that stopped beating leaves the page within this, whatever the live counter does. */
const REFRESH_MS = 60_000;

/**
 * The components that concern the KB (D1901), read-only. Optional by design: a
 * failure or a 404 (a principal that does not see the whole KB, an older
 * server) is an empty list, and the page renders as it did before.
 */
export function useComponents(kb: string | null, live: number, enabled = true): ComponentRow[] {
  const [rows, setRows] = useState<ComponentRow[]>([]);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!enabled || !kb) return;
    const timer = window.setInterval(() => setTick((n) => n + 1), REFRESH_MS);
    return () => window.clearInterval(timer);
  }, [enabled, kb]);
  // Another KB shows nothing of the previous one while its answer is pending.
  useEffect(() => {
    setRows([]);
  }, [kb]);
  useEffect(() => {
    if (!enabled || !kb) {
      setRows([]);
      return;
    }
    const controller = new AbortController();
    fetchComponents(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.components) ? res.components : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live, tick, enabled]);
  return rows;
}

/** Last and next doctor and who repairs; null until it answers, or when it cannot (a narrowed principal). */
export function useMaintenanceSummary(kb: string | null, live: number): MaintenanceSummary | null {
  const [summary, setSummary] = useState<MaintenanceSummary | null>(null);
  useEffect(() => {
    setSummary(null);
  }, [kb]);
  useEffect(() => {
    if (!kb) return;
    const controller = new AbortController();
    fetchMaintenanceSummary(kb, controller.signal)
      .then(setSummary)
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setSummary(null);
      });
    return () => controller.abort();
  }, [kb, live]);
  return summary;
}

/** A live replica with the perimeter (the component's name) it belongs to. */
type SurveyorRow = ReplicaRow & { perimeter: string };

/** Every live Surveyor replica of the components, in the order the server sends them. */
function liveSurveyors(components: ComponentRow[]): SurveyorRow[] {
  return components.filter((c) => c.kind === "surveyor").flatMap((c) => c.replicas.map((r) => ({ ...r, perimeter: c.name })));
}

/** One line of the Work strip's alarms: a cause and, when the server knows one, when it heard of it. */
interface SurveyorAlarm {
  key: string;
  cause: string;
  at?: string;
}

/**
 * The alarms of the Surveyors that concern the KB: an expected one that is
 * silent or short of replicas, one whose budget is spent (once per replica),
 * and the alarms a Surveyor reported or the server raised. Times are the
 * server's (`received_at`, `last_seen_at`), never one a Surveyor declared. A
 * reported alarm of another KB is not shown here (its detail is free text).
 */
export function surveyorAlarms(components: ComponentRow[], kb: string): SurveyorAlarm[] {
  const out: SurveyorAlarm[] = [];
  for (const c of expectedSilent(components)) {
    out.push({
      key: `silent/${c.name}`,
      cause:
        c.state === "silent"
          ? `Surveyor ${c.name} is expected and has not reported`
          : `Surveyor ${c.name} is expected to run ${c.min_replicas} replicas and ${c.replicas.length} ${c.replicas.length === 1 ? "is" : "are"} reporting`,
      at: c.state === "silent" ? (c.last_seen_at ?? undefined) : undefined,
    });
  }
  for (const c of components.filter((c) => c.kind === "surveyor")) {
    for (const r of c.replicas.filter((r) => r.state === "budget_exhausted")) {
      out.push({ key: `budget/${c.name}/${r.id}`, cause: `Surveyor ${c.name} · ${r.id} has spent its budget`, at: undefined });
    }
    for (const [i, a] of c.alarms.entries()) {
      if (a.kb !== undefined && a.kb !== "" && a.kb !== kb) continue;
      out.push({
        key: `alarm/${c.name}/${i}/${a.code}`,
        cause: `Surveyor ${c.name}: ${a.code}${a.kb && a.detail ? ` - ${a.detail}` : ""}`,
        at: a.received_at,
      });
    }
  }
  return out;
}

function useFeeds(kb: string, live: number): FeedRow[] {
  const [rows, setRows] = useState<FeedRow[]>([]);
  useEffect(() => {
    const controller = new AbortController();
    fetchFeeds(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.feeds) ? res.feeds : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live]);
  return rows;
}

const day = (iso?: string) => (iso ? iso.slice(0, 10) : "");

export function age(iso: string, now = Date.now()): string {
  const s = Math.max(0, Math.round((now - Date.parse(iso)) / 1000));
  if (Number.isNaN(s)) return "";
  if (s < 90) return "just now";
  if (s < 5400) return `${Math.round(s / 60)} min ago`;
  if (s < 129600) return `${Math.round(s / 3600)} h ago`;
  return `${Math.round(s / 86400)} d ago`;
}

function SurveyorItem({ w }: { w: SurveyorRow }) {
  return (
    <li className="surveyors__row">
      <span className="surveyors__name">
        {w.perimeter} · {w.id}
      </span>
      <span className="surveyors__state" data-state={w.state}>
        {w.state.replace("_", " ")}
      </span>
      {w.last_run && (
        <span className="surveyors__meta">
          last run {age(w.last_run.at)}: {w.last_run.units_done} units, {w.last_run.commits} commits
        </span>
      )}
      {w.budgets.map((b) => (
        <span key={b.profile} className="surveyors__budget" data-spent={b.limit > 0 && b.spent >= b.limit ? "all" : undefined}>
          {b.profile}
          {b.tier ? ` (${b.tier})` : ""}
          <progress max={b.limit || 1} value={Math.min(b.spent, b.limit || 1)} aria-label={`${b.profile} budget`} />
          <span>{`${b.spent} / ${b.limit}`}</span>
        </span>
      ))}
      <span className="surveyors__meta">seen {age(w.last_beat_at)}</span>
      {w.next_window_at && <span className="surveyors__meta">next window {day(w.next_window_at)}</span>}
    </li>
  );
}

/**
 * Work's strip: the live surveyors with their last run and budgets, then the
 * feeds and systems whose last poll failed. Nothing renders when both are empty.
 */
export function SurveyorsStrip({
  kb,
  live,
  components,
  onOpen,
}: {
  kb: string;
  live: number;
  components: ComponentRow[];
  onOpen(conceptId: string): void;
}) {
  const surveyors = liveSurveyors(components);
  const feeds = useFeeds(kb, live);
  const alarms = surveyorAlarms(components, kb);
  if (surveyors.length === 0 && feeds.length === 0 && alarms.length === 0) return null;
  return (
    <section className="surveyors" aria-label="Surveyors and feeds">
      {alarms.length > 0 && (
        <ul className="surveyors__list" aria-label="Surveyor alarms">
          {alarms.map((a) => (
            <li key={a.key} className="surveyors__row" data-tone="error">
              <span className="surveyors__error">{a.cause}</span>
              {a.at && <span className="surveyors__meta">{age(a.at)}</span>}
            </li>
          ))}
        </ul>
      )}
      {surveyors.length > 0 && (
        <ul className="surveyors__list" aria-label="Surveyors">
          {surveyors.map((w) => (
            <SurveyorItem key={`${w.perimeter}/${w.id}`} w={w} />
          ))}
        </ul>
      )}
      {feeds.length > 0 && (
        <ul className="surveyors__list" aria-label="Feeds in error">
          {feeds.map((f) => (
            <li key={f.id} className="surveyors__row" data-tone="error">
              <button type="button" className="surveyors__link" onClick={() => onOpen(f.id)}>
                {f.title || f.id}
              </button>
              {f.source_shape && <span className="surveyors__meta">{f.source_shape}</span>}
              <span className="surveyors__error">{f.feed_error}</span>
              {f.feed_checked_at && <span className="surveyors__meta">checked {day(f.feed_checked_at)}</span>}
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

/**
 * Health's lines (amends D948): which Surveyor maintains the KB, who repairs
 * it and when the doctor last ran; and, apart, a notice when an expected
 * Surveyor is silent. Upkeep, not findings: none of it is in the lint report.
 * Nothing renders without a live or an expected Surveyor, or for a caller who
 * cannot see the whole KB.
 */
export function MaintainedBy({
  components,
  summary,
}: {
  components: ComponentRow[];
  summary: MaintenanceSummary | null;
}) {
  const surveyors = liveSurveyors(components);
  const repair = summary?.maintenance?.repair;
  const missing = expectedSilent(components);
  const fallback = repair?.state === "fallback";
  if (surveyors.length === 0 && missing.length === 0) return null;
  // Every perimeter that has a live surveyor on the KB is named, in the order
  // the server sends them (sorted by surveyor): choosing one would be a guess.
  const perimeters = new Map<string, SurveyorRow[]>();
  for (const w of surveyors) perimeters.set(w.perimeter, [...(perimeters.get(w.perimeter) ?? []), w]);
  return (
    <>
      {surveyors.length > 0 && (
        <p className="health__lede health__maintainer">
          Maintained by{" "}
          {[...perimeters].map(([name, ws], i) => {
            const busy = ws.some((w) => w.state === "running") ? "running" : ws[0]!.state.replace("_", " ");
            return (
              <Fragment key={name}>
                {i > 0 && ", "}
                <strong>{name}</strong> ({ws.length} {ws.length === 1 ? "replica" : "replicas"}, {busy})
              </Fragment>
            );
          })}
          {repair ? ` · repairs by ${repair.state === "surveyor" && repair.by ? repair.by : "Meridian"}` : ""}
          {summary?.last_doctor ? ` · last doctor ${summary.last_doctor}` : ""}
          {summary?.next_doctor ? ` · next due ${summary.next_doctor}` : ""}
        </p>
      )}
      {missing.map((c) => (
        <p key={c.name} className="health__lede health__maintainer health__notice" role="status">
          {c.state === "silent"
            ? `Surveyor ${c.name} is expected and has not reported${c.last_seen_at ? `; last seen ${age(c.last_seen_at)}` : ""}.`
            : `Surveyor ${c.name} is expected to run ${c.min_replicas} replicas and ${c.replicas.length} ${c.replicas.length === 1 ? "is" : "are"} reporting.`}
          {fallback && c.state === "silent" && (!repair?.by || repair.by === c.name) && " Meridian is repairing this KB meanwhile."}
        </p>
      ))}
    </>
  );
}

/**
 * Settings, Surveyor tab: the KB's Sources by ingest status and the feeds whose
 * last poll failed. They are the KB's, not the Surveyor's, so the block shows
 * whether or not a Surveyor has reported. Absent with no KB, on a failed read
 * (a 404 for a narrowed principal, an older server) or while it loads. The
 * server caps the list at 500: a count at the cap says "500 or more".
 */
export function SourcesBlock({ kb, live = 0 }: { kb: string | null; live?: number }) {
  const [sources, setSources] = useState<SourceEntry[] | null>(null);
  // Another KB shows nothing of the previous one while its answer is pending.
  useEffect(() => {
    setSources(null);
  }, [kb]);
  useEffect(() => {
    if (!kb) return;
    const controller = new AbortController();
    fetchAllSources(kb, controller.signal)
      .then((res) => setSources(Array.isArray(res?.sources) ? res.sources : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setSources(null);
      });
    return () => controller.abort();
  }, [kb, live]);
  const feeds = useFeedsOf(kb, live);
  if (!kb || sources === null) return null;
  const count = (status: string) => sources.filter((s) => s.ingest_status === status).length;
  const capped = sources.length >= 500;
  const n = (status: string) => `${count(status)}${capped ? "+" : ""}`;
  return (
    <PageSection title="Sources" id="settings-sources" count={sources.length}>
      <p className="page-note">
        The Sources registered in {kb}, by ingest status, and the feeds whose last poll failed. A Surveyor&apos;s recipes
        and connectors are not listed: it reports its typed values only.
      </p>
      <ul className="surveyors__list" aria-label="Sources by status">
        {["pending", "ingested", "skipped"].map((status) => (
          <li key={status} className="surveyors__row">
            <span className="surveyors__name">{status}</span>
            <span className="surveyors__meta">{n(status)}</span>
          </li>
        ))}
      </ul>
      {feeds.length > 0 && (
        <ul className="surveyors__list" aria-label="Sources whose last poll failed">
          {feeds.map((f) => (
            <li key={f.id} className="surveyors__row" data-tone="error">
              <span className="surveyors__name">{f.title || f.id}</span>
              {(f.recipe || f.source_shape) && <span className="surveyors__meta">{f.recipe || f.source_shape}</span>}
              <span className="surveyors__error">{f.feed_error}</span>
            </li>
          ))}
        </ul>
      )}
    </PageSection>
  );
}

function useFeedsOf(kb: string | null, live: number): FeedRow[] {
  const [rows, setRows] = useState<FeedRow[]>([]);
  useEffect(() => {
    setRows([]);
    if (!kb) return;
    const controller = new AbortController();
    fetchFeeds(kb, controller.signal)
      .then((res) => setRows(Array.isArray(res?.feeds) ? res.feeds : []))
      .catch((err: unknown) => {
        if (!(err instanceof DOMException && err.name === "AbortError")) setRows([]);
      });
    return () => controller.abort();
  }, [kb, live]);
  return rows;
}

/**
 * Activity's "Surveyor runs", only when there are runs: the runs the Surveyors
 * of the components reported for this KB, newest first by the server's clock
 * (`received_at`), and each replica's last run counts. Nothing renders
 * (no header, no empty state) without a run or when the read failed.
 */
export function SurveyorRunsSection({
  kb,
  components,
  authors = [],
  onAuthor,
}: {
  kb: string;
  components: ComponentRow[];
  /** The authors the timeline knows: a Surveyor's changes are `surveyor/...`. */
  authors?: string[];
  onAuthor?(author: string): void;
}) {
  const surveyors = components.filter((c) => c.kind === "surveyor");
  const runs = surveyors
    .flatMap((c) => c.runs.filter((r) => r.kb === kb).map((r) => ({ ...r, perimeter: c.name })))
    .sort((a, b) => Date.parse(b.received_at) - Date.parse(a.received_at))
    .slice(0, 20);
  const lasts = surveyors.flatMap((c) => c.replicas.filter((r) => r.last_run).map((r) => ({ perimeter: c.name, replica: r })));
  if (runs.length === 0 && lasts.length === 0) return null;
  const authorOf = (id: string) => authors.find((a) => a.startsWith("surveyor/") && a.includes(id));
  return (
    <PageSection title="Surveyor runs" id="activity-surveyor-runs" count={runs.length + lasts.length}>
      <ul className="surveyors__list" aria-label="Surveyor runs">
        {runs.map((r) => (
          <li key={`${r.perimeter}/${r.run_id}`} className="surveyors__row" data-tone={r.outcome === "ok" ? undefined : "error"}>
            <span className="surveyors__name">
              {r.perimeter} · {r.replica}
            </span>
            <span>{r.task}</span>
            <span className="surveyors__meta">{age(r.received_at)}</span>
            <span className="surveyors__meta">{r.outcome}</span>
            <span className="surveyors__meta">{r.tokens_spent} tokens</span>
            {r.error && <span className="surveyors__error">{r.error}</span>}
            {onAuthor && authorOf(r.replica) && (
              <button type="button" className="surveyors__link" onClick={() => onAuthor(authorOf(r.replica)!)}>
                Its changes
              </button>
            )}
          </li>
        ))}
        {lasts.map(({ perimeter, replica: r }) => (
          <li key={`last/${perimeter}/${r.id}`} className="surveyors__row">
            <span className="surveyors__name">
              {perimeter} · {r.id}
            </span>
            <span>last run</span>
            <span className="surveyors__meta">
              {r.last_run!.units_done} units, {r.last_run!.commits} commits, {r.last_run!.tokens_spent} tokens
            </span>
          </li>
        ))}
      </ul>
    </PageSection>
  );
}
