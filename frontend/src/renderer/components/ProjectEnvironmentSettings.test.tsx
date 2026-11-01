import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, expect, it, vi } from "vitest";
import { CloudProjectEnvironmentSettings, ProjectEnvironmentSettings } from "./ProjectEnvironmentSettings";

const { getMock, putMock, cloudGet, cloudPut } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), cloudGet: vi.fn(), cloudPut: vi.fn() }));
vi.mock("../lib/api-client", () => ({ apiClient: { GET: getMock, PUT: putMock }, apiErrorMessage: () => "request failed" }));
vi.mock("../hooks/useCloudCp", () => ({ useCloudCp: () => ({ client: { getProjectEnvironment: cloudGet, putProjectEnvironment: cloudPut }, ready: true, baseUrl: "https://cloud.test" }) }));

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { status: "ok", project: { id: "p", name: "Example", config: { env: { EXISTING: "old" }, autoReview: true } } } });
	putMock.mockReset().mockResolvedValue({ data: { status: "ok" } });
});

it("saves validated variables without dropping other project settings", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectEnvironmentSettings projectId="p" /></QueryClientProvider>);
	const value = await screen.findByLabelText("Value 1");
	expect(value).toHaveAttribute("type", "password");
	await userEvent.clear(value);
	await userEvent.type(value, "new");
	await userEvent.click(screen.getByRole("button", { name: "Add variable" }));
	await userEvent.type(screen.getByLabelText("Name 2"), "SECOND");
	await userEvent.type(screen.getByLabelText("Value 2"), "another");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body).toEqual({ displayName: "Example", config: { env: { EXISTING: "new", SECOND: "another" }, autoReview: true } });
});

it("imports pasted .env entries into the draft, replacing matching names without saving early", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectEnvironmentSettings projectId="p" /></QueryClientProvider>);
	await screen.findByLabelText("Value 1");
	await userEvent.click(screen.getByRole("button", { name: "Paste .env" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Paste .env" }), { target: { value: "# copied config\nexisting=replaced\nexport NEW_KEY=\"hello world\"\nURL=https://example.com/?a=b" } });
	fireEvent.submit(document.getElementById("project-settings-form")!);
	expect(screen.getByRole("alert")).toHaveTextContent("Add the pasted entries to the draft before saving");
	expect(putMock).not.toHaveBeenCalled();
	await userEvent.click(screen.getByRole("button", { name: "Add to draft" }));
	expect(putMock).not.toHaveBeenCalled();
	expect(screen.getByLabelText("Value 1")).toHaveValue("replaced");
	expect(screen.getByLabelText("Value 2")).toHaveAttribute("type", "password");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce());
	expect(putMock.mock.calls[0][1].body.config.env).toEqual({ EXISTING: "replaced", NEW_KEY: "hello world", URL: "https://example.com/?a=b" });
});

it("rejects a pasted reserved name without partially changing the draft", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	const onSaveState = vi.fn();
	render(<QueryClientProvider client={client}><ProjectEnvironmentSettings projectId="p" onSaveState={onSaveState} /></QueryClientProvider>);
	await screen.findByLabelText("Value 1");
	await userEvent.click(screen.getByRole("button", { name: "Paste .env" }));
	fireEvent.change(screen.getByRole("textbox", { name: "Paste .env" }), { target: { value: "NEW=good\nAO_SESSION_ID=spoof" } });
	await userEvent.click(screen.getByRole("button", { name: "Add to draft" }));
	expect(screen.getByRole("alert")).toHaveTextContent("Check line 2");
	expect(screen.queryByLabelText("Value 2")).not.toBeInTheDocument();
	await userEvent.click(screen.getByRole("button", { name: "Cancel" }));
	expect(screen.getByLabelText("Value 1")).toHaveValue("old");
	expect(putMock).not.toHaveBeenCalled();
	expect(onSaveState).toHaveBeenLastCalledWith(expect.objectContaining({ phase: "idle", dirty: false }));
});

it("autosaves a valid draft without a save button", async () => {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><ProjectEnvironmentSettings projectId="p" /></QueryClientProvider>);
	const value = await screen.findByLabelText("Value 1");
	expect(screen.queryByRole("button", { name: "Save changes" })).not.toBeInTheDocument();
	await userEvent.clear(value);
	await userEvent.type(value, "auto");
	await waitFor(() => expect(putMock).toHaveBeenCalledOnce(), { timeout: 3000 });
	expect(putMock.mock.calls[0][1].body.config.env).toEqual({ EXISTING: "auto" });
});

it("saves a Cloud project's variables to the control plane, never the local daemon", async () => {
	cloudGet.mockReset().mockResolvedValue({ editable: true, variables: [{ name: "API_KEY", value: "old" }] });
	cloudPut.mockReset().mockImplementation(async (_org: string, _project: string, variables: Record<string, string>) => ({
		editable: true, variables: Object.entries(variables).map(([name, value]) => ({ name, value })),
	}));
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><CloudProjectEnvironmentSettings projectId="p" cloudOrgId="org" /></QueryClientProvider>);
	const value = await screen.findByLabelText("Value 1");
	expect(screen.getByText(/Stored encrypted/)).toBeInTheDocument();
	await userEvent.clear(value);
	await userEvent.type(value, "new");
	fireEvent.submit(document.getElementById("project-settings-form")!);
	await waitFor(() => expect(cloudPut).toHaveBeenCalledWith("org", "p", { API_KEY: "new" }));
	expect(getMock).not.toHaveBeenCalled();
	expect(putMock).not.toHaveBeenCalled();
});

it("shows a Cloud member who cannot change them only the variables' names", async () => {
	cloudGet.mockReset().mockResolvedValue({ editable: false, variables: [{ name: "API_KEY" }] });
	cloudPut.mockReset();
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(<QueryClientProvider client={client}><CloudProjectEnvironmentSettings projectId="p" cloudOrgId="org" /></QueryClientProvider>);
	expect(await screen.findByText("API_KEY")).toBeInTheDocument();
	expect(screen.getByText(/Only organization owners and admins/)).toBeInTheDocument();
	expect(screen.queryByRole("button", { name: "Add variable" })).not.toBeInTheDocument();
	expect(screen.queryByLabelText("Value 1")).not.toBeInTheDocument();
	fireEvent.submit(document.getElementById("project-settings-form")!);
	expect(cloudPut).not.toHaveBeenCalled();
});
