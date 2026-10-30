import { renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { isCloudSignInConfigured } from "./cloud-session";

describe("isCloudSignInConfigured", () => {
  it("is configured by default via the baked WorkOS client id", () => {
    // The client id ships as a baked default, so with no explicit argument
    // sign-in is always configured; the cloud offering toggle is the real gate.
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

    // A view opened afterwards (a new session's screen) must not read as
    // signed out while its own bridge round trip runs.
    const second = renderHook(() => useCloudSession());
    expect(second.result.current.status).toBe("authenticated");
    expect(second.result.current.session).toEqual(account);
    vi.doUnmock("./bridge");
  });
});
