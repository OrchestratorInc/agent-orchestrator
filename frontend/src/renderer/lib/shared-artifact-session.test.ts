import { describe, expect, it, vi } from "vitest";
import type { SharedArtifactCopy, SharedArtifactSessionRecord } from "../../preload";
import type { CloudCpArtifactShareView } from "./cloud-cp";
import { nextSharedArtifactStep, type LinkedSession, type SharedArtifactSessionDeps } from "./shared-artifact-session";

const key = { baseUrl: "https://cp.example", userId: "u1", linkId: "link-1" };
const record: SharedArtifactSessionRecord = { sessionId: "ao-7", dir: "shared/r", entry: "shared/r/r.html", updatedAt: "v1-date", fingerprint: "f1" };
const view = (updatedAt: string): CloudCpArtifactShareView => ({ linkId: "link-1", name: "r.html", contentType: "text/html", size: 1, updatedAt, contentUrl: "https://link/raw?vt=t" });
const copy = (fingerprint: string): SharedArtifactCopy => ({ entry: "shared/r/r.html", dir: "shared/r", files: [], missing: [], fingerprint });

function deps(overrides: Partial<SharedArtifactSessionDeps> & { session?: LinkedSession } = {}): SharedArtifactSessionDeps {
	const { session = { exists: true, projectId: "", terminated: false, busy: false }, ...rest } = overrides;
	return {
		getRecord: vi.fn(async () => record),
		setRecord: vi.fn(async () => undefined),
		removeRecord: vi.fn(async () => undefined),
		getSession: vi.fn(async () => session),
		view: vi.fn(async () => view("v1-date")),
		copy: vi.fn(async () => ({ ok: true as const, copy: copy("f1") })),
		...rest,
	};
}

describe("nextSharedArtifactStep", () => {
	it("starts a draft when no session works from the link", async () => {
		expect(await nextSharedArtifactStep(deps({ getRecord: vi.fn(async () => null) }), key, "r.html")).toEqual({ kind: "draft" });
	});

	it("forgets a deleted session and starts a draft", async () => {
		const d = deps({ session: { exists: false } });
		expect(await nextSharedArtifactStep(d, key, "r.html")).toEqual({ kind: "draft" });
		expect(d.removeRecord).toHaveBeenCalledWith(key);
	});

	it("asks about an archived session", async () => {
		const step = await nextSharedArtifactStep(deps({ session: { exists: true, projectId: "", terminated: true, busy: false } }), key, "r.html");
		expect(step).toEqual({ kind: "archived", record });
	});

	it("opens the session when the link has not changed, without copying", async () => {
		const d = deps();
		expect(await nextSharedArtifactStep(d, key, "r.html")).toEqual({ kind: "open", sessionId: "ao-7", projectId: "", entry: "shared/r/r.html" });
		expect(d.copy).not.toHaveBeenCalled();
	});

	it("opens the session when the link cannot be checked", async () => {
		const step = await nextSharedArtifactStep(deps({ view: vi.fn(async () => { throw new Error("offline"); }) }), key, "r.html");
		expect(step.kind).toBe("open");
	});

	it("treats a new date with the same bytes as no change, and moves the record's date", async () => {
		const d = deps({ view: vi.fn(async () => view("v2-date")) });
		expect((await nextSharedArtifactStep(d, key, "r.html")).kind).toBe("open");
		expect(d.setRecord).toHaveBeenCalledWith(key, { ...record, updatedAt: "v2-date" });
	});

	it("does not offer a version the person already kept theirs over", async () => {
		const d = deps({
			getRecord: vi.fn(async () => ({ ...record, declinedFingerprint: "f2" })),
			view: vi.fn(async () => view("v2-date")),
			copy: vi.fn(async () => ({ ok: true as const, copy: copy("f2") })),
		});
		expect((await nextSharedArtifactStep(d, key, "r.html")).kind).toBe("open");
	});

	it("does not take a page file that failed once for the owner's change", async () => {
		let calls = 0;
		const d = deps({
			view: vi.fn(async () => view("v2-date")),
			copy: vi.fn(async () => ({ ok: true as const, copy: { ...copy(`f-missing-${++calls}`), missing: ["a.png"] } })),
		});
		expect((await nextSharedArtifactStep(d, key, "r.html")).kind).toBe("open");
		expect(d.copy).toHaveBeenCalledTimes(2);
	});

	it("offers the owner's real change", async () => {
		const d = deps({
			session: { exists: true, projectId: "", terminated: false, busy: true },
			view: vi.fn(async () => view("v2-date")),
			copy: vi.fn(async () => ({ ok: true as const, copy: copy("f2") })),
		});
		const step = await nextSharedArtifactStep(d, key, "r.html");
		expect(step).toMatchObject({ kind: "update", sessionId: "ao-7", busy: true, updatedAt: "v2-date", copy: { fingerprint: "f2" } });
	});
});
