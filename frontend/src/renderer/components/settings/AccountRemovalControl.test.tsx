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
  return render(<QueryClientProvider client={client}><AccountRemovalControl accountId="account-a" /></QueryClientProvider>);
}

describe("coordinated account removal controls", () => {
  beforeEach(() => {
    vi.resetAllMocks();
    localStorage.clear();
    api.GET.mockImplementation(async path => success(path.endsWith("removal-impact") ? impact : operation));
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

  it("submits one confirmation with the exact fresh impact revision", async () => {
    show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    await waitFor(() => expect(submit).toBeEnabled());
    let resolve!: (value: unknown) => void;
    api.POST.mockImplementation(() => new Promise(done => { resolve = done; }));
    fireEvent.click(submit);
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    const body = api.POST.mock.calls[0][1].body;
    expect(api.POST.mock.calls[0][0]).toBe("/api/v1/accounts-manager/accounts/{accountId}/removals");
    expect(body).toEqual({ operationId: expect.any(String), expectedRevision: 0, confirmed: true });
    const accepted = { ...operation, id: body.operationId };
    api.GET.mockImplementation(async path => success(path.endsWith("removal-impact") ? impact : accepted));
    resolve(success(accepted));
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
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

  it("rejects a stale impact revision and requires renewed confirmation", async () => {
    api.POST.mockResolvedValue({ error: { requestId: "removal-conflict" }, response: new Response(null, { status: 409 }) });
    show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    expect(await screen.findByRole("alert")).toHaveTextContent("removal-conflict");
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(screen.queryByText("Removal complete")).not.toBeInTheDocument();
  });

  it("keeps an uncertain accepted request safe after remount without resubmitting", async () => {
    api.POST.mockRejectedValue(new Error("response lost"));
    const view = show();
    const submit = await screen.findByRole("button", { name: "Remove account" });
    await waitFor(() => expect(submit).toBeEnabled());
    fireEvent.click(submit);
    await waitFor(() => expect(api.POST).toHaveBeenCalledOnce());
    const id = api.POST.mock.calls[0][1].body.operationId;
    view.unmount();
    api.GET.mockResolvedValue({ error: { requestId: "unknown-removal" }, response: new Response(null, { status: 404 }) });
    show();
    expect(screen.queryByText(`Unconfirmed removal ID: ${id}`)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    expect(api.POST).toHaveBeenCalledOnce();
    expect(api.DELETE).not.toHaveBeenCalled();
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
