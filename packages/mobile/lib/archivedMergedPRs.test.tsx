import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSession, PRStatePage } from "./api";
import type { HostedSession } from "./hostedRows";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	configForHost: (hostId: string) => (hostId === "offline" ? null : { host: `${hostId}.test`, httpPort: "3011", muxPort: "3011", secure: false, password: "pw" }),
	getArchivedPRSessions: vi.fn(),
}));

vi.mock("./api", () => ({ getArchivedPRSessions: fixtures.getArchivedPRSessions }));
vi.mock("./store", () => ({ useApp: () => ({ configForHost: fixtures.configForHost }) }));
vi.mock("./connectionError", () => ({ userFacingError: (_cause: unknown, fallback: string) => fallback }));

import { archivedMergedRow, readAllPRStatePages, unloadedMergedSessions, useArchivedMergedPRs, withArchivedPRSessions, type MergedPRSource } from "./archivedMergedPRs";

const session = (id: string): DashboardSession => ({ id, projectId: "p", isTerminated: true, prs: [{ number: Number(id.replace(/\D/g, "")) || 1, url: "", state: "merged" }] } as DashboardSession);
const hosted = (id: string, hostId = "mac"): HostedSession => ({ ...session(id), hostId, hostName: hostId });
const page = (ids: string[], nextCursor: string | null, total = 0): PRStatePage => ({ sessions: ids.map(session), nextCursor, total });

describe("readAllPRStatePages", () => {
	it("follows the cursor to the end and keeps a session that moved between pages once", async () => {
		const pages: Record<string, PRStatePage> = { start: page(["s1", "s2"], "c1", 4), c1: page(["s2", "s3"], "c2", 4), c2: page(["s4"], null, 4) };
		const seen: string[][] = [];
		const cursors: (string | null)[] = [];
		const outcome = await readAllPRStatePages(async (cursor) => {
			cursors.push(cursor);
			return pages[cursor ?? "start"];
		}, (sessions) => seen.push(sessions.map((s) => s.id)));
		expect(outcome).toBe("done");
		expect(cursors).toEqual([null, "c1", "c2"]);
		expect(seen).toEqual([["s1", "s2"], ["s3"], ["s4"]]);
	});

	it("stops on a repeated cursor rather than looping", async () => {
		const fetchPage = vi.fn(async () => page(["s1"], "same", 1));
		expect(await readAllPRStatePages(fetchPage, () => {})).toBe("done");
		expect(fetchPage).toHaveBeenCalledTimes(2);
	});

	it("reports a daemon that predates the filter, and an abort", async () => {
		expect(await readAllPRStatePages(async () => null, () => {})).toBe("unsupported");
		const controller = new AbortController();
		const onPage = vi.fn();
		const outcome = await readAllPRStatePages(async () => {
			controller.abort();
			return page(["s1"], "more", 2);
		}, onPage, controller.signal);
		expect(outcome).toBe("aborted");
		expect(onPage).not.toHaveBeenCalled();
	});
});

describe("merging archived rows", () => {
	it("keeps the live row for a session archived between the poll and the read", () => {
		const live = [hosted("s1"), hosted("s2")];
		const merged = withArchivedPRSessions(live, [hosted("s2"), hosted("s3"), hosted("s1", "other")]);
		expect(merged.map((s) => `${s.hostId}/${s.id}`)).toEqual(["mac/s1", "mac/s2", "mac/s3", "other/s1"]);
		expect(merged[1]).toBe(live[1]);
		expect(withArchivedPRSessions(live, [])).toBe(live);
	});

	it("keeps only the merged PRs of an archived row, tagged with its machine", () => {
		const row = archivedMergedRow({ hostId: "mac", hostName: "Mac" }, {
			...session("s1"),
			prs: [{ number: 4, url: "", state: "open" }, { number: 5, url: "", state: "merged" }, { number: 6, url: "", state: "closed" }],
		});
		expect(row.prs?.map((pr) => pr.number)).toEqual([5]);
		expect(row.pr?.number).toBe(5);
		expect(row).toMatchObject({ hostId: "mac", hostName: "Mac" });
	});

	it("counts the matching sessions not loaded yet, skipping daemons without the filter", () => {
		expect(unloadedMergedSessions([
			{ rows: [hosted("s1")], total: 5, supported: true },
			{ rows: [], total: 9, supported: false },
			{ rows: [hosted("s2"), hosted("s3")], total: 1, supported: true },
		])).toBe(4);
	});
});

