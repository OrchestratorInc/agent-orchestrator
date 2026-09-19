import { describe, expect, it } from "vitest";
import type { Project, Session } from "@aoagents/cloud-client";
import { kanbanColumnForStatus, toDashboardSession, toProjectInfo } from "./mapping";

const project: Project = {
	id: "p1", orgId: "o1", displayName: "web",
	repositoryUrl: "https://github.com/acme/web", defaultBranch: "main",
	config: {}, createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-01T00:00:00Z",
};

const session: Session = {
	id: "s1", orgId: "o1", projectId: "p1", kind: "worker", harness: "claude-code",
	displayName: "fix the build", branch: "ao/fix-build", mode: "trusted",
	deniedCommands: [], activityState: "active", status: "working",
	runtimeConnected: true, isTerminated: false,
	createdAt: "2026-09-01T00:00:00Z", updatedAt: "2026-09-02T00:00:00Z",
};

describe("toProjectInfo", () => {
	it("names the project by its display name", () => {
		expect(toProjectInfo(project)).toEqual({ id: "p1", name: "web", kind: "single_repo" });
	});
});

describe("toDashboardSession", () => {
	it("carries the fields the board reads", () => {
		const mapped = toDashboardSession(session);
		expect(mapped.id).toBe("s1");
		expect(mapped.projectId).toBe("p1");
		expect(mapped.harness).toBe("claude-code");
		expect(mapped.displayName).toBe("fix the build");
		expect(mapped.branch).toBe("ao/fix-build");
		expect(mapped.status).toBe("working");
		expect(mapped.createdAt).toBe("2026-09-01T00:00:00Z");
		expect(mapped.lastActivityAt).toBe("2026-09-02T00:00:00Z");
	});

	// Mobile's mode vocabulary is the controller (chat/tui), not cloud's trust
	// level (read-only/standard/trusted). Cloud sessions are always Chat.
	it("reports Chat regardless of the cloud trust mode", () => {
		expect(toDashboardSession(session).mode).toBe("chat");
		expect(toDashboardSession({ ...session, mode: "read-only" }).mode).toBe("chat");
	});

	// Cloud has no terminal mux handle; leaving it set would offer a terminal
	// this pass cannot open.
	it("leaves the terminal handle unset", () => {
		expect(toDashboardSession(session).terminalHandleId).toBeUndefined();
	});

	it("derives the board column cloud does not send", () => {
		expect(toDashboardSession(session).kanbanColumn).toBe("building");
	});

	// Task 14 (cloud-lifecycle.ts, ported from desktop) reads the sandbox
	// lifecycle nested as session.cloud.{sandboxProvider,desiredState,observedState}
	// alongside a sibling runtimeConnected, even though the wire shape is flat.
	it("nests the sandbox lifecycle fields cloud sends flat", () => {
		const mapped = toDashboardSession({
			...session,
			sandboxProvider: "coder",
			desiredState: "running",
			observedState: "bootstrapping",
		});
		expect(mapped.cloud).toEqual({
			sandboxProvider: "coder",
			desiredState: "running",
			observedState: "bootstrapping",
		});
		expect(mapped.runtimeConnected).toBe(true);
	});

	it("leaves cloud undefined rather than an empty object when the lifecycle fields are absent", () => {
		expect(toDashboardSession(session).cloud).toBeUndefined();
	});
});

describe("kanbanColumnForStatus", () => {
	it("places each cloud status on the board", () => {
		expect(kanbanColumnForStatus("working")).toBe("building");
		expect(kanbanColumnForStatus("needs_input")).toBe("building");
		expect(kanbanColumnForStatus("idle")).toBe("building");
		expect(kanbanColumnForStatus("ci_failed")).toBe("validating");
		expect(kanbanColumnForStatus("changes_requested")).toBe("validating");
		expect(kanbanColumnForStatus("pr_open")).toBe("needs_review");
		expect(kanbanColumnForStatus("draft")).toBe("needs_review");
		expect(kanbanColumnForStatus("review_pending")).toBe("needs_review");
		expect(kanbanColumnForStatus("approved")).toBe("ready");
		expect(kanbanColumnForStatus("mergeable")).toBe("ready");
		expect(kanbanColumnForStatus("merged")).toBe("archive");
		expect(kanbanColumnForStatus("exited")).toBe("archive");
		expect(kanbanColumnForStatus("terminated")).toBe("archive");
	});

	it("falls back to building for an unknown status", () => {
		expect(kanbanColumnForStatus("something_new")).toBe("building");
	});
});
