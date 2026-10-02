import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { I18nextProvider } from "react-i18next";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { createAppI18n } from "../../i18n";
import { SignInBrowserRecovery } from "./SignInBrowserRecovery";

const mocks = vi.hoisted(() => ({ open: vi.fn() }));
vi.mock("../../lib/bridge", () => ({ aoBridge: { app: { openExternal: mocks.open } } }));
const link = "https://provider.example/login?state=public-fixture";
const show = (authorizationUrl: string | undefined = link, expiresAt = new Date(Date.now() + 600_000).toISOString(), onOpened = vi.fn()) => render(
  <I18nextProvider i18n={createAppI18n("en")}>
    <SignInBrowserRecovery authorizationUrl={authorizationUrl} expiresAt={expiresAt} onOpened={onOpened} />
  </I18nextProvider>,
);

describe("SignInBrowserRecovery", () => {
  beforeEach(() => { vi.resetAllMocks(); mocks.open.mockResolvedValue(undefined); });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

  it.each(["", "file:///private/secret", "javascript:alert(1)", "http://provider.example/login", "https://user:password@provider.example/login"])("never displays or opens an unsafe link: %s", url => {
    show(url);
    expect(screen.queryByRole("textbox")).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
    expect(mocks.open).not.toHaveBeenCalled();
  });

  it("copies only on explicit request and retains a selectable link after clipboard failure", async () => {
    const writeText = vi.fn().mockRejectedValueOnce(new Error("private clipboard failure")).mockResolvedValue(undefined);
    vi.stubGlobal("navigator", { clipboard: { writeText } });
    show();
    expect(writeText).not.toHaveBeenCalled();
    expect(screen.getByLabelText("Sign-in link")).toHaveAttribute("readonly");
    fireEvent.click(screen.getByRole("button", { name: "Copy sign-in link" }));
    expect(await screen.findByText("Could not copy URL")).toBeInTheDocument();
    expect(screen.queryByText("private clipboard failure")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Sign-in link")).toHaveValue(link);
    fireEvent.click(screen.getByRole("button", { name: "Copy sign-in link" }));
    expect(await screen.findByText("URL copied")).toBeInTheDocument();
    expect(writeText).toHaveBeenCalledTimes(2);
    expect(writeText).toHaveBeenLastCalledWith(link);
    expect(mocks.open).not.toHaveBeenCalled();
  });

  it("joins repeated clicks on the same native browser handoff", async () => {
    let finish!: () => void;
    mocks.open.mockReturnValue(new Promise<void>(resolve => { finish = resolve; }));
    const onOpened = vi.fn();
    show(link, new Date(Date.now() + 600_000).toISOString(), onOpened);
    const button = screen.getByRole("button", { name: "Open sign-in page" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(button).toBeDisabled();
    expect(mocks.open).toHaveBeenCalledTimes(1);
    expect(onOpened).not.toHaveBeenCalled();
    await act(async () => finish());
    await waitFor(() => expect(onOpened).toHaveBeenCalledOnce());
    expect(button).toBeEnabled();
  });

  it("withdraws an expired link without claiming the operation completed", async () => {
    vi.useFakeTimers();
    show(link, new Date(Date.now() + 1000).toISOString());
    expect(screen.getByLabelText("Sign-in link")).toBeInTheDocument();
    await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
    expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument();
    expect(screen.getByText("This sign-in link has expired. Cancel this attempt and start again.")).toBeInTheDocument();
    expect(mocks.open).not.toHaveBeenCalled();
    expect(screen.queryByText("Sign-in cancelled")).not.toBeInTheDocument();
  });

  it.each(["invalid", new Date(0).toISOString()])("does not offer a link with unusable expiry: %s", expires => {
    show(link, expires);
    expect(screen.queryByLabelText("Sign-in link")).not.toBeInTheDocument();
    expect(screen.queryByRole("button")).not.toBeInTheDocument();
  });
});
