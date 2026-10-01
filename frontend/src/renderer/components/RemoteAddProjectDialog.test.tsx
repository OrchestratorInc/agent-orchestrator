import { act, render, waitFor } from "@testing-library/react";
import type { ComponentProps } from "react";
import { beforeEach, expect, it, vi } from "vitest";
import type { CreateProjectFlow, CreateProjectInput } from "./CreateProjectFlow";

type FlowProps = ComponentProps<typeof CreateProjectFlow>;
const flows = vi.hoisted(() => new Map<string, FlowProps>());
const requests = vi.hoisted(() => new Map<string, { POST: ReturnType<typeof vi.fn> }>());
const clientForHost = vi.hoisted(() => vi.fn((hostId: string) => {
	const client = requests.get(hostId);
	if (!client) throw new Error(`Host ${hostId} is not connected`);
	return client;
}));
vi.mock("./CreateProjectFlow", () => ({
	CreateProjectFlow: (props: FlowProps) => {
		if (props.hostId) flows.set(props.hostId, props);
		return <div>{props.hostLabel}</div>;
	},
}));
vi.mock("../lib/host-clients", () => ({ clientForHost }));

import { RemoteAddProjectDialog } from "./RemoteAddProjectDialog";

const input: CreateProjectInput = {
	path: "/srv/todo-app",
	workerAgent: "opencode",
	orchestratorAgent: "codex",
	trackerIntake: { enabled: true, assignee: "alice" },
};

function addHost(hostId: string) {
	const POST = vi.fn(async (path: string) => path === "/api/v1/projects"
		? { data: { project: { id: "same-project-id" } } }
		: { data: { orchestrator: { id: `${hostId}-orchestrator` } } });
	requests.set(hostId, { POST });
	return POST;
}

beforeEach(() => {
	flows.clear();
	requests.clear();
	clientForHost.mockClear();
});

it("uses the shared create flow and only the selected host for project and orchestrator creation", async () => {
	const a = addHost("host-a");
	const b = addHost("host-b");
	const aCreated = vi.fn();
	const bCreated = vi.fn();
	const closeA = vi.fn();
	const closeB = vi.fn();
	const standaloneA = vi.fn();
	render(<>
		<RemoteAddProjectDialog hostId="host-a" hostLabel="Host A" connected onCreated={aCreated} onCreateStandaloneAgent={standaloneA} onOpenChange={closeA} />
		<RemoteAddProjectDialog hostId="host-b" hostLabel="Host B" connected onCreated={bCreated} onCreateStandaloneAgent={vi.fn()} onOpenChange={closeB} />
	</>);
	expect(flows.get("host-a")).toMatchObject({ mode: "choose", initialOpen: true, hostId: "host-a", hostLabel: "Host A" });
	expect(flows.get("host-a")?.onCreateStandaloneAgent).toBe(standaloneA);
	expect(flows.get("host-b")).toMatchObject({ mode: "choose", initialOpen: true, hostId: "host-b", hostLabel: "Host B" });
	await act(async () => {
		await Promise.all([
			flows.get("host-a")?.onCreateProject(input),
			flows.get("host-b")?.onCreateProject({ ...input, clonePreparationId: "prepared-b" }),
		]);
	});
	// false means durable registration: the shell can navigate before the agent starts.
	expect(aCreated).toHaveBeenNthCalledWith(1, "same-project-id", false);
	expect(bCreated).toHaveBeenNthCalledWith(1, "same-project-id", false);
	await waitFor(() => {
		expect(aCreated).toHaveBeenNthCalledWith(2, "same-project-id", true);
		expect(bCreated).toHaveBeenNthCalledWith(2, "same-project-id", true);
		expect(closeA).toHaveBeenCalledWith(false);
		expect(closeB).toHaveBeenCalledWith(false);
	});
	expect(a).toHaveBeenCalledWith("/api/v1/projects", { body: {
		path: "/srv/todo-app", asWorkspace: undefined, clonePreparationId: undefined,
		config: { worker: { agent: "opencode" }, orchestrator: { agent: "codex" }, trackerIntake: { enabled: true, assignee: "alice" } },
	} });
	expect(b).toHaveBeenCalledWith("/api/v1/projects", { body: {
		path: "/srv/todo-app", asWorkspace: undefined, clonePreparationId: "prepared-b",
		config: { worker: { agent: "opencode" }, orchestrator: { agent: "codex" }, trackerIntake: { enabled: true, assignee: "alice" } },
	} });
	expect(a).toHaveBeenCalledWith("/api/v1/orchestrators", { body: { projectId: "same-project-id" } });
	expect(b).toHaveBeenCalledWith("/api/v1/orchestrators", { body: { projectId: "same-project-id" } });
});

it("does not contact any daemon while the selected host is offline", async () => {
	addHost("host-a");
	render(<RemoteAddProjectDialog hostId="host-a" hostLabel="Host A" connected={false} onCreated={vi.fn()} onCreateStandaloneAgent={vi.fn()} onOpenChange={vi.fn()} />);
	await expect(flows.get("host-a")?.onCreateProject(input)).rejects.toThrow("Connect to Host A");
	expect(clientForHost).not.toHaveBeenCalled();
});

it("initializes Git on the selected host before the shared flow submits the project", async () => {
	const a = addHost("host-a");
	render(<RemoteAddProjectDialog hostId="host-a" hostLabel="Host A" connected onCreated={vi.fn()} onCreateStandaloneAgent={vi.fn()} onOpenChange={vi.fn()} />);
	await flows.get("host-a")?.onInitializeProject?.("/srv/todo-app");
	expect(a).toHaveBeenCalledWith("/api/v1/projects/initialize", { body: { path: "/srv/todo-app" } });
});
