import { useEffect, useRef } from "react";

import { captureRendererEvent } from "../lib/telemetry";
import { useAgentReadinessQuery } from "./useAgentReadinessQuery";

/**
 * Logs the install's agent inventory once per app launch.
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
    const installed = data.agents.filter((agent) => agent.installation.state === "installed").length;
    console.info("AO agent inventory", { installed, supported: data.agents.length });
    if (installed === 0) void captureRendererEvent("ao.setup.agent_missing", { reason: "none_installed" });
  }, [data]);
}
