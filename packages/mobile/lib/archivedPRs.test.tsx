import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ArchivedPRState, DashboardPR, DashboardSession, PRStatePage } from "./api";
import type { HostedSession } from "./hostedRows";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

const fixtures = vi.hoisted(() => ({
	offline: new Set(["offline"]),
	configForHost: (hostId: string) => (fixtures.offline.has(hostId) ? null : { host: `${hostId}.test`, httpPort: "3011", muxPort: "3011", secure: false, password: "pw" }),
	getArchivedPRSessions: vi.fn(),
}));

vi.mock("./api", () => ({ getArchivedPRSessions: fixtures.getArchivedPRSessions }));
vi.mock("./store", () => ({ useApp: () => ({ configForHost: fixtures.configForHost }) }));
vi.mock("./connectionError", () => ({ userFacingError: (_cause: unknown, fallback: string) => fallback }));

import {
	archivedPRRow, firstPageUnchanged, mergeArchivedRows, readAllPRStatePages, readIsConsistent, unloadedArchivedSessions, useArchivedPRs,
	withArchivedPRSessions, type ArchivedPRSource,
} from "./archivedPRs";

const pr = (number: number, state: DashboardPR["state"] = "merged"): DashboardPR => ({ number, url: "", state });
const session = (id: string, prs: DashboardPR[] = [pr(Number(id.replace(/\D/g, "")) || 1)]): DashboardSession =>
	({ id, projectId: "p", isTerminated: true, prs } as DashboardSession);
const hosted = (id: string, hostId = "mac"): HostedSession => ({ ...session(id), hostId, hostName: hostId });
const page = (ids: string[], nextCursor: string | null, total = 0, version: string | null = "v1"): PRStatePage =>
	({ sessions: ids.map((id) => session(id)), nextCursor, total, version });

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

	beforeEach(() => {
		fixtures.getArchivedPRSessions.mockReset();
		fixtures.offline = new Set(["offline"]);
	});
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

describe("useArchivedPRs reconnects", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: ReturnType<typeof useArchivedPRs>;

	function Probe(props: { sources: ArchivedPRSource[] }) {
		latest = useArchivedPRs(props.sources, { states: ["merged"], refreshToken: 0 });
		return null;
	}

	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
		fixtures.offline = new Set(["offline"]);
	});

	// The machine's config appears without the lookup changing identity, and
	// the archive count from its last poll is the same: only its being
	// reachable changes, and that alone must read the source it skipped.
	it("reads a source skipped while its machine was offline once the machine answers", async () => {
		fixtures.getArchivedPRSessions.mockReset();
		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		fixtures.offline = new Set(["laptop"]);
		const sources: ArchivedPRSource[] = [{ hostId: "laptop", hostName: "Laptop", archived: 4 }];
		await act(async () => { renderer = create(<Probe sources={sources} />); });
		expect(fixtures.getArchivedPRSessions).not.toHaveBeenCalled();

		fixtures.offline = new Set();
		await act(async () => { renderer!.update(<Probe sources={[...sources]} />); });
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
	});
});

describe("firstPageUnchanged", () => {
	const held = { complete: true, supported: true, version: "v1" };
	it("holds when the page names the list a complete stream holds", () => {
		expect(firstPageUnchanged(held, page(["s3", "s2"], "c1", 3, "v1"))).toBe(true);
	});
	// A later page can change with the total and first page intact; only the
	// version covers it.
	it("does not hold for a new version, even with the same first page and total", () => {
		expect(firstPageUnchanged(held, page(["s3", "s2"], "c1", 3, "v2"))).toBe(false);
	});
	it("does not hold for a list that never finished or a daemon without versions", () => {
		expect(firstPageUnchanged({ ...held, complete: false }, page(["s3", "s2"], "c1", 3, "v1"))).toBe(false);
		expect(firstPageUnchanged({ ...held, version: null }, page(["s3", "s2"], "c1", 3, null))).toBe(false);
	});
});

describe("readIsConsistent", () => {
	it("needs one version across the pages and every session of the list", () => {
		expect(readIsConsistent(3, 3, new Set(["v1"]))).toBe(true);
		expect(readIsConsistent(3, 3, new Set([null]))).toBe(true);
		expect(readIsConsistent(3, 3, new Set(["v1", "v2"]))).toBe(false);
		expect(readIsConsistent(2, 3, new Set(["v1"]))).toBe(false);
	});
});

