import { afterEach, describe, expect, it } from "vitest";
import { freshMachineSimActive, setFreshMachineSim, simulateFreshMachine } from "./fresh-machine-sim";
import { developerModeStorageKey } from "./window-translucency";

const noDaemon = () => Promise.reject(new TypeError("Failed to fetch"));
const get = (path: string) => new Request(`http://127.0.0.1:3001${path}`);

afterEach(() => {
	setFreshMachineSim(false);
	window.localStorage.removeItem(developerModeStorageKey);
});

describe("freshMachineSimActive", () => {
	it("stays off until it is switched on", () => {
		window.localStorage.setItem(developerModeStorageKey, "true");
		expect(freshMachineSimActive()).toBe(false);
		setFreshMachineSim(true);
		expect(freshMachineSimActive()).toBe(true);
	});

	it("needs Developer mode on as well", () => {
		setFreshMachineSim(true);
		expect(freshMachineSimActive()).toBe(false);
		window.localStorage.setItem(developerModeStorageKey, "true");
		expect(freshMachineSimActive()).toBe(true);
		window.localStorage.setItem(developerModeStorageKey, "false");
		expect(freshMachineSimActive()).toBe(false);
	});
});

describe("simulateFreshMachine", () => {
	it("answers without a daemon: git and the GitHub CLI missing, agents not installed", async () => {
		const requirements = await (await simulateFreshMachine(get("/api/v1/system/requirements"), noDaemon))!.json();
		expect(requirements.ready).toBe(false);
		expect(requirements.requirements.find((req: { id: string }) => req.id === "gh").satisfied).toBe(false);

		const readiness = await (await simulateFreshMachine(get("/api/v1/agents/readiness"), noDaemon))!.json();
		expect(readiness.agents.map((agent: { id: string }) => agent.id)).toEqual(["claude-code", "codex"]);
		expect(readiness.agents.every((agent: { installation: { state: string } }) => agent.installation.state === "not_installed")).toBe(true);
	});

	it("rewrites the daemon's real answer when there is one", async () => {
		const daemon = () => Promise.resolve(Response.json({ ready: true, requirements: [{ id: "git", satisfied: true }, { id: "tmux", satisfied: true }] }));
		const body = await (await simulateFreshMachine(get("/api/v1/system/requirements"), daemon))!.json();
		expect(body.requirements).toEqual([
			expect.objectContaining({ id: "git", satisfied: false }),
			{ id: "tmux", satisfied: true },
		]);
	});

	it("installs agents through their own endpoint, with progress, listed with the other jobs", async () => {
		const plans = await (await simulateFreshMachine(get("/api/v1/agents/installers"), noDaemon))!.json();
		expect(plans.agents.every((plan: { automatic: boolean }) => plan.automatic)).toBe(true);

		const started = await (await simulateFreshMachine(new Request("http://127.0.0.1:3001/api/v1/agents/codex/install", { method: "POST" }), noDaemon))!.json();
		expect(started).toMatchObject({ target: "codex", status: "installing", progress: { phase: "downloading" } });

		const listed = await (await simulateFreshMachine(get("/api/v1/agents/install-jobs"), noDaemon))!.json();
		expect(listed.jobs.map((job: { target: string }) => job.target)).toEqual(["codex"]);
	});

	it("lets every other request through to the daemon", async () => {
		expect(await simulateFreshMachine(get("/api/v1/sessions"), noDaemon)).toBeUndefined();
	});
});
