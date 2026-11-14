import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { isCloudSignInConfigured } from "./cloud-session";

describe("isCloudSignInConfigured", () => {
  it("is configured by default via the baked WorkOS client id", () => {
    expect(isCloudSignInConfigured(undefined)).toBe(true);
  });

  it("treats an explicitly blank client ID as not configured", () => {
    expect(isCloudSignInConfigured("   ")).toBe(false);
  });

  it("shows Cloud sign-in when the WorkOS client ID is configured", () => {
    expect(isCloudSignInConfigured("client_123")).toBe(true);
  });
});

describe("useCloudSession", () => {
  it("starts a later consumer from the last resolved session instead of loading", async () => {
    vi.resetModules();
    const account = { authProvider: "workos", user: { id: "user-1" } };
    vi.doMock("./bridge", () => ({
      aoBridge: {
        cloud: {
          getSession: () => Promise.resolve(account),
          onSessionChanged: () => () => undefined,
          signIn: () => undefined,
          signOut: () => Promise.resolve(),
        },
      },
    }));
    const { useCloudSession } = await import("./cloud-session");
    const first = renderHook(() => useCloudSession());
    expect(first.result.current.status).toBe("loading");
    await waitFor(() => expect(first.result.current.status).toBe("authenticated"));

    const second = renderHook(() => useCloudSession());
    expect(second.result.current.status).toBe("authenticated");
    expect(second.result.current.session).toEqual(account);
    vi.doUnmock("./bridge");
  });

  it("shares one bridge read and one change listener across every consumer", async () => {
    vi.resetModules();
    const account = { authProvider: "workos", user: { id: "user-1" } };
    const getSession = vi.fn(() => Promise.resolve({ ...account }));
    const changeListeners = new Set<(s: unknown) => void>();
    const onSessionChanged = vi.fn((listener: (s: unknown) => void) => {
      changeListeners.add(listener);
      return () => changeListeners.delete(listener);
    });
    vi.doMock("./bridge", () => ({
      aoBridge: { cloud: { getSession, onSessionChanged, signIn: () => undefined, signOut: () => Promise.resolve() } },
    }));
    const { useCloudSession } = await import("./cloud-session");
    const cards = Array.from({ length: 50 }, () => renderHook(() => useCloudSession()));
    await waitFor(() => expect(cards[0].result.current.status).toBe("authenticated"));
    expect(getSession).toHaveBeenCalledTimes(1);
    expect(onSessionChanged).toHaveBeenCalledTimes(1);

    const before = cards[0].result.current.session;
    changeListeners.forEach((listener) => listener({ ...account }));
    expect(cards[0].result.current.session).toBe(before);

    changeListeners.forEach((listener) => listener(null));
    await waitFor(() => expect(cards[49].result.current.status).toBe("unauthenticated"));
    cards.forEach((card) => card.unmount());
    expect(changeListeners.size).toBe(0);
    vi.doUnmock("./bridge");
  });

  it("drops a read that finishes after a newer session change", async () => {
    vi.resetModules();
    const account = { authProvider: "workos", user: { id: "user-1" } };
    let answer!: (s: unknown) => void;
    const getSession = vi.fn(() => new Promise((resolve) => { answer = resolve; }));
    const changeListeners = new Set<(s: unknown) => void>();
    vi.doMock("./bridge", () => ({
      aoBridge: {
        cloud: {
          getSession,
          onSessionChanged: (listener: (s: unknown) => void) => {
            changeListeners.add(listener);
            return () => changeListeners.delete(listener);
          },
          signIn: () => undefined,
          signOut: () => Promise.resolve(),
        },
      },
    }));
    const { useCloudSession } = await import("./cloud-session");
    const card = renderHook(() => useCloudSession());
    expect(getSession).toHaveBeenCalledTimes(1);
    act(() => changeListeners.forEach((listener) => listener(null)));
    expect(card.result.current.status).toBe("unauthenticated");
    await act(async () => answer({ ...account }));
    expect(card.result.current.status).toBe("unauthenticated");
    expect(card.result.current.session).toBeNull();
    card.unmount();
    vi.doUnmock("./bridge");
  });
});