type HookResult = ReturnType<typeof useArchivedMergedPRs>;

describe("useArchivedMergedPRs", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: HookResult;

	function Probe(props: { sources: MergedPRSource[]; active: boolean; refreshToken: number }) {
		latest = useArchivedMergedPRs(props.sources, { active: props.active, refreshToken: props.refreshToken });
		return null;
	}

	async function render(props: { sources: MergedPRSource[]; active: boolean; refreshToken: number }) {
		await act(async () => {
			if (renderer) renderer.update(<Probe {...props} />);
			else renderer = create(<Probe {...props} />);
		});
		// Let every page's promise settle.
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
	}

	const mac: MergedPRSource = { hostId: "mac", hostName: "Mac", archived: 12 };

	beforeEach(() => fixtures.getArchivedPRSessions.mockReset());
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
	});

	it("reads nothing until Merged or All is open, then reads every page", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s3"], null, 3) : page(["s1", "s2"], "c1", 3));
		await render({ sources: [mac], active: false, refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).not.toHaveBeenCalled();
		expect(latest.sessions).toEqual([]);

		await render({ sources: [mac], active: true, refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions.mock.calls.map((call) => call[1])).toEqual([
			{ prState: "merged", cursor: null, projectId: undefined },
			{ prState: "merged", cursor: "c1", projectId: undefined },
		]);
		expect(latest.sessions.map((s) => `${s.hostId}/${s.id}`)).toEqual(["mac/s1", "mac/s2", "mac/s3"]);
		expect(latest.sessions[0]?.hostName).toBe("Mac");
		expect(latest).toMatchObject({ unloaded: 0, loading: false, error: null });

		// Switching filters back and forth does not read again.
		await render({ sources: [mac], active: false, refreshToken: 0 });
		await render({ sources: [mac], active: true, refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
	});

	it("reads again on refresh or a new archive size, keeping the rows on screen meanwhile", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		await render({ sources: [mac], active: true, refreshToken: 0 });
		let release!: (value: PRStatePage) => void;
		fixtures.getArchivedPRSessions.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
		await render({ sources: [mac], active: true, refreshToken: 1 });
		expect(latest.loading).toBe(true);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		await act(async () => { release(page(["s2", "s1"], null, 2)); });
		await render({ sources: [mac], active: true, refreshToken: 1 });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s2", "s1"]);
		expect(latest.loading).toBe(false);

		await render({ sources: [{ ...mac, archived: 13 }], active: true, refreshToken: 1 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(3);
	});

	it("shows nothing from a daemon that ignored the filter, and keeps rows when a read fails", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValueOnce(null);
		await render({ sources: [mac], active: true, refreshToken: 0 });
		expect(latest.sessions).toEqual([]);
		expect(latest.unloaded).toBe(0);

		fixtures.getArchivedPRSessions.mockResolvedValueOnce(page(["s1"], null, 1));
		await render({ sources: [mac], active: true, refreshToken: 1 });
		fixtures.getArchivedPRSessions.mockRejectedValueOnce(new Error("down"));
		await render({ sources: [mac], active: true, refreshToken: 2 });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		expect(latest.error).toBe("Couldn't load merged pull requests.");
	});

	it("skips an offline machine and drops a machine that leaves the list", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		const offline: MergedPRSource = { hostId: "offline", hostName: "Off", archived: 3 };
		await render({ sources: [mac, offline], active: true, refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);
		await render({ sources: [], active: true, refreshToken: 0 });
		expect(latest.sessions).toEqual([]);
	});
});
