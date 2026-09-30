import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WorkspaceSession } from "../types/workspace";
import { SwitchAgentDialog } from "./SwitchAgentDialog";
import { TooltipProvider } from "./ui/tooltip";

const mocks = vi.hoisted(() => ({
	GET: vi.fn(), POST: vi.fn(), agent: vi.fn(), recover: vi.fn(),
	nativePending: false,
}));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: mocks.GET, POST: mocks.POST }, apiErrorMessage: () => "Request failed",
}));
vi.mock("../hooks/useSwitchAgent", async importOriginal => ({
	...await importOriginal<typeof import("../hooks/useSwitchAgent")>(),
	useSwitchAgent: () => ({ mutate: mocks.agent }),
	useRecoverAgentSwitch: () => ({ mutate: mocks.recover, isPending: false }),
	useSwitchAgentState: () => ({ isPending: mocks.nativePending, error: null }),
}));
vi.mock("../hooks/useAccountsManagerQuery", () => ({
	useAccountsManagerQuery: () => ({ data: {
		revision: 4, availability: "ready", stale: false,
		accounts: ["a", "b"].map(id => ({ id: "account-" + id, provider: "codex", label: "account-" + id, email: id + "@example.test", verification: "verified", status: "active", unavailable: false, disabled: false, quotaSupported: false })),
	} }),
}));

const session: WorkspaceSession = {
	id: "session-a", provider: "codex", kind: "worker", mode: "tui", status: "working",
	activity: { state: "active", lastActivityAt: "2026-09-30T00:00:00Z" },
	branch: "test", prs: [], title: "Switch controls", updatedAt: "2026-09-30T00:00:00Z",
	workspaceId: "project-a", workspaceName: "Test", terminalHandleId: "test-terminal",
};
const binding = { sessionId: session.id, provider: "codex", mode: "managed", accountId: "account-a", revision: 7, blocked: false };
const operation = { id: "operation-a", sessionId: session.id, provider: "codex", sourceMode: "managed", sourceAccountId: "account-a", sourceRevision: 7, targetMode: "managed", targetAccountId: "account-b", targetRevision: 0, policy: "drain", newConversation: false, phase: "waiting", canRetry: false, recoveryRequired: false, createdAt: "2026-09-30T00:00:00Z", updatedAt: "2026-09-30T00:00:00Z" };
const ok = (data: unknown) => ({ data, response: new Response(null, { status: 200 }) });

function show(overrides: Partial<WorkspaceSession> = {}, includeAccountControls = true) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	client.setQueryData(["project", session.workspaceId], { config: {} });
	const onOpenChange = vi.fn();
	return { client, onOpenChange, ...render(<QueryClientProvider client={client}><TooltipProvider>
		<SwitchAgentDialog includeAccountControls={includeAccountControls} container={document.body} open session={{ ...session, ...overrides }} onOpenChange={onOpenChange} />
	</TooltipProvider></QueryClientProvider>) };
}

beforeEach(() => {
	vi.resetAllMocks();
	localStorage.clear();
	mocks.nativePending = false;
	mocks.GET.mockImplementation(async (path: string, args) => {
		if (path === "/api/v1/sessions/{sessionId}/account") return ok(binding);
		if (path === "/api/v1/agents/{agent}/models") return ok({ agentId: args.params.path.agent, models: [{ id: "test-model", label: "Test model", isDefault: true }], source: "test", selectionMode: "catalog", stale: false });
		return ok({ config: {} });
	});
});


