import { createCloudClient } from "@aoagents/cloud-client";
import { describe, expect, it, vi } from "vitest";
import { createCloudSessionSource } from "./source";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async () => null, setItem: async () => {}, removeItem: async () => {},
} }));

const response = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

describe("Cloud recovery through the real HTTP client", () => {
	it.each(["repository_checkout_failed", "working_directory_unusable", "project_setup_failed", "bootstrap_failed"])("uses the server recovery route for %s", async (code) => {
		const workspaceFailure = code !== "bootstrap_failed";
		const session = {
			id: "s1", orgId: "o1", projectId: "p1", kind: "worker", harness: "codex", displayName: "Worker", branch: "main",
			mode: "trusted", interfaceMode: "chat", deniedCommands: [], activityState: "idle", status: "building",
			isTerminated: false, runtimeConnected: workspaceFailure, desiredState: "running",
			observedState: workspaceFailure ? "running" : "terminated", runtimeState: workspaceFailure ? "running" : "terminated",
			startupError: { code, message: "Could not prepare the worker", at: "2026-10-21T00:00:00Z" },
			createdAt: "2026-10-21T00:00:00Z", updatedAt: "2026-10-21T00:00:00Z",
		};
		const posted: string[] = [];
		const client = createCloudClient({ baseUrl: "https://cloud.test", getAccessToken: () => "test-token", fetch: async (input, init) => {
			const path = new URL(String(input)).pathname;
			if (init?.method !== "POST" && path === "/api/cloud/v1/orgs/o1/sessions/s1") return response({ session });
			posted.push(path);
			if (init?.method === "POST" && path === "/api/cloud/v1/orgs/o1/sessions/s1/workspace/checkout") return response({ requested: true });
			if (init?.method === "POST" && path === "/api/cloud/v1/orgs/o1/sessions/s1/startup-retry") {
				// The real startup-retry route refuses a worker that already checked in.
				return workspaceFailure ? response({ error: "conflict", code: "startup_retry_unavailable", message: "Worker already connected", requestId: "workspace-retry" }, 409) : response({ session });
			}
			throw new Error(`Unexpected recovery request: ${init?.method} ${path}`);
		} });
		const source = createCloudSessionSource({ client, orgId: "o1", scopeKey: "recovery-account", isCurrent: () => true });
		await source.retryStartup!("s1");
		expect(posted).toEqual([workspaceFailure ? "/api/cloud/v1/orgs/o1/sessions/s1/workspace/checkout" : "/api/cloud/v1/orgs/o1/sessions/s1/startup-retry"]);
	});
});
