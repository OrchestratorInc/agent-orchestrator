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
import { useUiStore } from "../stores/ui-store";
import { sessionUiKey } from "../lib/hosts";

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
	useUiStore.setState({ provisioningProjectIds: new Set(), orchestratorStartupErrors: {} });
});

it("shows remote provisioning and startup failure only for the creating host", async () => {
	let rejectOrchestrator!: (error: Error) => void;
	const orchestrator = new Promise<never>((_resolve, reject) => { rejectOrchestrator = reject; });
	requests.set("host-a", { POST: vi.fn((path: string) => path === "/api/v1/projects"
		? Promise.resolve({ data: { project: { id: "same-project-id" } } })
		: orchestrator) });
	render(<RemoteAddProjectDialog hostId="host-a" hostLabel="Host A" connected onCreated={vi.fn()} onCreateStandaloneAgent={vi.fn()} onOpenChange={vi.fn()} />);
	await act(async () => { await flows.get("host-a")?.onCreateProject(input); });
	const a = sessionUiKey("same-project-id", "host-a");
	const b = sessionUiKey("same-project-id", "host-b");
	expect(useUiStore.getState().provisioningProjectIds.has(a)).toBe(true);
	expect(useUiStore.getState().provisioningProjectIds.has(b)).toBe(false);
	await act(async () => { rejectOrchestrator(new Error("Agent unavailable")); });
	await waitFor(() => expect(useUiStore.getState().provisioningProjectIds.has(a)).toBe(false));
	expect(useUiStore.getState().orchestratorStartupErrors[a]).toContain("Agent unavailable");
	expect(useUiStore.getState().orchestratorStartupErrors[b]).toBeUndefined();
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
	// The first callback means durable registration; the second carries the started orchestrator.
	expect(aCreated).toHaveBeenNthCalledWith(1, "same-project-id");
	expect(bCreated).toHaveBeenNthCalledWith(1, "same-project-id");
	await waitFor(() => {
		expect(aCreated).toHaveBeenNthCalledWith(2, "same-project-id", "host-a-orchestrator");
		expect(bCreated).toHaveBeenNthCalledWith(2, "same-project-id", "host-b-orchestrator");
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
