import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	agentSheetRoute,
	connectSheetRoute,
	machineNameSheetRoute,
	parkSheetResult,
	projectSheetRoute,
	modelSheetRoute,
	releaseSheetResult,
	resetSheetResults,
	takeSheetResult,
} from "./sheetResult";

beforeEach(() => resetSheetResults());

describe("parkSheetResult", () => {
	it("round-trips a callback through its key", () => {
		const fn = vi.fn();
		const key = parkSheetResult<string>(fn);
		takeSheetResult<string>(key)?.("proj-1");
		expect(fn).toHaveBeenCalledWith("proj-1");
	});

	it("hands out a distinct key per park, so concurrent sheets can't collide", () => {
		expect(parkSheetResult(vi.fn())).not.toBe(parkSheetResult(vi.fn()));
	});

	// The leak this guards: a sheet dismissed without choosing still unmounts, and
	// its route releases the key. Looking it up afterwards must be a no-op, not a
	// call into a stale closure from a screen that is gone.
	it("stops resolving once released", () => {
		const fn = vi.fn();
		const key = parkSheetResult(fn);
		releaseSheetResult(key);
		expect(takeSheetResult(key)).toBeUndefined();
	});

	it("tolerates a missing or undefined key", () => {
		expect(takeSheetResult(undefined)).toBeUndefined();
		expect(takeSheetResult("never-parked")).toBeUndefined();
		expect(() => releaseSheetResult(undefined)).not.toThrow();
	});
});

describe("route builders", () => {
	it("encodes includeAll as a string param and parks the callback", () => {
		const fn = vi.fn();
		const route = projectSheetRoute({ selected: "p1", onSelect: fn, includeAll: false });
		expect(route.pathname).toBe("/sheets/project");
		expect(route.params.includeAll).toBe("0");
		expect(route.params.selected).toBe("p1");
		takeSheetResult<string>(route.params.resultKey)?.("p2");
		expect(fn).toHaveBeenCalledWith("p2");
	});

	it("defaults includeAll to on", () => {
		expect(projectSheetRoute({ selected: "", onSelect: vi.fn() }).params.includeAll).toBe("1");
	});

	it("omits optional title and subtitle rather than sending undefined", () => {
		const params = projectSheetRoute({ selected: "", onSelect: vi.fn() }).params;
		expect("title" in params).toBe(false);
		expect("subtitle" in params).toBe(false);
	});

	it("passes title and subtitle through when given", () => {
		const params = projectSheetRoute({
			selected: "",
			onSelect: vi.fn(),
			title: "Project",
			subtitle: "Where this agent gets its workspace.",
		}).params;
		expect(params).toMatchObject({ title: "Project", subtitle: "Where this agent gets its workspace." });
	});

	it("builds the agent route", () => {
		const fn = vi.fn();
		const route = agentSheetRoute({ selected: "claude-code", onSelect: fn });
		expect(route.pathname).toBe("/sheets/agent");
		expect(route.params.selected).toBe("claude-code");
		takeSheetResult<string>(route.params.resultKey)?.("codex");
		expect(fn).toHaveBeenCalledWith("codex");
	});

	it("builds a project-scoped model route and reports the selection", () => {
		const fn = vi.fn();
		const route = modelSheetRoute({ agentId: "codex", projectId: "p1", selected: "gpt-5", onSelect: fn });
		expect(route.pathname).toBe("/sheets/model");
		expect(route.params).toMatchObject({ agentId: "codex", projectId: "p1", selected: "gpt-5" });
		takeSheetResult<string>(route.params.resultKey)?.("gpt-5.4");
		expect(fn).toHaveBeenCalledWith("gpt-5.4");
	});

	it("passes the saved machine id to a reconnect form", () => {
		const fn = vi.fn();
		const route = connectSheetRoute(fn, "h_dev");
		expect(route.pathname).toBe("/sheets/connect");
		expect(route.params.hostId).toBe("h_dev");
		takeSheetResult<void>(route.params.resultKey)?.();
		expect(fn).toHaveBeenCalled();
	});

	it("builds the machine-name route and reports a nullable choice", () => {
		const fn = vi.fn();
		const route = machineNameSheetRoute({ suggested: "studio-mac", appearance: { symbol: "laptop" }, hostId: "h_new", onSelect: fn });
		expect(route.pathname).toBe("/sheets/machine-name");
		expect(route.params.suggested).toBe("studio-mac");
		expect(route.params.symbol).toBe("laptop");
		expect(route.params.hostId).toBe("h_new");
		const choice = { name: "Studio", appearance: { symbol: "desktop" as const } };
		takeSheetResult<typeof choice | null>(route.params.resultKey)?.(choice);
		expect(fn).toHaveBeenCalledWith(choice);
	});
});
