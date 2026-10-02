import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { AccountsManagerAccount, AccountsManagerSnapshot } from "../../hooks/useAccountsManagerQuery";
import { ManagedAccountAvailability } from "./ManagedAccountAvailability";

const account = { id: "account-a", provider: "codex", kind: "api_key", status: "active", verification: "verified", disabled: false, unavailable: false } as AccountsManagerAccount;
const snapshot = { revision: 1, availability: "ready", stale: false, accounts: [account], oauthSessions: [], routing: [] } as AccountsManagerSnapshot;

describe("ManagedAccountAvailability", () => {
  it.each([
    { name: "read error", isError: true, isFetching: false, data: snapshot, message: "Managed-account availability is unknown. The inventory is unavailable or stale." },
    { name: "refresh in progress", isError: false, isFetching: true, data: snapshot, message: "Checking managed accounts..." },
    { name: "stale inventory", isError: false, isFetching: false, data: { ...snapshot, stale: true }, message: "Managed-account availability is unknown. The inventory is unavailable or stale." },
    { name: "unavailable runner", isError: false, isFetching: false, data: { ...snapshot, availability: "degraded" } as AccountsManagerSnapshot, message: "Managed-account availability is unknown. The inventory is unavailable or stale." },
    { name: "missing inventory", isError: false, isFetching: false, data: undefined, message: "Managed-account availability is unknown. The inventory is unavailable or stale." },
  ])("does not advertise cached accounts with $name", ({ message, ...inventory }) => {
    render(<ManagedAccountAvailability harness="codex" installed inventory={inventory} />);
    expect(screen.getByText(message)).toBeInTheDocument();
    expect(screen.queryByText(/\d+ managed accounts? available/)).not.toBeInTheDocument();
  });

  it.each([
    { verification: "unverified" }, { verification: "invalid" }, { disabled: true }, { unavailable: true }, { status: "error" }, { status: "pending" }, { provider: "unrelated" },
  ])("excludes unusable or unrelated inventory %j", patch => {
    const data = { ...snapshot, accounts: [{ ...account, ...patch } as AccountsManagerAccount] };
    render(<ManagedAccountAvailability harness="codex" installed inventory={{ data, isError: false, isFetching: false }} />);
    expect(screen.getByText("No verified managed accounts available.")).toBeInTheDocument();
  });

  it("keeps installation a separate prerequisite", () => {
    render(<ManagedAccountAvailability harness="codex" installed={false} inventory={{ data: snapshot, isError: false, isFetching: false }} />);
    expect(screen.getByText("Install this harness before using managed accounts.")).toBeInTheDocument();
    expect(screen.queryByText("1 managed account available")).not.toBeInTheDocument();
  });

  it("counts verified accounts without selecting one or claiming every interface", () => {
    const data = { ...snapshot, accounts: [account, { ...account, id: "account-b" }] };
    render(<ManagedAccountAvailability harness="codex" installed inventory={{ data, isError: false, isFetching: false }} />);
    expect(screen.getByText("2 managed accounts available")).toBeInTheDocument();
    expect(screen.getByText(/support depends on session mode/)).toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });

  it("does not invent support for another harness", () => {
    const { container } = render(<ManagedAccountAvailability harness="unrelated" installed inventory={{ data: snapshot, isError: false, isFetching: false }} />);
    expect(container).toBeEmptyDOMElement();
  });
});
