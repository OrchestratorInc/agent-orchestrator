import { describe, expect, it } from "vitest";
import type { CloudCpSession } from "../lib/cloud-cp/types";
import { cloudArtifactFileUrl } from "./useCloudArtifacts";
import { toCloudWorkspaceSession } from "./useWorkspaceQuery";

function cloudSession(overrides: Partial<CloudCpSession> = {}): CloudCpSession {
	return {
		id: "s-1",
		orgId: "org-1",
		projectId: "p-1",
		kind: "worker",
		harness: "claude-code",
		displayName: "Report",
		branch: "ao/s-1",
		mode: "trusted",
		interfaceMode: "tui",
		deniedCommands: [],
		activityState: "idle",
		status: "idle",
		runtimeConnected: false,
		isTerminated: false,
		prs: [],
		createdAt: "2026-10-16T00:00:00Z",
		updatedAt: "2026-10-16T00:00:00Z",
		...overrides,
	};
}

describe("cloudArtifactFileUrl", () => {
	it("escapes each path segment under the access root", () => {
		expect(cloudArtifactFileUrl("https://cp.test/api/cloud/v1/artifact-files/t/", "my docs/r#1.html")).toBe(
			"https://cp.test/api/cloud/v1/artifact-files/t/my%20docs/r%231.html",
		);
	});

	it("adds the file version as a query parameter", () => {
		expect(cloudArtifactFileUrl("https://cp.test/files/t/", "report.html", "2026-10-16T09:55:07.878689Z")).toBe(
			"https://cp.test/files/t/report.html?v=2026-10-16T09%3A55%3A07.878689Z",
		);
	});
});

describe("toCloudWorkspaceSession artifacts", () => {
	it("maps the output fields and lists files without URLs", () => {
		const session = toCloudWorkspaceSession(
			cloudSession({
				requestedOutput: "artifact",
				outputType: "artifact",
				artifactFiles: [
					{ path: "report.html", name: "report.html", kind: "html", contentType: "text/html; charset=utf-8", size: 12, sha256: "a".repeat(64), updatedAt: "2026-10-16T01:00:00Z" },
				],
			}),
			{ id: "p-1", displayName: "Project" },
			"org-1",
		);
		expect(session.requestedOutput).toBe("artifact");
		expect(session.outputType).toBe("artifact");
		expect(session.artifactFiles).toEqual([
			{ kind: "html", name: "report.html", path: "report.html", size: 12, updatedAt: "2026-10-16T01:00:00Z" },
		]);
	});

	it("leaves artifact fields empty for an older control plane", () => {
		const session = toCloudWorkspaceSession(cloudSession(), { id: "p-1", displayName: "Project" }, "org-1");
		expect(session.artifactFiles).toBeUndefined();
		expect(session.outputType).toBeUndefined();
	});
});
