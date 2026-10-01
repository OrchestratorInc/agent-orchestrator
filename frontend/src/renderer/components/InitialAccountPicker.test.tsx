import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { InitialAccountPicker } from "./InitialAccountPicker";
import type { useInitialAccountChoice } from "../hooks/useInitialAccountChoice";

vi.mock("./settings/AccountUsage", () => ({
  useAccountUsage: () => [],
  accountUsageSummary: () => "",
}));

const account = {
  id: "account-a", provider: "codex", label: "Personal", status: "active", verification: "verified",
  disabled: false, unavailable: false, quotaSupported: false,
};

function state(capability: { isPending: boolean; isFetching: boolean }): ReturnType<typeof useInitialAccountChoice> {
  return {
    enabled: true,
    capability: { data: true, isError: false, error: null, ...capability },
    inventory: { isPending: false, isFetching: false },
    accounts: [account],
    selected: account,
    inventoryReady: true,
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
});
