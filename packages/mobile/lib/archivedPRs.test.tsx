import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArchivedPRState, DashboardPR, DashboardSession, PRStatePage } from "./api";
import type { HostedSession } from "./hostedRows";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	configForHost: (hostId: string) => (hostId === "offline" ? null : { host: `${hostId}.test`, httpPort: "3011", muxPort: "3011", secure: false, password: "pw" }),
	getArchivedPRSessions: vi.fn(),
}));

vi.mock("./api", () => ({ getArchivedPRSessions: fixtures.getArchivedPRSessions }));
vi.mock("./store", () => ({ useApp: () => ({ configForHost: fixtures.configForHost }) }));
vi.mock("./connectionError", () => ({ userFacingError: (_cause: unknown, fallback: string) => fallback }));

import {
	archivedPRRow, mergeArchivedRows, readAllPRStatePages, unloadedArchivedSessions, useArchivedPRs, withArchivedPRSessions, type ArchivedPRSource,
} from "./archivedPRs";

const pr = (number: number, state: DashboardPR["state"] = "merged"): DashboardPR => ({ number, url: "", state });
const session = (id: string, prs: DashboardPR[] = [pr(Number(id.replace(/\D/g, "")) || 1)]): DashboardSession =>
	({ id, projectId: "p", isTerminated: true, prs } as DashboardSession);
const hosted = (id: string, hostId = "mac"): HostedSession => ({ ...session(id), hostId, hostName: hostId });
const page = (ids: string[], nextCursor: string | null, total = 0): PRStatePage => ({ sessions: ids.map((id) => session(id)), nextCursor, total });

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

describe("archived rows", () => {
	it("keeps the live row for a session archived between the poll and the read", () => {
		const live = [hosted("s1"), hosted("s2")];
		const merged = withArchivedPRSessions(live, [hosted("s2"), hosted("s3"), hosted("s1", "other")]);
		expect(merged.map((s) => `${s.hostId}/${s.id}`)).toEqual(["mac/s1", "mac/s2", "mac/s3", "other/s1"]);
		expect(merged[1]).toBe(live[1]);
		expect(withArchivedPRSessions(live, [])).toBe(live);
	});

	it("keeps only the PRs in the state the row was read for, tagged with its machine", () => {
		const ended = session("s1", [pr(4, "open"), pr(5, "merged"), pr(6, "closed")]);
		const merged = archivedPRRow({ hostId: "mac", hostName: "Mac" }, ended, "merged");
		expect(merged.prs?.map((p) => p.number)).toEqual([5]);
		expect(merged.pr?.number).toBe(5);
		expect(merged).toMatchObject({ hostId: "mac", hostName: "Mac" });
		expect(archivedPRRow({ hostId: "mac", hostName: "Mac" }, ended, "closed").prs?.map((p) => p.number)).toEqual([6]);
	});

	it("joins a session read for both its merged and its closed PRs into one row", () => {
		const ended = session("s1", [pr(5, "merged"), pr(6, "closed")]);
		const source = { hostId: "mac", hostName: "Mac" };
		const rows = mergeArchivedRows([[archivedPRRow(source, ended, "merged"), hosted("s2")], [archivedPRRow(source, ended, "closed")]]);
		expect(rows.map((row) => row.id)).toEqual(["s1", "s2"]);
		expect(rows[0]?.prs?.map((p) => p.number)).toEqual([5, 6]);
	});

	it("counts the matching sessions not loaded yet, skipping daemons without the filter", () => {
		expect(unloadedArchivedSessions([
			{ rows: [hosted("s1")], total: 5, supported: true },
			{ rows: [], total: 9, supported: false },
			{ rows: [hosted("s2"), hosted("s3")], total: 1, supported: true },
		])).toBe(4);
	});
});

type HookProps = { sources: ArchivedPRSource[]; states: ArchivedPRState[]; refreshToken: number };
type HookResult = ReturnType<typeof useArchivedPRs>;

