// TEMPORARY dev-only simulation of a freshly installed machine, for walking
// through first-run onboarding. Enable with `VITE_AO_FRESH_MACHINE=1 npm run dev`.
// The daemon still runs; this answers a handful of its endpoints from memory:
// git, gh and the coding agents start missing, GitHub is not signed in, and
// installs pretend to succeed. State resets on reload. Remove before merging.

export const freshMachineSim = import.meta.env.VITE_AO_FRESH_MACHINE === "1";

const FAKE_INSTALL_MS = 2_000;
// Readiness lists agents by id; installs use their own target names.
const AGENT_ID_FOR_TARGET: Record<string, string> = { claude: "claude-code" };

const installed = new Set<string>();
const installStartedAt = new Map<string, number>();

const json = (body: unknown, status = 200) =>
	new Response(JSON.stringify(body), { status, headers: { "Content-Type": "application/json" } });

function installJob(target: string) {
	const startedAt = installStartedAt.get(target);
	if (startedAt === undefined) return { target, status: "idle", command: `brew install ${target} (simulated)` };
	const done = Date.now() - startedAt >= FAKE_INSTALL_MS;
	if (done) installed.add(AGENT_ID_FOR_TARGET[target] ?? target);
	return {
		target,
		status: done ? "succeeded" : "installing",
		command: `brew install ${target} (simulated)`,
		startedAt: new Date(startedAt).toISOString(),
		finishedAt: done ? new Date(startedAt + FAKE_INSTALL_MS).toISOString() : null,
	};
}

type Requirement = { id: string; satisfied: boolean; detail?: string };

async function requirements(real: Response): Promise<Response> {
	const body = (await real.json()) as { requirements?: Requirement[]; ready?: boolean };
	const reqs = (body.requirements ?? []).map((req) => {
		if (req.id !== "git" && req.id !== "gh") return req;
		const satisfied = installed.has(req.id);
		return { ...req, satisfied, detail: satisfied ? `/usr/local/bin/${req.id} (simulated)` : `${req.id} was not found on PATH.` };
	});
	return json({ ...body, requirements: reqs, ready: installed.has("git") && body.ready !== false });
}

type Agent = {
	id: string;
	effectiveReadiness: string;
	installation: { state: string };
	authentication: { state: string };
};

async function agentReadiness(real: Response): Promise<Response> {
	const body = (await real.json()) as { agents?: Agent[] };
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

/** Answers the request from the simulation, or returns undefined to let it
 * reach the daemon. `send` performs the real request when the simulation
 * only rewrites the daemon's answer. */
export async function simulateFreshMachine(input: Request, send: () => Promise<Response>): Promise<Response | undefined> {
	const path = new URL(input.url).pathname;
	const method = input.method;
	if (method === "GET" && path === "/api/v1/system/requirements") return requirements(await send());
	if (method === "GET" && path.startsWith("/api/v1/agents/readiness")) return agentReadiness(await send());
	if (method === "POST" && path === "/api/v1/agents/readiness/ensure") return agentReadiness(await send());
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
	const install = path.match(/^\/api\/v1\/system\/install\/([^/]+)$/);
	if (install) {
		const target = decodeURIComponent(install[1]);
		if (method === "POST" && !installStartedAt.has(target)) installStartedAt.set(target, Date.now());
		return json(installJob(target));
	}
	return undefined;
}
