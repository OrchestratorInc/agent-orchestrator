import { createElement } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";
import type { PullRequestSource } from "./source";
import { useSourcePRs } from "./useSourcePRs";
Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
let result: ReturnType<typeof useSourcePRs>; let renderer: ReactTestRenderer;
function Probe({ source }: { source: PullRequestSource }) { result = useSourcePRs([{ source, sessionId: "same" }]); return null; }
function Many({ source }: { source: PullRequestSource }) { result = useSourcePRs(Array.from({ length: 257 }, (_, index) => ({ source, sessionId: index === 0 ? "same" : `session-${index}` }))); return null; }
afterEach(() => { act(() => renderer?.unmount()); vi.restoreAllMocks(); });
it("bounds retained navigation entries without retaining a retired source lease", async () => {
  vi.spyOn(Date, "now").mockReturnValue(1_000);
  const list = vi.fn(async () => []);
  const source = { scopeKey: "account", isCurrent: () => true, list } as unknown as PullRequestSource;
  await act(async () => { renderer = create(createElement(Many, { source })); });
  expect(list).toHaveBeenCalledTimes(257);
  act(() => renderer.unmount());
  await act(async () => { renderer = create(createElement(Probe, { source })); });
  expect(list).toHaveBeenCalledTimes(258);
});
it("expires retained PRs from the real fetch time even after a quick revisit", async () => {
  const now = vi.spyOn(Date, "now").mockReturnValue(1_000);
  const list = vi.fn(async () => []);
  const source = { scopeKey: "account", isCurrent: () => true, list } as unknown as PullRequestSource;
  await act(async () => { renderer = create(createElement(Probe, { source })); });
  act(() => renderer.unmount());
  now.mockReturnValue(45_000);
  await act(async () => { renderer = create(createElement(Probe, { source })); });
  expect(list).toHaveBeenCalledTimes(1);
  act(() => renderer.unmount());
  now.mockReturnValue(71_000);
  await act(async () => { renderer = create(createElement(Probe, { source })); });
  expect(list).toHaveBeenCalledTimes(2);
});
it("aborts and forgets an old account even when its scope and session IDs match", async () => {
  let finish!: (value: unknown[]) => void;
  const oldList = vi.fn((_id, options) => new Promise<never>((resolve) => { finish = resolve as never; }));
  const oldSource = { scopeKey: "same", isCurrent: () => true, list: oldList } as unknown as PullRequestSource;
  const source = { scopeKey: "same", isCurrent: () => true, list: vi.fn(async () => [{ number: 8, url: "new" }]) } as unknown as PullRequestSource;
  await act(async () => { renderer = create(createElement(Probe, { source: oldSource })); });
  await act(async () => { renderer.update(createElement(Probe, { source })); });
  expect(oldList.mock.calls[0][1].signal.aborted).toBe(true);
  await act(async () => { finish([{ number: 7, url: "old" }]); });
  expect(result.list(source, "same")).toEqual([{ number: 8, url: "new" }]);
  expect(result.list(oldSource, "same")).toEqual([]);
});
it("retains same-source cards after a failed refresh and exposes retryable failure", async () => {
  const list = vi.fn().mockResolvedValueOnce([{ number: 7, url: "current" }]).mockRejectedValueOnce(new Error("Offline"));
  const source = { scopeKey: "account", isCurrent: () => true, list } as unknown as PullRequestSource;
  await act(async () => { renderer = create(createElement(Probe, { source })); });
  await act(async () => { result.reload(); });
  expect(result.list(source, "same")).toEqual([{ number: 7, url: "current" }]);
  expect(result.error).toBe("Offline");
});
