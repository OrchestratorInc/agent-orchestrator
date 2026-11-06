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

/**
 * Sends ao.setup.checked the first time the startup system check and agent
 * readiness have both answered, then again whenever the result changes (a
 * tool or agent installed or signed in, or GitHub sign-in becoming known).
 * Reuses the startup checks' cached results: it never runs a probe of its own,
 * so the GitHub credential probe is reported only once the app already ran it.
 */
export function useSetupCheckedTelemetry(): void {
  const { query } = useSystemRequirementsGate();
  const { data: catalog } = useAgentReadinessQuery();
  const { data: githubAuth } = useQuery({ ...githubAuthRequirementQueryOptions, enabled: false });
  const requirements = query.data?.requirements;
  useEffect(() => {
    if (!requirements || !catalog) return;
    const check = buildSetupCheck(requirements, catalog, githubAuth?.satisfied);
    const signature = JSON.stringify(check);
    const storage = telemetryStorage();
    try {
      if (storage?.getItem(SETUP_CHECK_STORAGE_KEY) === signature) return;
    } catch {
      // Unreadable storage: send, and let the per-name rate limit bound it.
    }
    void captureRendererEvent("ao.setup.checked", check).then((sent) => {
      if (!sent) return;
      try {
        storage?.setItem(SETUP_CHECK_STORAGE_KEY, signature);
      } catch {
        // Best effort: a lost record re-sends the same state once.
      }
    });
  }, [requirements, catalog, githubAuth?.satisfied]);
}