describe("compact agent and account switching", () => {
 it("keeps the original compact agent row with an account action and no tabs", async () => {
  show();
  expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Target agent" })).toBeInTheDocument();
  expect(await screen.findByRole("button", { name: "Model" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Switch" })).toBeInTheDocument();
  expect(screen.getByRole("button", { name: "Switch account" })).toBeInTheDocument();
  expect(mocks.GET.mock.calls.some(([path]) => path.endsWith("/account"))).toBe(false);
 });

 it("opens the account popup when the current agent is chosen, without native handoff", async () => {
  show();
  await userEvent.click(screen.getByRole("button", { name: "Target agent" }));
  const current = screen.getByRole("menuitem", { name: /^Codex/ });
  expect(current).not.toHaveAttribute("data-disabled");
  await userEvent.click(current);
  const popup = await screen.findByRole("dialog", { name: "Switch account" });
  expect(await within(popup).findByLabelText("Target account")).toBeVisible();
  expect(within(popup).getByRole("button", { name: "Switch account" })).toBeDisabled();
  expect(screen.queryByRole("tablist")).not.toBeInTheDocument();
  expect(mocks.agent).not.toHaveBeenCalled();
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it("uses provider-wide model settings for a projectless session", async () => {
  show({ workspaceId: "__standalone__" });
  await waitFor(() => expect(mocks.GET.mock.calls.some(([path]) => path === "/api/v1/agents/{agent}/models")).toBe(true));
  expect(mocks.GET.mock.calls.some(([path]) => path === "/api/v1/projects/{id}")).toBe(false);
  const models = mocks.GET.mock.calls.filter(([path]) => path === "/api/v1/agents/{agent}/models");
  for (const [, args] of models) expect(args.params.query.projectId).toBeUndefined();
 });

 it("keeps identity readable and technical fields collapsed until requested", async () => {
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await screen.findByLabelText("Target account");
  expect(screen.getByRole("option", { name: /b@example.test/ })).toBeInTheDocument();
  expect(within(screen.getByRole("region", { name: "Committed account" })).getByText(/a@example.test/)).toBeVisible();
  expect(screen.getByText(/Revision: 7/)).not.toBeVisible();
  expect(screen.getByLabelText("Start a new conversation")).not.toBeVisible();
  await userEvent.click(screen.getByText("More options", { exact: true }));
  expect(screen.getByText(/Revision: 7/)).toBeVisible();
  expect(screen.getByLabelText("Start a new conversation")).toBeVisible();
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it.each(["drain", "interrupt"])("submits an explicit account and %s timing only to the account route", async policy => {
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await screen.findByLabelText("Target account");
  const submit = screen.getByRole("button", { name: "Switch account" });
  fireEvent.change(screen.getByLabelText("Target account"), { target: { value: "managed:account-b" } });
  expect(submit).toBeDisabled();
  fireEvent.change(screen.getByLabelText("Switch timing"), { target: { value: policy } });
  let resolve!: (value: unknown) => void;
  mocks.POST.mockImplementation(() => new Promise(done => { resolve = done; }));
  await userEvent.click(submit);
  await waitFor(() => expect(mocks.POST).toHaveBeenCalledOnce());
  expect(mocks.POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/account-switches", {
   params: { path: { sessionId: session.id } },
   body: { operationId: expect.any(String), expectedRevision: 7, mode: "managed", accountId: "account-b", policy, newConversation: false },
  });
  expect(screen.getByRole("button", { name: "Back to agent" })).toBeDisabled();
  const accepted = { ...operation, policy, id: mocks.POST.mock.calls[0][1].body.operationId };
  mocks.GET.mockImplementation(async path => ok(path.endsWith("/{operationId}") ? accepted : { ...binding, switch: accepted }));
  resolve(ok(accepted));
  expect(await screen.findByRole("region", { name: "Switch operation" })).toHaveTextContent("waiting");
  expect(within(screen.getByRole("region", { name: "Committed account" })).getByText(/a@example.test/)).toBeVisible();
  expect(mocks.agent).not.toHaveBeenCalled();
 });

 it("retains an unsubmitted draft while returning to the agent picker", async () => {
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await screen.findByLabelText("Target account");
  fireEvent.change(screen.getByLabelText("Target account"), { target: { value: "managed:account-b" } });
  fireEvent.change(screen.getByLabelText("Switch timing"), { target: { value: "drain" } });
  await userEvent.click(screen.getByRole("button", { name: "Back to agent" }));
  expect(screen.getByRole("button", { name: "Target agent" })).toBeVisible();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  expect(screen.getByLabelText("Target account")).toHaveValue("managed:account-b");
  expect(screen.getByLabelText("Switch timing")).toHaveValue("drain");
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it("opens the account popup using the keyboard without submitting", async () => {
  show();
  screen.getByRole("button", { name: "Switch account" }).focus();
  await userEvent.keyboard("{Enter}");
  expect(await screen.findByRole("dialog", { name: "Switch account" })).toBeVisible();
  await screen.findByLabelText("Target account");
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it("cancels the recorded operation without an optimistic account change", async () => {
  let observed = { ...operation };
  mocks.GET.mockImplementation(async path => ok(path.endsWith("/{operationId}") ? observed : { ...binding, switch: observed }));
  mocks.POST.mockImplementation(async () => { observed = { ...observed, phase: "cancelled" }; return ok(observed); });
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await screen.findByRole("button", { name: "Cancel account switch" });
  expect(screen.getByRole("button", { name: "Back to agent" })).toBeDisabled();
  await userEvent.click(screen.getByRole("button", { name: "Cancel account switch" }));
  await waitFor(() => expect(screen.getByRole("region", { name: "Switch operation" })).toHaveTextContent("cancelled"));
  expect(mocks.POST).toHaveBeenCalledWith("/api/v1/sessions/{sessionId}/account-switches/{operationId}/cancel", { params: { path: { sessionId: session.id, operationId: operation.id } } });
  expect(within(screen.getByRole("region", { name: "Committed account" })).getByText(/a@example.test/)).toBeVisible();
  await waitFor(() => expect(screen.getByRole("button", { name: "Back to agent" })).toBeEnabled());
 });

 it("preserves unknown operations and locks handoff after reopening the account popup", async () => {
  mocks.POST.mockRejectedValue(new Error("private-transport-detail"));
  const first = show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await screen.findByLabelText("Target account");
  fireEvent.change(screen.getByLabelText("Target account"), { target: { value: "managed:account-b" } });
  fireEvent.change(screen.getByLabelText("Switch timing"), { target: { value: "drain" } });
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await waitFor(() => expect(mocks.POST).toHaveBeenCalledOnce());
  const id = mocks.POST.mock.calls[0][1].body.operationId;
  await waitFor(() => expect(screen.getByRole("button", { name: "Back to agent" })).toBeDisabled());
  expect(screen.queryByText("private-transport-detail", { exact: false })).not.toBeInTheDocument();
  first.unmount();
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  await waitFor(() => expect(screen.getByRole("button", { name: "Back to agent" })).toBeDisabled());
  expect(screen.getByText(id, { exact: false })).toBeInTheDocument();
  expect(mocks.POST).toHaveBeenCalledOnce();
 });

 it("keeps native handoff usable when account control is unavailable", async () => {
  mocks.GET.mockImplementation(async path => path.endsWith("/account")
   ? { error: { requestId: "unavailable-79", message: "private-endpoint" }, response: new Response(null, { status: 501 }) }
   : ok({ models: [], config: {} }));
  show();
  await userEvent.click(screen.getByRole("button", { name: "Switch account" }));
  expect(await screen.findByRole("alert")).toHaveTextContent("unavailable-79");
  expect(screen.queryByText("private-endpoint", { exact: false })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Back to agent" }));
  await userEvent.click(screen.getByRole("button", { name: "Switch" }));
  expect(mocks.agent).toHaveBeenCalledOnce();
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it("disables account entry during native handoff admission", () => {
  mocks.nativePending = true;
  show();
  expect(screen.getByRole("button", { name: "Switch account" })).toBeDisabled();
 });

 it("opens account controls directly for an inactive local session", async () => {
  show({ isTerminated: true, status: "terminated" });
  expect(await screen.findByRole("dialog", { name: "Switch account" })).toBeVisible();
  expect(await screen.findByLabelText("Target account")).toBeVisible();
  expect(screen.queryByRole("button", { name: "Back to agent" })).not.toBeInTheDocument();
 });

 it("keeps native recovery authoritative instead of showing account switching", async () => {
  show({ isTerminated: true, status: "terminated", activeAgentSwitch: {
   id: "native-recovery", fromHarness: "codex", targetHarness: "fx",
   state: "stopping_source", errorCode: "source_stop_unconfirmed", agentHandoffStatus: "received",
  } });
  expect(screen.getByRole("dialog", { name: "Switch agent" })).toBeVisible();
  expect(screen.queryByRole("button", { name: "Switch account" })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole("button", { name: "Check Codex" }));
  expect(mocks.recover).toHaveBeenCalledWith({ sessionId: session.id, switchId: "native-recovery" });
  expect(mocks.POST).not.toHaveBeenCalled();
 });

 it.each([{ cloud: { orgId: "test-org" } }, { provider: "goose" }] satisfies Partial<WorkspaceSession>[] )("does not expose local account controls for unsupported session %j", overrides => {
  show(overrides);
  expect(screen.queryByRole("button", { name: "Switch account" })).not.toBeInTheDocument();
  expect(mocks.GET.mock.calls.some(([path]) => path.endsWith("/account"))).toBe(false);
 });
});
