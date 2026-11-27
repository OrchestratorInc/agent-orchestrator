import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { CloudModelCatalogWarmer } from "../components/CloudModelCatalogWarmer";
import { useCloudModelCatalog } from "./useCloudModelCatalog";

const listUserAgentModels = vi.hoisted(() => vi.fn());
const state = vi.hoisted(() => ({
	ready: true,
	orgId: "org-1" as string | undefined,
	connections: [] as Array<{ id: string; provider: string; label: string; validationState: string; updatedAt: string }>,
}));
vi.mock("./useCloudCp", () => ({
	useCloudCp: () => ({ ready: state.ready, baseUrl: "https://cp.test", userId: "user-1", client: { listUserAgentModels } }),
}));
vi.mock("./useCloudOrg", () => ({
	useCloudOrg: () => ({ org: state.orgId ? { id: state.orgId } : undefined }),
}));
vi.mock("./useProviderConnections", () => ({
	useProviderConnections: () => ({ data: state.connections, isError: false }),
}));

const connection = (provider: string, updatedAt = "t1") => ({ id: `c-${provider}`, provider, label: "default", validationState: "valid", updatedAt });
const catalog = (id: string) => ({ source: "worker", models: [{ id, displayName: id, default: true }] });

function Probe({ agent, onModels }: { agent: string; onModels: (ids: string[] | undefined) => void }) {
	const { catalog: list } = useCloudModelCatalog({ orgId: "org-1", agent, enabled: true });
	onModels(list?.models.map((model) => model.id));
	return null;
}

describe("useWarmCloudModelCatalogs", () => {
	beforeEach(() => {
		listUserAgentModels.mockReset();
		listUserAgentModels.mockImplementation(async (_org: string, agent: string) => catalog(`${agent}-model`));
		state.ready = true;
		state.orgId = "org-1";
		state.connections = [];
	});

	it("loads the models of every logged-in Cloud harness, and only those", async () => {
		state.connections = [connection("codex"), { ...connection("cursor"), validationState: "invalid" }];
		render(<QueryClientProvider client={new QueryClient()}><CloudModelCatalogWarmer /></QueryClientProvider>);
		await waitFor(() => expect(listUserAgentModels).toHaveBeenCalledTimes(1));
		expect(listUserAgentModels).toHaveBeenCalledWith("org-1", "codex", expect.anything());
	});

	it("loads a harness's models as soon as it is logged in", async () => {
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		expect(listUserAgentModels).not.toHaveBeenCalled();

		// A login refreshes the connections, which now hold the harness.
		state.connections = [connection("claude-code")];
		view.rerender(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		await waitFor(() => expect(listUserAgentModels).toHaveBeenCalledWith("org-1", "claude-code", expect.anything()));

		// Logging in again (a new credential) loads the list again.
		state.connections = [connection("claude-code", "t2")];
		view.rerender(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		await waitFor(() => expect(listUserAgentModels).toHaveBeenCalledTimes(2));
	});

	it("hands the loaded list to a picker at once, without asking Cloud again", async () => {
		state.connections = [connection("codex")];
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		await waitFor(() => expect(listUserAgentModels).toHaveBeenCalledTimes(1));
		await waitFor(() => expect(client.isFetching()).toBe(0));

		const seen: Array<string[] | undefined> = [];
		view.rerender(<QueryClientProvider client={client}><CloudModelCatalogWarmer /><Probe agent="codex" onModels={(ids) => seen.push(ids)} /></QueryClientProvider>);
		expect(seen[0]).toEqual(["codex-model"]);
		expect(listUserAgentModels).toHaveBeenCalledTimes(1);
	});

	it("asks nothing while signed out of Cloud or without an organization", async () => {
		state.connections = [connection("codex")];
		state.ready = false;
		const client = new QueryClient();
		const view = render(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		state.ready = true;
		state.orgId = undefined;
		view.rerender(<QueryClientProvider client={client}><CloudModelCatalogWarmer /></QueryClientProvider>);
		await new Promise((done) => setTimeout(done, 10));
		expect(listUserAgentModels).not.toHaveBeenCalled();
	});
});
