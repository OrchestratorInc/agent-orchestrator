import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { LINK_PREVIEW_SETTLE_MS, useLinkPreview } from "./useLinkPreview";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: () => "request failed",
}));

// The hook caches per URL in the shared module-level queryClient, so each test
// uses distinct URLs to stay isolated.

afterEach(() => {
	vi.useRealTimers();
	getMock.mockReset();
});

describe("useLinkPreview", () => {
	it("does not fetch until enabled", async () => {
		renderHook(() => useLinkPreview("https://idle.example.test/", false));
		await new Promise((resolve) => setTimeout(resolve, 50));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("refetches when the url changes on the same hook instance", async () => {
		// Regression: XtermTerminal reuses one hook instance across hovered links;
		// a previous URL's preview must never be returned for the current URL.
		const urlA = "https://videos.example.test/never-gonna";
		const urlB = "https://social.example.test/home";
		getMock.mockImplementation((_path: string, opts: { params: { query: { url: string } } }) =>
			Promise.resolve({ data: { url: opts.params.query.url, title: `Title for ${opts.params.query.url}` }, error: undefined }),
		);

		const { result, rerender } = renderHook(({ url }) => useLinkPreview(url, true), {
			initialProps: { url: urlA },
		});
		await waitFor(() => expect(result.current.data?.url).toBe(urlA));

		rerender({ url: urlB });
		expect(result.current.data?.url).not.toBe(urlA);
		await waitFor(() => expect(result.current.data?.url).toBe(urlB));
		expect(getMock).toHaveBeenCalledTimes(2);

		// Re-hovering the first URL serves the cache without another request.
		rerender({ url: urlA });
		await waitFor(() => expect(result.current.data?.url).toBe(urlA));
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("surfaces fetch failure as isError", async () => {
		getMock.mockResolvedValue({ data: undefined, error: { message: "upstream down" } });
		const { result } = renderHook(() => useLinkPreview("https://down.example.test/", true));
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(result.current.data).toBeUndefined();
	});

	it("refetches a failed URL when re-enabled after being disabled", async () => {
		// A transient blip on first hover must not permanently kill previews for
		// this component: leaving and returning retries the same URL.
		const url = "https://retry.example.test/";
		getMock.mockResolvedValue({ data: undefined, error: { message: "upstream down" } });

		const { result, rerender } = renderHook(({ enabled }) => useLinkPreview(url, enabled), {
			initialProps: { enabled: true },
		});
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(getMock).toHaveBeenCalledTimes(1);

		rerender({ enabled: false });
		await waitFor(() => expect(result.current.isError).toBe(false));
		expect(getMock).toHaveBeenCalledTimes(1);

		rerender({ enabled: true });
		await waitFor(() => expect(result.current.isError).toBe(true));
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("asks the daemon again on every hover of a failed URL so a recovery shows at once", async () => {
		const url = "https://recovering-gateway.example.test/";
		getMock.mockResolvedValueOnce({ error: { message: "upstream failed" }, response: { status: 502 } })
			.mockResolvedValueOnce({ data: { url, title: "Recovered" } });
		const { result, rerender } = renderHook(({ enabled }) => useLinkPreview(url, enabled), { initialProps: { enabled: true } });
		await waitFor(() => expect(result.current.isError).toBe(true));
		rerender({ enabled: false });
		rerender({ enabled: true });
		await waitFor(() => expect(result.current.data?.title).toBe("Recovered"));
		expect(getMock).toHaveBeenCalledTimes(2);
	});

	it("settles to the fallback when the daemon never answers, then upgrades if it does", async () => {
		vi.useFakeTimers();
		const url = "https://never-answers.example.test/";
		let resolveRequest!: (value: unknown) => void;
		getMock.mockImplementation(() => new Promise((resolve) => { resolveRequest = resolve; }));
		const { result } = renderHook(() => useLinkPreview(url, true));
		await act(async () => { await vi.advanceTimersByTimeAsync(LINK_PREVIEW_SETTLE_MS - 1); });
		expect(result.current).toMatchObject({ isPending: true, isError: false });
		await act(async () => { await vi.advanceTimersByTimeAsync(1); });
		expect(result.current).toMatchObject({ isPending: false, isError: true });
		await act(async () => {
			resolveRequest({ data: { url, title: "Late" } });
			await vi.advanceTimersByTimeAsync(0);
		});
		expect(result.current).toMatchObject({ isPending: false, isError: false, data: { title: "Late" } });
	});

	it("aborts the request when the hover ends so passed-over links free their connection", async () => {
		const url = "https://passed-over.example.test/";
		getMock.mockImplementation((_path: string, opts: { signal: AbortSignal }) => new Promise((_resolve, reject) => {
			opts.signal.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")));
		}));
		const { rerender } = renderHook(({ enabled }) => useLinkPreview(url, enabled), { initialProps: { enabled: true } });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		const signal = getMock.mock.calls[0][1].signal as AbortSignal;
		expect(signal.aborted).toBe(false);
		rerender({ enabled: false });
		expect(signal.aborted).toBe(true);
	});

	it("keeps the request while another hover on the same URL is still open", async () => {
		const url = "https://shared-hover.example.test/";
		getMock.mockImplementation(() => new Promise(() => undefined));
		const first = renderHook(() => useLinkPreview(url, true));
		const second = renderHook(() => useLinkPreview(url, true));
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		const signal = getMock.mock.calls[0][1].signal as AbortSignal;
		first.unmount();
		expect(signal.aborted).toBe(false);
		second.unmount();
		expect(signal.aborted).toBe(true);
	});

	it("ignores a slow response for a URL the hook has moved on from", async () => {
		const slow = "https://rapid-a.example.test/";
		const fast = "https://rapid-b.example.test/";
		let finishSlow!: (value: unknown) => void;
		getMock.mockImplementation((_path: string, opts: { params: { query: { url: string } } }) =>
			opts.params.query.url === slow
				? new Promise((resolve) => { finishSlow = resolve; })
				: Promise.resolve({ data: { url: fast, title: "Fast" } }),
		);
		const { result, rerender } = renderHook(({ url }) => useLinkPreview(url, true), { initialProps: { url: slow } });
		await waitFor(() => expect(getMock).toHaveBeenCalledTimes(1));
		rerender({ url: fast });
		await waitFor(() => expect(result.current.data?.title).toBe("Fast"));
		await act(async () => { finishSlow({ data: { url: slow, title: "Slow" } }); });
		expect(result.current.data?.title).toBe("Fast");
	});

	it("serves a succeeded URL from cache when re-enabled without refetching", async () => {
		const url = "https://cached-hit.example.test/";
		getMock.mockResolvedValue({ data: { url, title: "Cached" }, error: undefined });

		const { result, rerender } = renderHook(({ enabled }) => useLinkPreview(url, enabled), {
			initialProps: { enabled: true },
		});
		await waitFor(() => expect(result.current.data?.url).toBe(url));
		expect(getMock).toHaveBeenCalledTimes(1);

		rerender({ enabled: false });
		rerender({ enabled: true });
		await waitFor(() => expect(result.current.data?.url).toBe(url));
		expect(result.current.isError).toBe(false);
		expect(getMock).toHaveBeenCalledTimes(1);
	});
});
