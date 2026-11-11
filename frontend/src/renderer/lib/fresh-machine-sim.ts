import { AGENT_OPTIONS, agentLabel } from "./agent-options";
import { IS_DEV } from "./is-dev";
import { developerModeStorageKey } from "./window-translucency";

// Dev builds only: a simulated freshly installed machine, for walking through
// first-run onboarding on a machine that is already set up. Switched on from
// Settings > General > Advanced (shown in dev builds with Developer mode on),
// and on only while Developer mode stays on. IS_DEV folds to false in
// production, so the API client's hook and everything here are stripped there.
//
// It answers a handful of daemon endpoints from memory: git, the GitHub CLI
// and the coding agents start missing, GitHub is not signed in, and installs
// pretend to succeed after a moment. When a daemon is running its real answers
// are rewritten; without one (the browser preview, `npm run dev:web`) a small
// baseline stands in, so the whole flow can be walked there too. The switch
// lasts for this window's session; the simulated installs reset on reload.

const STORAGE_KEY = "ao.dev.freshMachine";
const FAKE_INSTALL_MS = 3_000;
// Readiness lists agents by id; installs use their own target names.
const AGENT_ID_FOR_TARGET: Record<string, string> = { claude: "claude-code" };

function readFlag(key: string, storage: () => Storage | undefined): boolean {
	try {
		return storage()?.getItem(key) === "true";
	} catch {
		return false;
	}
}
const local = () => (typeof window === "undefined" ? undefined : window.localStorage);
const session = () => (typeof window === "undefined" ? undefined : window.sessionStorage);

/** Whether requests are answered as if on a new machine: a dev build, with
 * Developer mode on and the simulation switched on. */
export function freshMachineSimActive(): boolean {
	return IS_DEV && readFlag(STORAGE_KEY, session) && readFlag(developerModeStorageKey, local);
}

/** Switches the simulation on or off; switching it on starts from nothing
 * installed (or only git, with `gitInstalled`, which keeps the app's
 * missing-git screen out of the way). */
export function setFreshMachineSim(on: boolean, { gitInstalled = false }: { gitInstalled?: boolean } = {}) {
	if (!IS_DEV) return;
	installed.clear();
	installStartedAt.clear();
	if (on && gitInstalled) installed.add("git");
	try {
		if (on) session()?.setItem(STORAGE_KEY, "true");
		else session()?.removeItem(STORAGE_KEY);
	} catch {
		// Session storage blocked: the simulation stays off.
	}
}

const installed = new Set<string>();
const installStartedAt = new Map<string, number>();

const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

/** A pretend install: it downloads, then installs, reporting progress as a
 * real installer's output would, and succeeds after FAKE_INSTALL_MS. */
function installJob(target: string) {
	const command = `brew install ${target} (simulated)`;
	const startedAt = installStartedAt.get(target);
	if (startedAt === undefined) return { target, status: "idle", command };
	const elapsed = Date.now() - startedAt;
	const done = elapsed >= FAKE_INSTALL_MS;
	if (done) installed.add(AGENT_ID_FOR_TARGET[target] ?? target);
	const downloading = elapsed < FAKE_INSTALL_MS / 2;
	return {
		target,
		status: done ? "succeeded" : "installing",
		command,
		startedAt: new Date(startedAt).toISOString(),
		finishedAt: done ? new Date(startedAt + FAKE_INSTALL_MS).toISOString() : null,
		...(done ? {} : {
			progress: {
				phase: downloading ? "downloading" : "installing",
				percent: Math.round((elapsed / FAKE_INSTALL_MS) * 100),
				detail: downloading ? `Downloading ${target}… (simulated)` : `Installing ${target}… (simulated)`,
			},
		}),
	};
}

/** The daemon's answer, or `fallback` when there is no daemon to ask. */
async function realOr<T>(send: () => Promise<Response>, fallback: T): Promise<T> {
	try {
		const response = await send();
		return response.ok ? ((await response.json()) as T) : fallback;
	} catch {
		return fallback;
	}
}

type Requirement = { id: string; label?: string; required?: boolean; satisfied: boolean; detail?: string };
type Requirements = { requirements?: Requirement[]; ready?: boolean };
const BASE_REQUIREMENTS: Requirements = {
	ready: true,
	requirements: [
		{ id: "git", label: "git", required: true, satisfied: true },
		{ id: "gh", label: "GitHub CLI", required: false, satisfied: true },
	],
};

