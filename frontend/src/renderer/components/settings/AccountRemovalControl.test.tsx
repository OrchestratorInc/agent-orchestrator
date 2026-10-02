import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AccountRemovalControl } from "./AccountRemovalControl";

const api = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn(), DELETE: vi.fn() }));
vi.mock("../../lib/api-client", () => ({ apiClient: api }));
const impact = { accountId: "account-a", revision: 0, sessions: [
  { sessionId: "session-active", provider: "codex", bindingRevision: 7, stopped: false },
  { sessionId: "session-dormant", provider: "codex", bindingRevision: 3, stopped: true },
] };
const operation = { id: "remove-a", accountId: "account-a", phase: "requested", impact, canCancel: true, recoveryRequired: false, createdAt: "2026-09-28T00:00:00Z", updatedAt: "2026-09-28T00:00:00Z" };
const success = (data: unknown) => ({ data, response: new Response(null, { status: 200 }) });
function show() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  return { ...render(<QueryClientProvider client={client}><AccountRemovalControl accountId="account-a" /></QueryClientProvider>), client };
}

describe("coordinated account removal controls", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    api.GET.mockImplementation(async path => success(path.endsWith("removal-impact") ? impact : operation));
  });

  it("disables permanent removal after impact loads and never sends a mutation", async () => {
    const { client } = show();
    await waitFor(() => expect(client.isFetching()).toBe(0));
    const submit = screen.getByRole("button", { name: "Remove account" });
    expect(submit).toBeDisabled();
    expect(screen.getByText("Permanent account removal is unavailable until safe session shutdown can be verified. You can still inspect removal impact and recovery status.")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Removal impact" })).toHaveTextContent("Affected sessions (2)");
    fireEvent.click(submit);
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
    expect(localStorage.getItem("ao:account-removals:v1")).toBeNull();
  });

  it("shows durable removal failure after reload without claiming credential removal", async () => {
    localStorage.setItem("ao:account-removals:v1", JSON.stringify([{ accountId: "account-a", operationId: "remove-a" }]));
    let current = { ...operation, phase: "recovery_required", recoveryRequired: true, canCancel: false, errorCode: "SOURCE_STOP_UNCONFIRMED" };
    api.GET.mockImplementation(async path => success(path.endsWith("removal-impact") ? impact : current));
    show();
    const region = await screen.findByRole("region", { name: "Removal operation" });
    expect(region).toHaveTextContent("Error code: SOURCE_STOP_UNCONFIRMED");
    expect(screen.queryByText("Removal complete")).not.toBeInTheDocument();
    current = { ...current, phase: "complete", recoveryRequired: false };
    fireEvent.click(screen.getByRole("button", { name: "Refresh removal status" }));
    await waitFor(() => expect(region).toHaveTextContent("Phase: complete"));
    expect(region).not.toHaveTextContent("SOURCE_STOP_UNCONFIRMED");
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
  });

  it("shows the exact fresh impact revision while keeping destructive confirmation disabled", async () => {
    show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    expect(await screen.findByText("Impact revision: 0")).toBeInTheDocument();
    expect(screen.getByText("session-active (codex): stop not acknowledged")).toBeInTheDocument();
    expect(screen.getByText("session-dormant (codex): stop acknowledged")).toBeInTheDocument();
    expect(submit).toBeDisabled();
    fireEvent.click(submit);
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
  });

  it("shows unavailable removal capability and never uses the older direct delete route", async () => {
    api.GET.mockResolvedValue({ error: { requestId: "remove-unavailable", message: "private-token" }, response: new Response(null, { status: 501 }) });
    show();
    expect(await screen.findByRole("alert")).toHaveTextContent("remove-unavailable");
    expect(screen.getByRole("alert")).not.toHaveTextContent("private-token");
    expect(await screen.findByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
  });

  it("keeps submission disabled when the impact read reports stale state", async () => {
    api.GET.mockResolvedValue({ error: { requestId: "removal-conflict" }, response: new Response(null, { status: 409 }) });
    show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    expect(await screen.findByRole("alert")).toHaveTextContent("removal-conflict");
    fireEvent.click(submit);
    expect(api.POST).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(screen.queryByText("Removal complete")).not.toBeInTheDocument();
  });

  it("keeps an uncertain accepted request safe after remount without resubmitting", async () => {
    const id = "uncertain-remove-a";
    const saved = JSON.stringify([{ accountId: "account-a", operationId: id, request: { operationId: id, expectedRevision: 0, confirmed: true } }]);
    localStorage.setItem("ao:account-removals:v1", saved);
    api.GET.mockRejectedValue(new Error("response lost"));
    const view = show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    await screen.findByRole("alert");
    fireEvent.click(submit);
    expect(api.POST).not.toHaveBeenCalled();
    view.unmount();
    api.GET.mockResolvedValue({ error: { requestId: "unknown-removal" }, response: new Response(null, { status: 404 }) });
    show();
    expect(screen.queryByText(`Unconfirmed removal ID: ${id}`)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
    expect(localStorage.getItem("ao:account-removals:v1")).toBe(saved);
  });

  it("polls an existing removal without exposing recovery controls", async () => {
    localStorage.setItem("ao:account-removals:v1", JSON.stringify([{ accountId: "account-a", operationId: "remove-a" }]));
    const current = { ...operation, phase: "recovery_required", canCancel: false, recoveryRequired: true };
    api.GET.mockImplementation(async () => success(current));
    show();
    expect(screen.getByRole("button", { name: "Cancel" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Retry account removal" })).not.toBeInTheDocument();
    expect(api.DELETE).not.toHaveBeenCalled();
  });

  it("fails closed on corrupt recovery references before any removal request", async () => {
    localStorage.setItem("ao:account-removals:v1", "{");
    show();
    expect(await screen.findByRole("alert")).toHaveTextContent("Saved removal recovery is unavailable");
    expect(api.POST).not.toHaveBeenCalled();
    expect(api.DELETE).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
  });
});
