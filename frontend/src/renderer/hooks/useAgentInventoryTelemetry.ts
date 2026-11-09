import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { buildAgentInventory, buildSetupCheck } from "../lib/agent-inventory-telemetry";
import { captureRendererEvent, telemetryStorage } from "../lib/telemetry";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";
import { githubAuthRequirementQueryOptions, useSystemRequirementsGate } from "./useSystemRequirementsGate";

/**
 * Reports the install's agent inventory once per app launch.
 *
 * Mounted from the shell so it runs once, and reads the cached agents query, so
 * it adds no request of its own. Reported a single time per launch because the
 * inventory only changes when someone installs or authorizes an agent. Surface
 * and focus ensures update the display copy, which would otherwise make this a
 * repeating stream.
 */
export function useAgentInventoryTelemetry(): void {
  const { data } = useAgentReadinessQuery();
  const reported = useRef(false);

  useEffect(() => {
    if (!data || reported.current) return;
    reported.current = true;
    const inventory = buildAgentInventory(data);
    void captureRendererEvent("ao.renderer.agents_available", inventory);
    if (inventory.installed_count === 0) void captureRendererEvent("ao.agents.missing", { reason: "none_installed" });
  }, [data]);
}

const SETUP_CHECK_STORAGE_KEY = "ao.telemetry.setupCheck";

type SetupCheckRecord = Record<string, unknown>;

function readSetupCheckRecord(storage: ReturnType<typeof telemetryStorage>): SetupCheckRecord | null {
  try {
    const parsed: unknown = JSON.parse(storage?.getItem(SETUP_CHECK_STORAGE_KEY) ?? "null");
    return parsed && typeof parsed === "object" ? (parsed as SetupCheckRecord) : null;
  } catch {
    return null;
  }
}

/**
 * Whether a check differs from the last one sent. Only the fields this check
 * knows are compared: the GitHub probe often has not answered at launch, and an
 * unanswered probe is not a change.
 */
export function setupCheckChanged(check: SetupCheckRecord, sent: SetupCheckRecord | null): boolean {
  if (!sent) return true;
  return Object.entries(check).some(([key, value]) => sent[key] !== value);
}

/**
 * Sends ao.setup.checked the first time the startup system check and agent
 * readiness have both settled, then again when a known value changes (a tool
 * or agent installed or signed in, or GitHub sign-in becoming known). Reuses
 * the startup checks' cached results and never runs a probe of its own, so the
 * GitHub credential probe is reported only once the app already ran it.
 */
export function useSetupCheckedTelemetry(): void {
  const { query } = useSystemRequirementsGate();
  const { data: catalog } = useAgentReadinessQuery();
  const { data: githubAuth } = useQuery({ ...githubAuthRequirementQueryOptions, enabled: false });
  const requirements = query.data?.requirements;
  useEffect(() => {
    if (!requirements || !catalog) return;
    // Agent probes still running would report a count that is about to change.
    if (catalog.agents.some((agent) => agent.installation.freshness === "checking" || agent.authentication.freshness === "checking")) return;
    const check: SetupCheckRecord = buildSetupCheck(requirements, catalog, githubAuth?.satisfied);
    const storage = telemetryStorage();
    const sent = readSetupCheckRecord(storage);
    if (!setupCheckChanged(check, sent)) return;
    void captureRendererEvent("ao.setup.checked", check).then((captured) => {
      if (!captured) return;
      try {
        storage?.setItem(SETUP_CHECK_STORAGE_KEY, JSON.stringify({ ...sent, ...check }));
      } catch {
        // Best effort: a lost record re-sends the same state once.
      }
    });
  }, [requirements, catalog, githubAuth?.satisfied]);
}