async function requirements(send: () => Promise<Response>): Promise<Response> {
	const body = await realOr(send, BASE_REQUIREMENTS);
	const reqs = (body.requirements ?? []).map((req) => {
		if (req.id !== "git" && req.id !== "gh") return req;
		const satisfied = installed.has(req.id);
		return { ...req, satisfied, detail: satisfied ? `/usr/local/bin/${req.id} (simulated)` : `${req.id} was not found on PATH.` };
	});
	return json({ ...body, requirements: reqs, ready: installed.has("git") && body.ready !== false });
}

type Agent = {
	id: string;
	label?: string;
	effectiveReadiness: string;
	installation: { state: string };
	authentication: { state: string };
	usageCount?: number;
};
const baseAgent = (id: string, label: string): Agent => ({
	id,
	label,
	effectiveReadiness: "ready",
	installation: { state: "installed" },
	authentication: { state: "authorized" },
	usageCount: 0,
});
// Every harness AO knows, as on a real machine.
const BASE_AGENTS: { agents: Agent[] } = { agents: AGENT_OPTIONS.map((id) => baseAgent(id, agentLabel(id))) };

async function agentReadiness(send: () => Promise<Response>): Promise<Response> {
	const body = await realOr(send, BASE_AGENTS);
	// An installed agent counts as signed in, so the flow can move on.
	const agents = (body.agents ?? []).map((agent) => {
		const ready = installed.has(agent.id);
		return {
			...agent,
			effectiveReadiness: ready ? "ready" : "not_ready",
			installation: { ...agent.installation, state: ready ? "installed" : "not_installed" },
			authentication: { ...agent.authentication, state: ready ? "authorized" : "unauthorized" },
		};
	});
	return json({ ...body, agents });
}

/** One-click install plans for the agents (Settings > Harness and onboarding
 * read these to offer an Install). */
async function installers(send: () => Promise<Response>): Promise<Response> {
	const body = await realOr(send, {
		agents: BASE_AGENTS.agents.map(({ id }) => ({
			agentId: id,
			automatic: true,
			available: true,
			method: "script",
			methods: [{ id: "script", label: "Install script", available: true, recommended: true, reinstallAvailable: false, updateAvailable: false }],
			documentationUrl: "https://example.invalid/install",
		})),
	});
	return json(body);
}

/** Answers the request from the simulation, or returns undefined to let it
 * reach the daemon. `send` performs the real request when the simulation
 * only rewrites the daemon's answer. */
export async function simulateFreshMachine(input: Request, send: () => Promise<Response>): Promise<Response | undefined> {
	const path = new URL(input.url).pathname;
	const method = input.method;
	if (method === "GET" && path === "/api/v1/system/requirements") return requirements(send);
	if (method === "GET" && path.startsWith("/api/v1/agents/readiness")) return agentReadiness(send);
	if (method === "POST" && path === "/api/v1/agents/readiness/ensure") return agentReadiness(send);
	// "Check again" on the missing-git screen: pretend the person installed it.
	if (method === "POST" && path === "/api/v1/agents/refresh") {
		installed.add("git");
		return json({});
	}
	// gh counts as signed in once it is installed.
	if (method === "GET" && path === "/api/v1/system/github-auth") {
		const satisfied = installed.has("gh");
		return json({ id: "github-auth", label: "GitHub", satisfied, required: false, detail: satisfied ? "Signed in (simulated)" : "Not signed in." });
	}
	if (method === "GET" && path === "/api/v1/agents/installers") return installers(send);
	// Agents install through their own endpoint, and every job shows in one list.
	if (method === "GET" && path === "/api/v1/agents/install-jobs") {
		return json({ jobs: [...installStartedAt.keys()].map(installJob) });
	}
	const agentInstall = path.match(/^\/api\/v1\/agents\/([^/]+)\/install$/);
	if (agentInstall) {
		const agent = decodeURIComponent(agentInstall[1]!);
		if (method === "POST" && !installStartedAt.has(agent)) installStartedAt.set(agent, Date.now());
		return json(installJob(agent));
	}
	const install = path.match(/^\/api\/v1\/system\/install\/([^/]+)$/);
	if (install) {
		const target = decodeURIComponent(install[1]!);
		if (method === "POST" && !installStartedAt.has(target)) installStartedAt.set(target, Date.now());
		return json(installJob(target));
	}
	return undefined;
}
