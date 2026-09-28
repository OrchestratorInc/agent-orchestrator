import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { AccountUsage } from "./AccountUsage";
import type { AccountsManagerAccount } from "../../hooks/useAccountsManagerQuery";
import { AccountControlError } from "../../lib/accounts-manager-controls";

const fetchQuota = vi.hoisted(() => vi.fn());
vi.mock("../../hooks/useAccountsManagerQuery", () => ({ fetchAccountsManagerQuota: fetchQuota }));

const account = { id:"account-a",provider:"codex",kind:"oauth",label:"Work",generation:1,verification:"verified",quotaSupported:true,disabled:false,unavailable:false,status:"active" } as AccountsManagerAccount;
const quota = (remainingFraction=.75) => ({observedAt:"2026-09-28T00:00:00Z",summary:[],serverTimeOffsetMs:0,groups:[{displayName:"Account",buckets:[{window:"18000s",remainingFraction,resetTime:"2030-01-01T00:00:00Z",description:""}]}]});

describe("AccountUsage",()=>{
  beforeEach(()=>vi.resetAllMocks());

  function show(value=account) {
    const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
    const tree = (next:AccountsManagerAccount)=><QueryClientProvider client={client}><AccountUsage account={next}/></QueryClientProvider>;
    return {...render(tree(value)),tree};
  }

  it("does not request or fabricate statistics for unsupported credentials",()=>{
    show({...account,quotaSupported:false});
    expect(screen.getByText("Usage unavailable")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(fetchQuota).not.toHaveBeenCalled();
  });

  it.each([
    ["api_key", "Subscription usage is not available for API keys. Check usage in the provider's billing console."],
    ["access_token", "This stored sign-in token does not establish usage permission. Setup tokens can allow model requests without account usage access."],
  ])("explains unsupported usage for %s without requesting it", (kind, message) => {
    show({ ...account, kind, quotaSupported: false } as AccountsManagerAccount);
    expect(screen.getByText(message)).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Refresh usage" })).not.toBeInTheDocument();
    expect(fetchQuota).not.toHaveBeenCalled();
  });

  it("does not read usage for a legacy token with a contradictory capability flag", () => {
    show({ ...account, kind: "access_token", quotaSupported: true });
    expect(screen.getByText(/does not establish usage permission/)).toBeInTheDocument();
    expect(fetchQuota).not.toHaveBeenCalled();
  });

  it("does not attach a late usage error to a changed credential format", async () => {
    let reject!: (cause: unknown) => void;
    fetchQuota.mockImplementationOnce(() => new Promise((_resolve, fail) => { reject = fail; }));
    const view = show();
    await waitFor(() => expect(fetchQuota).toHaveBeenCalledOnce());
    const signal = fetchQuota.mock.calls[0][1] as AbortSignal;
    view.rerender(view.tree({ ...account, kind: "access_token", quotaSupported: false }));
    expect(signal.aborted).toBe(true);
    await act(async () => reject(new AccountControlError(401, "old-usage-request", "ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED")));
    expect(screen.getByText(/does not establish usage permission/)).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    expect(screen.queryByText(/old-usage-request/)).not.toBeInTheDocument();
  });

  it.each([
    [401,"ACCOUNTS_MANAGER_USAGE_AUTHENTICATION_REQUIRED","The provider could not authenticate the usage request. Reconnect this account, then refresh usage."],
    [403,"ACCOUNTS_MANAGER_USAGE_ACCESS_DENIED","The provider refused the usage request. Your account selection has not changed."],
    [429,"ACCOUNTS_MANAGER_USAGE_RATE_LIMITED","Usage checks are temporarily rate-limited. Wait before refreshing again."],
    [503,"ACCOUNTS_MANAGER_USAGE_UNAVAILABLE","The usage service is temporarily unavailable. Try again later."],
    [502,"ACCOUNTS_MANAGER_USAGE_RESPONSE_INVALID","The usage service returned an unreadable response. Your account selection has not changed."],
  ] as const)("explains usage failure %s without implying an account operation",async(status,code,message)=>{
    fetchQuota.mockRejectedValue(new AccountControlError(status,"usage-check-79",code));
    show();
    expect(await screen.findByRole("alert")).toHaveTextContent(message);
    expect(screen.getByRole("alert")).toHaveTextContent("usage-check-79");
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(fetchQuota).toHaveBeenCalledOnce();
  });

  it("retains the last observation with an explicit stale marker after a failed refresh",async()=>{
    fetchQuota.mockResolvedValueOnce(quota()).mockRejectedValueOnce(new AccountControlError(503,"quota-check-79"));
    show();
    expect(await screen.findByText(/75% remaining/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button",{name:"Refresh usage"}));
    expect(await screen.findByText("Last observation only. The refresh failed.")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("quota-check-79");
    expect(screen.getByRole("progressbar")).toHaveAttribute("value","0.75");
  });

  it("does not publish an old account response into a new account or generation",async()=>{
    let resolve!:(value:ReturnType<typeof quota>)=>void;
    fetchQuota.mockImplementationOnce(()=>new Promise(done=>{resolve=done;})).mockResolvedValueOnce(quota(.4));
    const view=show();
    await waitFor(()=>expect(fetchQuota).toHaveBeenCalledOnce());
    view.rerender(view.tree({...account,id:"account-b",generation:2}));
    expect(await screen.findByText(/40% remaining/)).toBeInTheDocument();
    await act(async()=>resolve(quota(.99)));
    expect(screen.queryByText(/99% remaining/)).not.toBeInTheDocument();
    expect(screen.getByText(/40% remaining/)).toBeInTheDocument();
  });

  it("renders actual exhausted quota but rejects an invalid fraction",async()=>{
    fetchQuota.mockResolvedValueOnce(quota(0)).mockResolvedValueOnce(quota(-1));
    show();
    expect(await screen.findByText(/0% remaining/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button",{name:"Refresh usage"}));
    expect(await screen.findByText("Usage unavailable")).toBeInTheDocument();
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
  });
});
