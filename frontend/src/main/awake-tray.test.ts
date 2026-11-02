// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { createAwakeTrayMonitor } from "./awake-tray";

afterEach(() => { vi.useRealTimers(); });

function setup() {
	vi.useFakeTimers();
	let base: string | null = "http://127.0.0.1:3001";
	const request = vi.fn(async (url: string) => Response.json(url.endsWith("mobile/status") ? { keepAwake: { active: false } } : { active: false }));
	const setState = vi.fn();
	const monitor = createAwakeTrayMonitor({ baseURL: () => base, request, setState });
	return { request, setState, monitor, setBase: (value: string | null) => { base = value; } };
}

describe("local awake menu bar monitor", () => {
	it.each(["awake", "mobile/status"])("shows an active hold from %s and clears after release", async (source) => {
		const { request, setState, monitor } = setup();
		await vi.advanceTimersByTimeAsync(0);
		expect(setState).toHaveBeenLastCalledWith(false);
		request.mockImplementation(async (url) => Response.json(url.endsWith("mobile/status") ? { keepAwake: { active: source === "mobile/status" } } : { active: source === "awake" }));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(true);
		request.mockImplementation(async (url) => Response.json(url.endsWith("mobile/status") ? { keepAwake: { active: false } } : { active: false }));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(false);
		monitor.dispose();
	});

	it("reports unknown for failed, malformed, or unavailable status, then recovers", async () => {
		const { request, setState, monitor, setBase } = setup();
		await vi.advanceTimersByTimeAsync(0);
		request.mockResolvedValue(new Response("unavailable", { status: 503 }));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(null);
		request.mockResolvedValue(Response.json({}));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(null);
		setBase(null);
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(null);
		setBase("http://127.0.0.1:3001");
		request.mockImplementation(async (url) => Response.json(url.endsWith("mobile/status") ? { keepAwake: { active: false } } : { active: true }));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(true);
		monitor.dispose();
	});

	it("keeps a confirmed hold visible when the other source is unavailable", async () => {
		const { request, setState, monitor } = setup();
		await vi.advanceTimersByTimeAsync(0);
		request.mockImplementation(async (url) => {
			if (url.endsWith("mobile/status")) throw new Error("unavailable");
			return Response.json({ active: true });
		});
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).toHaveBeenLastCalledWith(true);
		monitor.dispose();
	});

	it("ignores a response from a previous daemon attachment", async () => {
		const { request, setState, monitor, setBase } = setup();
		await vi.advanceTimersByTimeAsync(0);
		const finishes: ((value: Response) => void)[] = [];
		request.mockImplementation(() => new Promise((resolve) => { finishes.push(resolve); }));
		await vi.advanceTimersByTimeAsync(5000);
		setBase("http://127.0.0.1:3002");
		setState.mockClear();
		for (const finish of finishes) finish(Response.json({ active: true, keepAwake: { active: true } }));
		await vi.advanceTimersByTimeAsync(0);
		expect(setState).not.toHaveBeenCalled();
		monitor.dispose();
	});

	it("does not overlap requests or publish stale responses after detach/disposal", async () => {
		const { request, setState, monitor, setBase } = setup();
		await vi.advanceTimersByTimeAsync(0);
		const finishes: ((value: Response) => void)[] = [];
		request.mockImplementation(() => new Promise((resolve) => { finishes.push(resolve); }));
		await vi.advanceTimersByTimeAsync(5000);
		const count = request.mock.calls.length;
		await vi.advanceTimersByTimeAsync(5000);
		expect(request).toHaveBeenCalledTimes(count);
		setBase(null);
		monitor.dispose();
		setState.mockClear();
		for (const finish of finishes) finish(Response.json({ active: true, keepAwake: { active: true } }));
		await vi.advanceTimersByTimeAsync(5000);
		expect(setState).not.toHaveBeenCalled();
		expect(request).toHaveBeenCalledTimes(count);
	});
});
