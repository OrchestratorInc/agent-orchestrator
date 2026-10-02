import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { createAppI18n, type AppLocale } from "../../i18n";
import { AccountsManagerSection } from "./AccountsManagerSection";
import { accountsManagerQueryKey } from "../../hooks/useAccountsManagerQuery";
import { AccountControlError } from "../../lib/accounts-manager-controls";

const mocks = vi.hoisted(() => ({
  GET: vi.fn(),
  POST: vi.fn(),
  DELETE: vi.fn(),
  openExternal: vi.fn(),
  startOAuth: vi.fn(),
  cancelOAuth: vi.fn(),
  addKey: vi.fn(),
  updateRouting: vi.fn(),
  rename: vi.fn(),
  setDisabled: vi.fn(),
  models: vi.fn(),
	quota: vi.fn(),
  queryError: null as Error | null,
  snapshot: {
    revision: 1,
    availability: "ready",
    stale: false,
    accounts: [],
    oauthSessions: [],
    routing: [],
  } as Record<string, unknown>,
}));

vi.mock("../../lib/api-client", async (importOriginal) => ({
  ...await importOriginal<typeof import("../../lib/api-client")>(),
  apiClient: { GET: mocks.GET, POST: mocks.POST, DELETE: mocks.DELETE },
}));

vi.mock("../../lib/bridge", () => ({
  aoBridge: { app: { openExternal: mocks.openExternal } },
}));
vi.mock("../../hooks/useAccountsManagerQuery", async () => {
  const actual = await vi.importActual<
    typeof import("../../hooks/useAccountsManagerQuery")
  >("../../hooks/useAccountsManagerQuery");
  return {
    ...actual,
    useAccountsManagerEvents: () => undefined,
    useAccountsManagerQuery: () => ({
      data: mocks.snapshot,
      isLoading: false,
      isError: Boolean(mocks.queryError),
      error: mocks.queryError,
    }),
    startAccountsManagerOAuth: mocks.startOAuth,
    cancelAccountsManagerOAuth: mocks.cancelOAuth,
    addAccountsManagerAPIKey: mocks.addKey,
    updateAccountsManagerRouting: mocks.updateRouting,
    renameAccountsManagerAccount: mocks.rename,
    setAccountsManagerDisabled: mocks.setDisabled,
    fetchAccountsManagerModels: mocks.models,
		fetchAccountsManagerQuota: mocks.quota,
  };
});

function renderSection(locale: AppLocale = "en", client = new QueryClient()) {
  const tree = () => (
    <I18nextProvider i18n={createAppI18n(locale)}>
      <QueryClientProvider client={client}>
        <AccountsManagerSection />
      </QueryClientProvider>
    </I18nextProvider>
  );
  const view = render(tree());
  return { ...view, refresh: () => view.rerender(tree()) };
}

async function clickAccountAction(name: string) {
  const user = userEvent.setup();
  await user.click(screen.getByRole("button", { name: /More account actions|Más acciones de cuenta/ }));
  await user.click(await screen.findByRole("menuitem", { name }));
}