describe("useArchivedPRs", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: HookResult;

	function Probe(props: HookProps) {
		latest = useArchivedPRs(props.sources, { states: props.states, refreshToken: props.refreshToken });
		return null;
	}

	async function render(props: HookProps) {
		await act(async () => {
			if (renderer) renderer.update(<Probe {...props} />);
			else renderer = create(<Probe {...props} />);
		});
		// Let every page's promise settle.
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
	}

	const mac: ArchivedPRSource = { hostId: "mac", hostName: "Mac", archived: 12 };
	const calls = () => fixtures.getArchivedPRSessions.mock.calls.map((call) => call[1] as { prState: string; cursor: string | null });

	beforeEach(() => fixtures.getArchivedPRSessions.mockReset());
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
	});

	it("reads nothing until asked, then every page of each state", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { prState: ArchivedPRState; cursor: string | null }) => {
			if (options?.prState === "closed") return { sessions: [session("c9", [pr(9, "closed")])], nextCursor: null, total: 1 };
			return options?.cursor ? page(["s3"], null, 3) : page(["s1", "s2"], "c1", 3);
		});
		await render({ sources: [mac], states: [], refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).not.toHaveBeenCalled();
		expect(latest.sessions).toEqual([]);

		await render({ sources: [mac], states: ["merged"], refreshToken: 0 });
		expect(calls()).toEqual([
			{ prState: "merged", cursor: null, projectId: undefined },
			{ prState: "merged", cursor: "c1", projectId: undefined },
		]);
		expect(latest.sessions.map((s) => `${s.hostId}/${s.id}`)).toEqual(["mac/s1", "mac/s2", "mac/s3"]);
		expect(latest.sessions[0]?.hostName).toBe("Mac");
		expect(latest).toMatchObject({ unloaded: { merged: 0, closed: 0 }, loading: { merged: false, closed: false }, error: null });

		// All adds the closed ones without reading merged again.
		await render({ sources: [mac], states: ["merged", "closed"], refreshToken: 0 });
		expect(calls().slice(2)).toEqual([{ prState: "closed", cursor: null, projectId: undefined }]);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1", "s2", "s3", "c9"]);

		// Going back to Open and returning reads nothing new.
		await render({ sources: [mac], states: [], refreshToken: 0 });
		await render({ sources: [mac], states: ["merged", "closed"], refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(3);
	});

	it("reads again on refresh or a new archive size, keeping the rows on screen meanwhile", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		await render({ sources: [mac], states: ["merged"], refreshToken: 0 });
		let release!: (value: PRStatePage) => void;
		fixtures.getArchivedPRSessions.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
		await render({ sources: [mac], states: ["merged"], refreshToken: 1 });
		expect(latest.loading.merged).toBe(true);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		await act(async () => { release(page(["s2", "s1"], null, 2)); });
		await render({ sources: [mac], states: ["merged"], refreshToken: 1 });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s2", "s1"]);
		expect(latest.loading.merged).toBe(false);

		await render({ sources: [{ ...mac, archived: 13 }], states: ["merged"], refreshToken: 1 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(3);
	});

	it("shows nothing from a daemon that ignored the filter, and keeps rows when a read fails", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValueOnce(null);
		await render({ sources: [mac], states: ["merged"], refreshToken: 0 });
		expect(latest.sessions).toEqual([]);
		expect(latest.unloaded.merged).toBe(0);

		fixtures.getArchivedPRSessions.mockResolvedValueOnce(page(["s1"], null, 1));
		await render({ sources: [mac], states: ["merged"], refreshToken: 1 });
		fixtures.getArchivedPRSessions.mockRejectedValueOnce(new Error("down"));
		await render({ sources: [mac], states: ["merged"], refreshToken: 2 });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		expect(latest.error).toBe("Couldn't load past pull requests.");
	});

	it("skips an offline machine and drops a machine that leaves the list", async () => {
		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		const offline: ArchivedPRSource = { hostId: "offline", hostName: "Off", archived: 3 };
		await render({ sources: [mac, offline], states: ["merged"], refreshToken: 0 });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);
		await render({ sources: [], states: ["merged"], refreshToken: 0 });
		expect(latest.sessions).toEqual([]);
	});
});