describe("useArchivedPRs as the archive moves", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: ReturnType<typeof useArchivedPRs>;
	const loadingSeen: boolean[] = [];

	function Probe(props: { archived: number }) {
		latest = useArchivedPRs([{ hostId: "mac", hostName: "Mac", archived: props.archived }], { states: ["merged"], refreshToken: 0 });
		loadingSeen.push(latest.loading.merged);
		return null;
	}

	async function render(archived: number) {
		await act(async () => {
			if (renderer) renderer.update(<Probe archived={archived} />);
			else renderer = create(<Probe archived={archived} />);
		});
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
	}

	beforeEach(() => {
		fixtures.getArchivedPRSessions.mockReset();
		loadingSeen.length = 0;
	});
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
	});

	// A worker ending without a merged PR moves the archive's size; one page
	// shows nothing changed, and the list neither reloads nor flickers.
	it("checks one page when the archive's size moves and keeps the list when it did not change", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s1"], null, 3) : page(["s3", "s2"], "c1", 3));
		await render(10);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
		loadingSeen.length = 0;

		await render(11);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(3);
		expect(fixtures.getArchivedPRSessions.mock.calls[2]?.[1]).toMatchObject({ cursor: null });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s3", "s2", "s1"]);
		expect(loadingSeen).not.toContain(true);
	});

	it("reads on from the checked page when the list did change", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s1"], null, 3) : page(["s3", "s2"], "c1", 3));
		await render(10);
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s2", "s1"], null, 4, "v2") : page(["s4", "s3"], "c1", 4, "v2"));
		await render(11);
		// The checked first page is reused: one more request for the rest.
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(4);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s4", "s3", "s2", "s1"]);
	});

	// One session restored and another ended: same total, same first page, a
	// different second page. The version shows it, and the list reads on.
	it("reads on when the first page and total match but the list's version moved", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s1"], null, 3, "v1") : page(["s4", "s3"], "c1", 3, "v1"));
		await render(10);
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s0"], null, 3, "v2") : page(["s4", "s3"], "c1", 3, "v2"));
		await render(11);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(4);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s4", "s3", "s0"]);
	});

	// The list moved between two pages of one read: start over from page one.
	it("reads again from page one when its pages came from different lists", async () => {
		fixtures.getArchivedPRSessions
			.mockResolvedValueOnce(page(["s4", "s3"], "c1", 3, "v1"))
			.mockResolvedValueOnce(page(["s1"], null, 3, "v2"))
			.mockResolvedValueOnce(page(["s4", "s3"], "c1", 3, "v2"))
			.mockResolvedValueOnce(page(["s2"], null, 3, "v2"));
		await render(10);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(4);
		expect(fixtures.getArchivedPRSessions.mock.calls[2]?.[1]).toMatchObject({ cursor: null });
		expect(latest.sessions.map((s) => s.id)).toEqual(["s4", "s3", "s2"]);
		expect(latest.unloaded.merged).toBe(0);
	});

	it("stops retrying a list that keeps moving, showing what it read and counting only that", async () => {
		let version = 0;
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) => {
			version += 1;
			return options?.cursor ? page(["s1"], null, 5, `v${version}`) : page(["s3", "s2"], "c1", 5, `v${version}`);
		});
		await render(10);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(6);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s3", "s2", "s1"]);
		expect(latest.unloaded.merged).toBe(0);
		expect(latest.loading.merged).toBe(false);
	});

	// A re-read that failed on page two leaves its old rows, but they are no
	// longer a proven-complete list: the next size change reads everything.
	it("reads everything on the next change after a re-read failed partway", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s1"], null, 3) : page(["s3", "s2"], "c1", 3));
		await render(10);
		fixtures.getArchivedPRSessions.mockReset();
		// The list moved (a new version), so the check reads on and page two fails.
		fixtures.getArchivedPRSessions
			.mockResolvedValueOnce(page(["s3", "s2"], "c1", 3, "v2"))
			.mockRejectedValueOnce(new Error("dropped"));
		await render(11);
		expect(latest.error).toBe("Couldn't load past pull requests.");

		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { cursor: string | null }) =>
			options?.cursor ? page(["s1"], null, 3) : page(["s3", "s2"], "c1", 3));
		await render(12);
		// Not the one-page shortcut: both pages are read again.
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(4);
		expect(latest.error).toBeNull();
		expect(latest.sessions.map((s) => s.id)).toEqual(["s3", "s2", "s1"]);
	});

	it("lets a read in flight finish when the size moves, then checks once", async () => {
		let release!: (value: PRStatePage) => void;
		fixtures.getArchivedPRSessions.mockImplementationOnce(() => new Promise((resolve) => { release = resolve; }));
		await render(10);
		await render(11);
		await render(12);
		const signal = (fixtures.getArchivedPRSessions.mock.calls[0]?.[2] as { signal: AbortSignal }).signal;
		expect(signal.aborted).toBe(false);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);

		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		await act(async () => { release(page(["s1"], null, 1)); });
		await render(12);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
	});

	// A first read that fails partway shows what it has, and the counts claim
	// no more than that.
	it("counts only the rows shown after a read fails partway", async () => {
		fixtures.getArchivedPRSessions
			.mockResolvedValueOnce(page(["s5", "s4"], "c1", 5))
			.mockRejectedValueOnce(new Error("down"));
		await render(10);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s5", "s4"]);
		expect(latest.unloaded.merged).toBe(0);
		expect(latest.error).toBe("Couldn't load past pull requests.");
	});
});

