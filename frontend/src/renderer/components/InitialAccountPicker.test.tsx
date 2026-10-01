import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InitialAccountPicker, InitialAccountStatus } from "./InitialAccountPicker";
import type { useInitialAccountChoice } from "../hooks/useInitialAccountChoice";

vi.mock("./settings/AccountUsage", () => ({
  useAccountUsage: () => [],
  accountUsageSummary: () => "",
}));

const account = {
  id: "account-a", provider: "codex", label: "Personal", status: "active", verification: "verified",
  disabled: false, unavailable: false, quotaSupported: false,
};

function state(
  capability: { isPending: boolean; isFetching: boolean; isError?: boolean; error?: unknown },
  inventory: { isPending: boolean; isFetching: boolean; isEnabled?: boolean } = { isPending: false, isFetching: false, isEnabled: true },
  inventoryReady = true,
): ReturnType<typeof useInitialAccountChoice> {
  return {
    enabled: true,
    capability: { data: true, isError: false, error: null, ...capability },
    inventory,
    accounts: [account],
    selected: account,
    inventoryReady,
    managedProviders: ["codex"],
    ready: true,
    confirm: vi.fn(),
  } as unknown as ReturnType<typeof useInitialAccountChoice>;
}

describe("InitialAccountPicker", () => {
  it.each([
    ["initial capability load", true, true],
    ["background capability refresh", false, false],
  ])("%s controls selection availability", (_label, isPending, disabled) => {
    render(<InitialAccountPicker state={state({ isPending, isFetching: true })} value="managed:account-a" disabled={false} onChange={vi.fn()} />);
    if (disabled) expect(screen.getByRole("combobox")).toBeDisabled();
    else expect(screen.getByRole("combobox")).toBeEnabled();
  });

  it.each([
    ["capability failure with disabled inventory", { isPending: false, isFetching: false, isError: true, error: new Error("capability failed") }, { isPending: true, isFetching: false, isEnabled: false }, false],
    ["enabled initial inventory load", { isPending: false, isFetching: false }, { isPending: true, isFetching: true, isEnabled: true }, true],
    ["background inventory refresh", { isPending: false, isFetching: false }, { isPending: false, isFetching: true, isEnabled: true }, false],
  ] as const)("%s keeps refresh availability correct", (_label, capability, inventory, disabled) => {
    render(<InitialAccountStatus state={state(capability, inventory, false)} value="managed:account-a" disabled={false} />);
    const refresh = screen.getByRole("button", { name: "Refresh account state" });
    if (disabled) expect(refresh).toBeDisabled();
    else expect(refresh).toBeEnabled();
  });
});