describe("AccountsManagerSection", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    mocks.queryError = null;
    mocks.cancelOAuth.mockResolvedValue(undefined);
    mocks.models.mockResolvedValue({ models: [] });
		mocks.quota.mockResolvedValue({ observedAt:"2026-09-28T00:00:00Z",groups:[],summary:[],serverTimeOffsetMs:0 });
    mocks.snapshot = {
      revision: 1,
      availability: "ready",
      stale: false,
      accounts: [],
      oauthSessions: [],
      routing: [],
    };
  });

  it("shows a stale inventory read error and its request ID without permitting cached mutations", () => {
    mocks.queryError = new AccountControlError(503, "inventory-79");
    renderSection();
    expect(screen.getByRole("alert")).toHaveTextContent("inventory-79");
    expect(screen.getByRole("button", { name: "Add codex account" })).toBeDisabled();
  });

  it("labels a legacy sign-in token without claiming an API key or changing its account", async () => {
    mocks.snapshot.accounts = [{ id: "legacy-token", provider: "codex", kind: "access_token", status: "active", generation: 4, verification: "verified", quotaSupported: false, cooldowns: [] }];
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: /Codex sign-in token/ }));
    expect(screen.getByText("Stored sign-in token. Its credential format has not been converted. Isolated native-token profiles are not available yet.")).toBeInTheDocument();
    expect(screen.queryByText(/Codex API key/)).not.toBeInTheDocument();
    expect(mocks.quota).not.toHaveBeenCalled();
    expect(mocks.setDisabled).not.toHaveBeenCalled();
    expect(mocks.updateRouting).not.toHaveBeenCalled();
    expect(mocks.snapshot.accounts).toHaveLength(1);
  });

  it("explains wrong-method token input and preserves its request ID", async () => {
    const transport = await vi.importActual<typeof import("../../hooks/useAccountsManagerQuery")>("../../hooks/useAccountsManagerQuery");
    mocks.addKey.mockImplementation(transport.addAccountsManagerAPIKey);
    mocks.POST.mockResolvedValue({ error: { code: "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED", requestId: "credential-format-79", message: "private-token" }, response: new Response(null, { status: 400 }) });
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: /API key/ }));
    fireEvent.change(screen.getByLabelText("API key"), { target: { value: "synthetic-token" } });
    fireEvent.click(screen.getByRole("button", { name: "Add account" }));
    expect(await screen.findByText("Sign-in tokens are not API keys. Use native sign-in in Harnesses. This does not connect a managed account; isolated native-token profiles are not available yet.")).toBeInTheDocument();
    expect(screen.getByText("Request ID: credential-format-79")).toBeInTheDocument();
    expect(screen.getByLabelText("API key")).toHaveValue("");
    expect(mocks.snapshot.accounts).toEqual([]);
  });

	it("shows observed usage and reset time in expanded account details", async () => {
		mocks.snapshot.accounts = [{id:"saved-a",provider:"codex",kind:"oauth",label:"Work",status:"active",generation:4,verification:"verified",quotaSupported:true,cooldowns:[]}];
		mocks.quota.mockResolvedValue({observedAt:"2026-09-28T00:00:00Z",summary:[],serverTimeOffsetMs:0,groups:[{displayName:"Account",buckets:[{window:"18000s",remainingFraction:.75,resetTime:"2030-01-01T00:00:00Z",description:""}]}]});
		renderSection();
		fireEvent.click(screen.getByRole("button",{name:/Work/}));
		expect(await screen.findByText(/75% remaining/)).toBeInTheDocument();
		expect(screen.getByText(/Last checked:/)).toBeInTheDocument();
		expect(screen.getByText(/Resets:/)).toBeInTheDocument();
		expect(mocks.quota).toHaveBeenCalledWith("saved-a",expect.any(AbortSignal));
		expect(screen.queryByRole("button",{name:"Reset quota"})).not.toBeInTheDocument();
	});

	it("keeps unverified saved credentials out of default selection", async () => {
		mocks.snapshot.accounts = [{id:"saved-a",provider:"codex",kind:"api_key",label:"Work",status:"active",generation:4,verification:"unverified",quotaSupported:false,cooldowns:[]}];
		renderSection();
		expect(screen.getByText("Not verified")).toBeInTheDocument();
		expect(screen.getByRole("button", {name:"Use as default"})).toBeDisabled();
		const user = userEvent.setup();
		await user.click(screen.getByRole("button", { name: "More account actions" }));
		expect(await screen.findByRole("menuitem",{name:"Verify credential"})).toBeInTheDocument();
	});

  it("shows a refresh failure request ID without raw diagnostics or changed account state", async () => {
    mocks.snapshot.accounts = [{ id: "saved-a", provider: "codex", kind: "oauth", label: "Work", status: "active", verification: "verified", generation: 4, cooldowns: [] }];
    mocks.POST.mockResolvedValue({ error: { requestId: "refresh-row-79", message: "private-token http://127.0.0.1:9999" }, response: new Response(null, { status: 503 }) });
    renderSection();
    await clickAccountAction("Refresh account");
    expect(await screen.findByText("Request ID: refresh-row-79")).toBeInTheDocument();
    expect(screen.queryByText("private-token", { exact: false })).not.toBeInTheDocument();
    expect(mocks.snapshot.accounts).toHaveLength(1);
  });

  it("shows removal impact and an unavailable notice without deleting", async () => {
    mocks.snapshot.accounts = [{ id: "saved-a", provider: "codex", kind: "api_key", label: "Work", status: "active", verification: "verified", generation: 4, cooldowns: [] }];
    mocks.GET.mockResolvedValue({ data: { accountId: "saved-a", revision: 0, sessions: [] }, response: new Response(null, { status: 200 }) });
    renderSection();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "More account actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove account" }));
    expect(await screen.findByText("Review affected sessions and removal status.")).toBeInTheDocument();
    expect(mocks.GET).toHaveBeenCalledWith("/api/v1/accounts-manager/accounts/{accountId}/removal-impact", expect.objectContaining({ params: { path: { accountId: "saved-a" } } }));
    expect(mocks.DELETE).not.toHaveBeenCalled();
    expect(mocks.POST).not.toHaveBeenCalled();
    expect(mocks.snapshot.accounts).toHaveLength(1);
  });

  it("keeps the last account visible while permanent removal is disabled", async () => {
    mocks.snapshot.accounts = [{ id: "saved-a", provider: "codex", kind: "api_key", label: "Work", status: "active", verification: "verified", generation: 4, cooldowns: [] }];
    mocks.GET.mockImplementation(async path => ({ data: path.endsWith("removal-impact")
      ? { accountId: "saved-a", revision: 0, sessions: [] }
      : { id: "remove-a", accountId: "saved-a", phase: "requested", impact: { accountId: "saved-a", revision: 0, sessions: [] }, canCancel: true, recoveryRequired: false, createdAt: "2026-09-28T00:00:00Z", updatedAt: "2026-09-28T00:00:00Z" }, response: new Response(null, { status: 200 }) }));
    mocks.POST.mockImplementation(async (_path, { body }) => ({ data: { id: body.operationId, accountId: "saved-a", phase: "requested", impact: { accountId: "saved-a", revision: 0, sessions: [] }, canCancel: true, recoveryRequired: false, createdAt: "2026-09-28T00:00:00Z", updatedAt: "2026-09-28T00:00:00Z" }, response: new Response(null, { status: 200 }) }));
    renderSection();
    const user = userEvent.setup();
    await user.click(screen.getByRole("button", { name: "More account actions" }));
    await user.click(await screen.findByRole("menuitem", { name: "Remove account" }));
    await screen.findByText("No sessions are using this account.");
    expect(screen.getByRole("button", { name: "Remove account" })).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: "Remove account" }));
    expect(mocks.POST).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Cancel" }));
    const codexGroup = document.querySelector('[data-agent-provider="codex"]');
    expect(codexGroup).not.toBeNull();
    expect(within(codexGroup as HTMLElement).getByRole("button", { name: "Add codex account" })).toBeInTheDocument();
    expect(within(codexGroup as HTMLElement).getByRole("button", { name: /Work/ })).toBeInTheDocument();
    expect(mocks.DELETE).not.toHaveBeenCalled();
  });

  it.each(["pending", "pruned"])("closes the cancelled %s sign-in flow and keeps saved accounts", async state => {
    mocks.snapshot.accounts = [{ id: "saved-a", provider: "codex", kind: "api_key", label: "Work", status: "active", verification: "verified", generation: 4, cooldowns: [] }];
    mocks.snapshot.oauthSessions = state === "pending" ? [{ id: "login-a", provider: "codex", status: "pending" }] : [];
    mocks.startOAuth.mockResolvedValue({ id: "login-a", authorizationUrl: "https://provider.example/login", provider: "codex", mode: "device", status: "pending", userCode: "ABCD-EFGH", expiresAt: new Date(Date.now() + 600_000).toISOString() });
    mocks.openExternal.mockResolvedValue(undefined);
    renderSection();
    if (state === "pruned") {
      fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
      fireEvent.click(screen.getByRole("button", { name: /Device sign-in/ }));
      await waitFor(() => expect(mocks.openExternal).toHaveBeenCalledOnce());
    }
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument());
    expect(screen.queryByText("Sign-in cancelled")).not.toBeInTheDocument();
    expect(screen.getByText("Work")).toBeInTheDocument();
    expect(mocks.snapshot.accounts).toHaveLength(1);
    expect(mocks.DELETE).not.toHaveBeenCalled();
  });

  it("renames an account with its displayed generation and filters labels", async () => {
    mocks.snapshot.accounts = [
      {
        id: "saved-a",
        provider: "codex",
        kind: "api_key",
        label: "Work",
        status: "active", verification: "verified",
        disabled: false,
        unavailable: false,
        generation: 4,
        reconnectSupported: false,
        cooldowns: [],
      },
    ];
    mocks.rename.mockResolvedValue({ ...mocks.snapshot, revision: 2 });
    renderSection();
    fireEvent.change(
      screen.getByRole("textbox", { name: "Search saved accounts" }),
      { target: { value: "absent" } },
    );
    expect(screen.getByText("No matching accounts.")).toBeInTheDocument();
    fireEvent.change(
      screen.getByRole("textbox", { name: "Search saved accounts" }),
      { target: { value: "work" } },
    );
    fireEvent.click(screen.getByRole("button", { name: /Work Verified/ }));
    await clickAccountAction("Rename");
    fireEvent.change(screen.getByRole("textbox", { name: "Account label" }), {
      target: { value: "Production" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save label" }));
    await waitFor(() =>
      expect(mocks.rename).toHaveBeenCalledWith("saved-a", "Production", 4),
    );
    expect(mocks.updateRouting).not.toHaveBeenCalled();
    expect(mocks.startOAuth).not.toHaveBeenCalled();
  });

  it("allows API-key disablement independently of sign-in support", async () => {
    mocks.snapshot.accounts = [
      {
        id: "key",
        provider: "codex",
        kind: "api_key",
        status: "active", verification: "verified",
        disabled: false,
        unavailable: false,
        generation: 1,
        reconnectSupported: false,
        cooldowns: [],
      },
    ];
    mocks.setDisabled.mockResolvedValue({ ...mocks.snapshot, revision: 2 });
    renderSection();
    await clickAccountAction("Disable");
    await waitFor(() =>
      expect(mocks.setDisabled).toHaveBeenCalledWith("key", true),
    );
  });

  it("reconnects only the selected saved generation without import controls", async () => {
    mocks.snapshot.accounts = [
      {
        id: "saved-a",
        provider: "codex",
        kind: "oauth",
        label: "Work",
        email: "a@example.invalid",
        status: "active", verification: "verified",
        disabled: false,
        unavailable: false,
        generation: 4,
        reconnectSupported: true,
        cooldowns: [],
      },
    ];
    mocks.startOAuth.mockResolvedValue({
      id: "reconnect-a",
      authorizationUrl: "https://provider.example/login",
      provider: "codex",
      mode: "device",
      status: "pending",
    });
    mocks.openExternal.mockResolvedValue(undefined);
    renderSection();
    await clickAccountAction("Reconnect");
    expect(screen.getByText(/Sign in again for Work/)).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "API key" }),
    ).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: /Device sign-in/ }),
    );
    await waitFor(() =>
      expect(mocks.startOAuth).toHaveBeenCalledWith("codex", "device", {
        accountId: "saved-a",
        generation: 4,
      }),
    );
    expect(mocks.updateRouting).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() =>
      expect(mocks.cancelOAuth).toHaveBeenCalledWith("reconnect-a"),
    );
  });

  it("disables reconnect for imported identity claims", async () => {
    mocks.snapshot.accounts = [
      {
        id: "imported",
        provider: "codex",
        kind: "oauth",
        status: "active", verification: "verified",
        disabled: false,
        unavailable: false,
        generation: 1,
        reconnectSupported: false,
        cooldowns: [],
      },
    ];
    renderSection();
    await clickAccountAction("Reconnect");
    const reconnect = screen.getByRole("menuitem", { name: "Reconnect" });
    expect(reconnect).toHaveAttribute("aria-disabled", "true");
    expect(reconnect).toHaveAttribute(
      "title",
      "This credential has no verified provider identity. Add it as a separate account instead.",
    );
  });

  it("localizes account controls, empty counts, and sign-in failures", async () => {
    mocks.startOAuth.mockRejectedValue(new Error("private upstream detail"));
    renderSection("es");
    expect(screen.getByText("Cuentas")).toBeInTheDocument();
    expect(screen.queryByText("0 cuentas guardadas")).not.toBeInTheDocument();
    fireEvent.click(
      screen.getByRole("button", { name: "Añadir cuenta de codex" }),
    );
    fireEvent.click(
      screen.getByRole("button", {
        name: /Inicio de sesión con dispositivo/,
      }),
    );
    expect(
      await screen.findByText("No se pudo iniciar sesión. Inténtalo de nuevo."),
    ).toBeInTheDocument();
    expect(
      screen.queryByText("private upstream detail"),
    ).not.toBeInTheDocument();
  });

  it("renders unset routing policies before any account is saved", () => {
    mocks.snapshot.routing = [
      { provider: "codex", enabled: false, accountIds: [] },
    ];
    renderSection();
    expect(screen.queryByText("No Codex accounts yet.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Add codex account" })).toHaveTextContent("Add an account");
    expect(screen.queryByRole("switch", { name: "Route new codex sessions through Accounts Manager" })).not.toBeInTheDocument();
  });

  it.each([
    [1, "1 cuenta guardada"],
    [2, "2 cuentas guardadas"],
  ] as const)(
			"localizes the saved-account count for %i accounts",
			async (count, summary) => {
      mocks.snapshot.accounts = Array.from({ length: count }, (_, index) => ({
        id: `account-${index}`,
        provider: "codex",
        kind: "oauth",
        status: "active", verification: "verified",
        disabled: false,
        unavailable: false,
        quotaSupported: false,
        cooldowns: [],
      }));
      renderSection("es");
      expect(screen.getByText(summary)).toBeInTheDocument();
			const actions = screen.getAllByRole("button", { name: "Más acciones de cuenta" });
			expect(actions).toHaveLength(count);
			await userEvent.setup().click(actions[0]);
			expect(screen.getByRole("menuitem", { name: "Actualizar cuenta" })).toBeInTheDocument();
			expect(screen.getByRole("menuitem", { name: "Eliminar cuenta" })).toBeInTheDocument();
      expect(screen.getAllByText("Lista")).toHaveLength(count);
    },
  );

  it.each(["callback", "device"])("keeps the accepted %s operation recoverable when the browser opener fails before inventory arrives", async mode => {
    const authorizationUrl = "https://auth.example.test/authorize?state=public-fixture";
    mocks.startOAuth.mockResolvedValue({
      id: "safe-operation",
      authorizationUrl,
      provider: "codex",
      status: "pending",
      mode,
      expiresAt: new Date(Date.now() + 600_000).toISOString(),
      ...(mode === "device" ? { userCode: "ABCD-EFGH" } : {}),
    });
    mocks.openExternal.mockRejectedValueOnce(new Error("private opener details")).mockResolvedValue(undefined);
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: mode === "callback" ? /Browser sign-in/ : /Device sign-in/ }));
    expect(await screen.findByLabelText("Sign-in link")).toHaveValue(authorizationUrl);
    expect(screen.getByText("Could not open your browser. Open or copy the sign-in link to continue.")).toBeInTheDocument();
    expect(screen.queryByText("private opener details")).not.toBeInTheDocument();
    expect(mocks.cancelOAuth).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Open sign-in page" }));
    await waitFor(() => expect(mocks.openExternal).toHaveBeenCalledTimes(2));
    expect(mocks.openExternal).toHaveBeenLastCalledWith(authorizationUrl);
    expect(mocks.startOAuth).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(mocks.cancelOAuth).toHaveBeenCalledTimes(1));
    expect(mocks.cancelOAuth).toHaveBeenCalledWith("safe-operation");
    await waitFor(() => expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument());
    expect(screen.queryByText("Sign-in cancelled")).not.toBeInTheDocument();
    expect(mocks.startOAuth).toHaveBeenCalledWith("codex", mode);
  });

  it("starts Codex device sign-in by default", async () => {
    mocks.startOAuth.mockResolvedValue({
      id: "safe-operation",
      authorizationUrl: "https://auth.openai.com/codex/device",
      userCode: "ABCD-EFGH",
      provider: "codex",
      mode: "device",
      status: "pending",
    });
    mocks.openExternal.mockResolvedValue(undefined);
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(
      screen.getByRole("button", { name: /Device sign-in/ }),
    );
    await waitFor(() =>
      expect(mocks.startOAuth).toHaveBeenCalledWith("codex", "device"),
    );
    expect(mocks.openExternal).toHaveBeenCalledWith(
      "https://auth.openai.com/codex/device",
    );
  });

  it.each([undefined, "file:///private/secret", "http://example.test/login", "https://user:password@example.test/login"])("rejects an unusable authorization link before the browser handoff: %s", async authorizationUrl => {
    mocks.startOAuth.mockResolvedValue({ id: "invalid-link", authorizationUrl, provider: "codex", mode: "callback", status: "pending", expiresAt: new Date(Date.now() + 600_000).toISOString() });
    mocks.openExternal.mockResolvedValue(undefined);
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: /Browser sign-in/ }));
    await waitFor(() => expect(mocks.cancelOAuth).toHaveBeenCalledWith("invalid-link"));
    expect(mocks.openExternal).not.toHaveBeenCalled();
    expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument();
    expect(await screen.findByText("Could not start sign-in. Try again.")).toBeInTheDocument();
  });

  it.each(["completed", "failed", "expired"])("retires a transient link on a rounded-equal %s observation and never resurrects it after pruning", async status => {
    const session = { id: "local-start", provider: "codex", mode: "callback", status: "pending", authorizationUrl: "https://provider.example/login", expiresAt: new Date(Date.now() + 600_000).toISOString() };
    mocks.snapshot.revision = Number.MAX_SAFE_INTEGER + 1;
    mocks.startOAuth.mockResolvedValue(session);
    mocks.openExternal.mockRejectedValue(new Error("private opener details"));
    const view = renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: /Browser sign-in/ }));
    expect(await screen.findByLabelText("Sign-in link")).toHaveValue(session.authorizationUrl);
    mocks.snapshot = { ...mocks.snapshot, oauthSessions: [{ ...session, status }] };
    view.refresh();
    await waitFor(() => expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument());
    mocks.snapshot = { ...mocks.snapshot, oauthSessions: [] };
    view.refresh();
    expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument();
    expect(mocks.cancelOAuth).not.toHaveBeenCalled();
    expect(mocks.startOAuth).toHaveBeenCalledTimes(1);
  });

  it("does not unlock a new sign-in when a cancelled operation's browser handoff returns late", async () => {
    let finishOpen!: () => void;
    let finishStart!: (session: object) => void;
    const session = { id: "old-operation", provider: "codex", mode: "callback", status: "pending", authorizationUrl: "https://provider.example/login", expiresAt: new Date(Date.now() + 600_000).toISOString() };
    mocks.startOAuth.mockResolvedValueOnce(session).mockReturnValueOnce(new Promise(resolve => { finishStart = resolve; }));
    mocks.openExternal.mockReturnValueOnce(new Promise<void>(resolve => { finishOpen = resolve; })).mockResolvedValue(undefined);
    renderSection();
    const start = () => {
      fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
      fireEvent.click(screen.getByRole("button", { name: /Browser sign-in/ }));
    };
    start();
    await waitFor(() => expect(mocks.openExternal).toHaveBeenCalledOnce());
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "Cancel" })).not.toBeInTheDocument());
    start();
    expect(screen.getByRole("button", { name: /Browser sign-in/ })).toBeDisabled();
    await act(async () => finishOpen());
    expect(screen.getByRole("button", { name: /Browser sign-in/ })).toBeDisabled();
    expect(mocks.startOAuth).toHaveBeenCalledTimes(2);
    await act(async () => finishStart({ ...session, id: "new-operation" }));
  });

  it("shows the pending device code inline", () => {
    mocks.snapshot = {
      revision: 2,
      availability: "ready",
      stale: false,
      accounts: [],
      oauthSessions: [
        {
          id: "safe-operation",
          provider: "codex",
          mode: "device",
          status: "pending",
          authorizationUrl: "https://auth.openai.com/codex/device",
          userCode: "ABCD-EFGH",
          expiresAt: "2026-09-20T12:00:00Z",
        },
      ],
    };
    renderSection();
    expect(screen.getByText("ABCD-EFGH")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy" })).toBeInTheDocument();
  });

  it("clears API key input after a failed submission", async () => {
    mocks.addKey.mockRejectedValue(new Error("rejected"));
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: /API key/ }));
    const input = screen.getByLabelText("API key") as HTMLInputElement;
    fireEvent.change(input, { target: { value: "secret-value" } });
    fireEvent.click(screen.getByRole("button", { name: "Add account" }));
    await waitFor(() => expect(mocks.addKey).toHaveBeenCalled());
    await waitFor(() => expect(input.value).toBe(""));
  });

  it("requires an explicit default instead of choosing available accounts", async () => {
    mocks.snapshot = {
      revision: 3,
      availability: "ready",
      stale: false,
      accounts: [
        {
          id: "account-a",
          provider: "codex",
          kind: "oauth",
          email: "a@example.com",
          status: "active", verification: "verified",
          disabled: false,
          unavailable: false,
          quotaSupported: false,
          cooldowns: [],
        },
        {
          id: "account-b",
          provider: "codex",
          kind: "oauth",
          email: "b@example.com",
          status: "active", verification: "verified",
          disabled: false,
          unavailable: false,
          quotaSupported: false,
          cooldowns: [],
        },
      ],
      oauthSessions: [],
      routing: [{ provider: "codex", enabled: false, accountIds: [] }],
    };
    mocks.updateRouting.mockResolvedValue({
      ...mocks.snapshot,
      revision: 4,
      routing: [
        {
          provider: "codex",
          enabled: true,
          accountIds: ["account-b"],
        },
      ],
    });
    renderSection();
    expect(
      screen.queryByRole("switch", {
        name: "Route new codex sessions through Accounts Manager",
      }),
    ).not.toBeInTheDocument();
    expect(screen.queryByText("Route new sessions through Accounts Manager")).not.toBeInTheDocument();
    fireEvent.click(
      screen.getAllByRole("button", { name: "Use as default" })[1],
    );
    await waitFor(() =>
      expect(mocks.updateRouting).toHaveBeenCalledWith("codex", true, [
        "account-b",
      ]),
    );
    expect(screen.queryByText("Changes apply to new sessions. Existing sessions keep their selected account.")).not.toBeInTheDocument();
  });

  it.each([false, true])("keeps default selection actionable when legacy routing is %s", async enabled => {
    mocks.snapshot.accounts = [{ id: "account-a", provider: "codex", label: "Review Work", kind: "oauth", status: "active", verification: "verified", disabled: false, unavailable: false, quotaSupported: false, cooldowns: [] }];
    mocks.snapshot.routing = [{ provider: "codex", enabled, accountIds: ["account-a"] }];
    mocks.updateRouting.mockResolvedValue(mocks.snapshot);
    renderSection();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    if (enabled) {
      await clickAccountAction("Remove");
      expect(mocks.updateRouting).toHaveBeenCalledWith("codex", false, []);
    } else {
      await userEvent.click(screen.getByRole("button", { name: "Use as default" }));
      expect(mocks.updateRouting).toHaveBeenCalledWith("codex", true, ["account-a"]);
    }
  });

  it("shows a safe default-save error after the routing row is removed", async () => {
    mocks.snapshot.accounts = [{ id: "account-a", provider: "codex", label: "Review Work", kind: "oauth", status: "active", verification: "verified", disabled: false, unavailable: false, quotaSupported: false, cooldowns: [] }];
    mocks.updateRouting.mockRejectedValueOnce(new AccountControlError(503, "default-request"));
    renderSection();
    await userEvent.click(screen.getByRole("button", { name: "Use as default" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("default-request");
    expect(screen.getByRole("button", { name: "Use as default" })).toBeEnabled();
  });

  it("never replaces an unusable saved selection automatically", () => {
    mocks.snapshot = {
      revision: 5,
      availability: "ready",
      stale: false,
      accounts: [
        {
          id: "disabled-account",
          provider: "claude",
          kind: "oauth",
          status: "disabled",
          disabled: true,
          unavailable: false,
          quotaSupported: false,
          cooldowns: [],
        },
        {
          id: "ready-account",
          provider: "claude",
          kind: "oauth",
          status: "active", verification: "verified",
          disabled: false,
          unavailable: false,
          quotaSupported: false,
          cooldowns: [],
        },
      ],
      oauthSessions: [],
      routing: [
        {
          provider: "claude",
          enabled: false,
          accountIds: ["disabled-account"],
        },
      ],
    };
    mocks.updateRouting.mockResolvedValue({ ...mocks.snapshot, revision: 6 });

    renderSection();
    expect(screen.queryByRole("switch")).not.toBeInTheDocument();
    const defaults = screen.getAllByRole("button", { name: "Use as default" });
    expect(defaults[0]).toBeDisabled();
    expect(defaults[1]).toBeEnabled();
    expect(mocks.updateRouting).not.toHaveBeenCalled();
  });

  it("preserves newer event data when a mutation completes late", async () => {
    const client = new QueryClient();
    client.setQueryData(accountsManagerQueryKey, {
      ...mocks.snapshot,
      revision: 20,
    });
    mocks.addKey.mockResolvedValue({ ...mocks.snapshot, revision: 19 });
    renderSection("en", client);
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(screen.getByRole("button", { name: /API key/ }));
    fireEvent.change(screen.getByLabelText("API key"), {
      target: { value: "secret-value" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Add account" }));
    await waitFor(() =>
      expect(screen.queryByLabelText("API key")).not.toBeInTheDocument(),
    );
    expect(client.getQueryData(accountsManagerQueryKey)).toMatchObject({
      revision: 20,
    });
  });

  it("keeps a failed cancellation visible and allows retry", async () => {
    mocks.snapshot.oauthSessions = [
      { id: "pending", provider: "codex", status: "pending" },
    ];
    mocks.cancelOAuth.mockRejectedValueOnce(new Error("private error"));
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(
      await screen.findByText("Could not cancel sign-in. Try again."),
    ).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    await waitFor(() => expect(mocks.cancelOAuth).toHaveBeenCalledTimes(2));
    await waitFor(() =>
      expect(
        screen.queryByText("Waiting for sign-in…"),
      ).not.toBeInTheDocument(),
    );
  });

  it.each(["pending", "unknown"])(
    "does not present %s accounts as ready",
    (status) => {
      mocks.snapshot.accounts = [
        {
          id: "account",
          provider: "codex",
          kind: "oauth",
          status,
          cooldowns: [],
        },
      ];
      renderSection();
      expect(screen.queryByText("Ready")).not.toBeInTheDocument();
      expect(
        screen.getByText(status === "pending" ? "Pending" : "Unknown"),
      ).toBeInTheDocument();
    },
  );

  it("disables account mutations when the cached inventory is stale", () => {
    mocks.snapshot.stale = true;
    renderSection();
    expect(
      screen.getByRole("button", { name: "Add codex account" }),
    ).toBeDisabled();
  });

  it("distinguishes saved unverified keys without presenting them as ready", () => {
    mocks.snapshot.accounts = ["00000a", "00000b"].map((id) => ({
      id,
      provider: "codex",
      kind: "api_key",
      status: "active", verification: "unverified",
      cooldowns: [],
    }));
    renderSection();
    expect(screen.getAllByText("Codex API key (00000a)")).toHaveLength(1);
    expect(screen.getAllByText("Codex API key (00000b)")).toHaveLength(1);
    expect(screen.getAllByText("Not verified")).toHaveLength(2);
    expect(screen.queryByText("Ready")).not.toBeInTheDocument();
  });

  it("cancels a login that finishes starting after the form was closed", async () => {
    let finish!: (value: { id: string; authorizationUrl: string }) => void;
    mocks.startOAuth.mockReturnValue(
      new Promise((resolve) => {
        finish = resolve;
      }),
    );
    renderSection();
    fireEvent.click(screen.getByRole("button", { name: "Add codex account" }));
    fireEvent.click(
      screen.getByRole("button", { name: /Device sign-in/ }),
    );
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    finish({ id: "late-login", authorizationUrl: "https://example.test" });
    await waitFor(() =>
      expect(mocks.cancelOAuth).toHaveBeenCalledWith("late-login"),
    );
    expect(mocks.openExternal).not.toHaveBeenCalled();
  });
});