describe("useArchivedPRs after a failed read", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: ReturnType<typeof useArchivedPRs>;
	const sources: ArchivedPRSource[] = [{ hostId: "laptop", hostName: "Laptop", archived: 4 }];

	function Probe() {
		latest = useArchivedPRs([...sources], { states: ["merged"], refreshToken: 0 });
		return null;
	}

	async function render() {
		await act(async () => {
			if (renderer) renderer.update(<Probe />);
			else renderer = create(<Probe />);
		});
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
	}

	beforeEach(() => {
		fixtures.getArchivedPRSessions.mockReset();
		fixtures.offline = new Set();
	});
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
		fixtures.offline = new Set(["offline"]);
	});

	// The read fails as the machine drops; nothing else moves (same count, no
	// refresh), so only the machine coming back can trigger the retry.
	it("reads a failed source again when its machine comes back, and only then", async () => {
		fixtures.getArchivedPRSessions.mockRejectedValueOnce(new Error("offline"));
		await render();
		expect(latest.error).toBe("Couldn't load past pull requests.");
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);

		// Settling or re-rendering while still reachable does not retry.
		await render();
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);

		fixtures.offline = new Set(["laptop"]);
		await render();
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);

		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		fixtures.offline = new Set();
		await render();
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		expect(latest.error).toBeNull();

		// A healthy source is not re-read by another drop and return.
		fixtures.offline = new Set(["laptop"]);
		await render();
		fixtures.offline = new Set();
		await render();
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
	});
});

describe("useArchivedPRs reconnect edge cases", () => {
	let renderer: ReactTestRenderer | null = null;
	let latest: ReturnType<typeof useArchivedPRs>;
	const sources: ArchivedPRSource[] = [{ hostId: "laptop", hostName: "Laptop", archived: 4 }];
	const statesOf = (call: unknown[]) => (call[1] as { prState: string }).prState;

	function Probe(props: { states: ArchivedPRState[] }) {
		latest = useArchivedPRs([...sources], { states: props.states, refreshToken: 0 });
		return null;
	}

	async function render(states: ArchivedPRState[]) {
		await act(async () => {
			if (renderer) renderer.update(<Probe states={states} />);
			else renderer = create(<Probe states={states} />);
		});
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
	}

	beforeEach(() => {
		fixtures.getArchivedPRSessions.mockReset();
		fixtures.offline = new Set();
	});
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = null;
		fixtures.offline = new Set(["offline"]);
	});

	// Closed failed on All; the machine drops and returns while Merged is open.
	// The closed read retries when All is shown again, not while Merged is.
	it("retries a failed read of a filter that was not open when its machine came back", async () => {
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { prState: string }) => {
			if (options?.prState === "closed") throw new Error("offline");
			return page(["s1"], null, 1);
		});
		await render(["merged", "closed"]);
		expect(latest.error).toBe("Couldn't load past pull requests.");

		await render(["merged"]);
		fixtures.offline = new Set(["laptop"]);
		await render(["merged"]);
		fixtures.offline = new Set();
		fixtures.getArchivedPRSessions.mockImplementation(async (_config: unknown, options?: { prState: string }) =>
			options?.prState === "closed" ? { ...page(["c9"], null, 1), sessions: [session("c9", [pr(9, "closed")])] } : page(["s1"], null, 1));
		await render(["merged"]);
		expect(fixtures.getArchivedPRSessions.mock.calls.map(statesOf).filter((s) => s === "closed")).toHaveLength(1);

		await render(["merged", "closed"]);
		expect(fixtures.getArchivedPRSessions.mock.calls.map(statesOf).filter((s) => s === "closed")).toHaveLength(2);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1", "c9"]);
		expect(latest.error).toBeNull();
	});

	// The machine answers again before its pending request fails: the return
	// waits for the read to settle, then the failed read is retried once.
	it("retries once when the machine is back before its pending read fails", async () => {
		let reject!: (error: Error) => void;
		fixtures.getArchivedPRSessions.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
		await render(["merged"]);
		fixtures.offline = new Set(["laptop"]);
		await render(["merged"]);
		fixtures.offline = new Set();
		await render(["merged"]);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(1);

		fixtures.getArchivedPRSessions.mockResolvedValue(page(["s1"], null, 1));
		await act(async () => { reject(new Error("offline")); });
		await act(async () => { for (let i = 0; i < 10; i += 1) await Promise.resolve(); });
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
		expect(latest.sessions.map((s) => s.id)).toEqual(["s1"]);
		expect(latest.error).toBeNull();

		// Settled and healthy: nothing more is fetched.
		await render(["merged"]);
		expect(fixtures.getArchivedPRSessions).toHaveBeenCalledTimes(2);
	});
});
