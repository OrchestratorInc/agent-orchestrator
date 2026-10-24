import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionPRSummary } from "../api";
import type { PullRequestSource } from "./source";

const identities = new WeakMap<PullRequestSource, number>();
let identity = 0;
const entries = new WeakMap<PullRequestSource, Map<string, { at: number; prs: SessionPRSummary[] }>>();
const REUSE_MS = 60_000;
const MAX_RETAINED_ENTRIES = 256;
function sourceID(source: PullRequestSource) { if (!identities.has(source)) identities.set(source, ++identity); return identities.get(source)!; }
type Target = { source: PullRequestSource; sessionId: string };
const keyOf = ({ source, sessionId }: Target) => JSON.stringify([sourceID(source), source.scopeKey, sessionId]);

/** Bounded, memory-only reuse belongs to one authenticated source instance. */
export function useSourcePRs(targets: Target[]) {
  const key = JSON.stringify(targets.map(keyOf));
  const cache = useRef(new Map<string, SessionPRSummary[]>());
  const [generation, setGeneration] = useState(0);
  const [version, bump] = useState(0);
  const [status, setStatus] = useState({ key, loading: targets.length > 0, error: "" });
  const active = new Set(targets.map(keyOf));
  useEffect(() => {
    const controller = new AbortController();
    const selected = [...new Map(targets.filter(({ source }) => source.isCurrent()).map((target) => [keyOf(target), target])).values()];
    for (const cached of cache.current.keys()) if (!active.has(cached)) cache.current.delete(cached);
    setStatus({ key, loading: selected.length > 0, error: "" });
    void (async () => {
      let error = "";
      for (let i = 0; i < selected.length; i += 6) {
        if (controller.signal.aborted) return;
        await Promise.all(selected.slice(i, i + 6).map(async (target) => {
          const { source, sessionId } = target;
          const now = Date.now();
          const sourceEntries = entries.get(source);
          if (sourceEntries) for (const [id, entry] of sourceEntries) if (now - entry.at >= REUSE_MS) sourceEntries.delete(id);
          const retained = entries.get(source)?.get(sessionId);
          try {
            const reuse = generation === 0 && retained !== undefined;
            const prs = reuse ? retained.prs : await source.list(sessionId, { signal: controller.signal });
            if (controller.signal.aborted || !source.isCurrent()) return;
            cache.current.set(keyOf(target), prs);
            if (!reuse) {
              if (!entries.has(source)) entries.set(source, new Map());
              const retainedEntries = entries.get(source)!;
              retainedEntries.delete(sessionId);
              retainedEntries.set(sessionId, { at: Date.now(), prs });
              while (retainedEntries.size > MAX_RETAINED_ENTRIES) retainedEntries.delete(retainedEntries.keys().next().value!);
            }
          } catch (cause) {
            if (!controller.signal.aborted && source.isCurrent()) error ||= cause instanceof Error ? cause.message : "Could not load Cloud pull requests.";
          }
        }));
        if (!controller.signal.aborted) bump((n) => n + 1);
      }
      if (!controller.signal.aborted) setStatus({ key, loading: false, error });
    })();
    return () => controller.abort();
  }, [key, generation]);
  const reload = useCallback(() => setGeneration((n) => n + 1), []);
  return {
    list: (source: PullRequestSource, sessionId: string) => source.isCurrent() && active.has(keyOf({ source, sessionId })) ? cache.current.get(keyOf({ source, sessionId })) ?? [] : [],
    loading: status.key === key ? status.loading : targets.length > 0,
    error: status.key === key ? status.error : "",
    reload,
    version,
  };
}
