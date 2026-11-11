import { describe, expect, it } from "vitest";
import { daemonFailureTitle } from "./daemon-failure";

describe("daemonFailureTitle", () => {
	it.each([
		{ code: "not_ready" as const, title: "Orchestrator daemon is not ready yet" },
		{ code: "port_unconfirmed" as const, title: "Orchestrator daemon is not ready yet" },
		{ code: "not_configured" as const, title: "Orchestrator daemon is not configured" },
		{ code: "daemon_unreachable" as const, title: "Orchestrator daemon is unreachable" },
		{ code: "identity_mismatch" as const, title: "Orchestrator daemon identity check failed" },
		{ code: "binary_missing" as const, title: "Orchestrator daemon binary is missing" },
		{ code: "spawn_failed" as const, title: "Orchestrator daemon failed to start" },
		{ code: "exited" as const, title: "Orchestrator daemon failed to start" },
	])("returns $title for $code", ({ code, title }) => {
		expect(daemonFailureTitle({ state: "error", code })).toBe(title);
	});
});
