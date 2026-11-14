import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useUiStore } from "../stores/ui-store";
import { useAutoSpawnPauseNotice } from "./useAutoSpawnPauseNotice";

const pause = { harness: "claude-code", errorCode: "UNSUPPORTED_MODEL", message: "Model unavailable", failures: 3, pausedAt: "2026-11-10T10:00:00Z" };
describe("useAutoSpawnPauseNotice", () => {
 beforeEach(() => { window.localStorage.clear(); useUiStore.setState({ globalToasts: [] }); });
 it("keeps a pause visible until acknowledgement and removes recovered pauses", () => {
  const projects = [{ id: "persistent", name: "Mer", autoSpawnPauses: [pause] }];
  const { rerender } = renderHook(({ list }) => useAutoSpawnPauseNotice(list), { initialProps: { list: projects } });
  const toast = useUiStore.getState().globalToasts[0];
  expect(toast.durationMs).toBe(0);
  expect(toast.body).toContain("30-minute cooldown");
  act(() => useUiStore.setState({ globalToasts: [] }));
  rerender({ list: [...projects] });
  expect(useUiStore.getState().globalToasts).toHaveLength(1);
  act(() => { toast.action?.run(); useUiStore.getState().dismissGlobalToast(useUiStore.getState().globalToasts[0].nonce); });
  rerender({ list: [...projects] });
  expect(useUiStore.getState().globalToasts).toHaveLength(0);
  rerender({ list: [{ ...projects[0], autoSpawnPauses: [{ ...pause, pausedAt: "later" }] }] });
  expect(useUiStore.getState().globalToasts).toHaveLength(1);
  rerender({ list: [{ ...projects[0], autoSpawnPauses: [] }] });
  expect(useUiStore.getState().globalToasts).toHaveLength(0);
 });
 it("retains acknowledgements in memory when storage writes fail", () => {
  const projects = [{ id: "storage-failure", name: "Mer", autoSpawnPauses: [pause] }];
  const { unmount } = renderHook(() => useAutoSpawnPauseNotice(projects));
  const toast = useUiStore.getState().globalToasts[0];
  const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => { throw new Error("denied"); });
  try { expect(() => toast.action?.run()).not.toThrow(); } finally { spy.mockRestore(); }
  unmount();
  useUiStore.setState({ globalToasts: [] });
  renderHook(() => useAutoSpawnPauseNotice(projects));
  expect(useUiStore.getState().globalToasts).toHaveLength(0);
 });
 it("distinguishes hosts and launch configurations", () => {
  renderHook(() => useAutoSpawnPauseNotice([
   { id: "same", name: "Shared", autoSpawnPauses: [pause] },
   { id: "same", name: "Shared", hostId: "host-2", autoSpawnPauses: [pause, { ...pause, model: "different" }] },
  ]));
  expect(useUiStore.getState().globalToasts).toHaveLength(3);
  expect(useUiStore.getState().globalToasts.map((toast) => toast.title)).toEqual(["Automatic starts paused for Shared", "Automatic starts paused for Shared (host-2)", "Automatic starts paused for Shared (host-2)"]);
 });
});
