import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";

import { buildAgentInventory, buildSetupCheck } from "../lib/agent-inventory-telemetry";
import { captureRendererEvent, telemetryStorage } from "../lib/telemetry";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";
import { githubAuthRequirementQueryOptions, useSystemRequirementsGate } from "./useSystemRequirementsGate";

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

export function setupCheckChanged(check: SetupCheckRecord, sent: SetupCheckRecord | null): boolean {
  if (!sent) return true;
  return Object.entries(check).some(([key, value]) => sent[key] !== value);
}

export function useSetupCheckedTelemetry(): void {
  const { query } = useSystemRequirementsGate();
  const { data: catalog } = useAgentReadinessQuery();
  const { data: githubAuth } = useQuery({ ...githubAuthRequirementQueryOptions, enabled: false });
  const requirements = query.data?.requirements;
  useEffect(() => {
    if (!requirements || !catalog) return;
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
      }
    });
  }, [requirements, catalog, githubAuth?.satisfied]);
}
