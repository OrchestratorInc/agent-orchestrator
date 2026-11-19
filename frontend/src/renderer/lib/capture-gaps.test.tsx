import { StrictMode } from "react";
import { render, waitFor } from "@testing-library/react";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, notFound, redirect, RouterProvider } from "@tanstack/react-router";
import { afterEach, expect, it, vi } from "vitest";
import { queryClient } from "./query-client";
import { apiClient, setApiBaseUrl } from "./api-client";
import { clientForHost } from "./host-clients";
import { apiErrorException } from "./api-error-exception";
import { RouteErrorCapture, reportRouteError } from "../components/RouteErrorCapture";
import { captureRendererException } from "./telemetry";
import type { RendererTelemetryCaptureInput } from "../../shared/telemetry-policy";

const capture = vi.hoisted(() => vi.fn<(input: RendererTelemetryCaptureInput) => Promise<boolean>>(async () => true));
vi.mock("./bridge", () => ({ aoBridge: { telemetry: { getBootstrap: async () => null, capture } } }));

afterEach(() => { queryClient.clear(); capture.mockClear(); vi.restoreAllMocks(); });

it("reports a non-API query error once across cache, route and React StrictMode without private query keys", async () => {
	const error = new TypeError("private query details");
	await expect(queryClient.fetchQuery({ queryKey: ["private-project-id"], retry: false, queryFn: () => { throw error; } })).rejects.toBe(error);
	const view = render(<StrictMode><RouteErrorCapture error={error} reset={() => {}} /></StrictMode>);
	await waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ kind: "exception", message: "Renderer error", tags: { operation: "query" } });
	expect(JSON.stringify(capture.mock.calls)).not.toMatch(/private query details|private-project-id/);
	view.unmount();
});

it("reports non-API mutation failures without exposing their inputs", async () => {
	const error = new RangeError("private mutation details");
	const mutation = queryClient.getMutationCache().build(queryClient, {
		mutationKey: ["private-mutation-key"], retry: false,
		mutationFn: async (_variables: { password: string }) => { throw error; },
	});
	await expect(mutation.execute({ password: "private-password" })).rejects.toBe(error);
	await waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ tags: { operation: "mutation" } });
	expect(JSON.stringify(capture.mock.calls)).not.toMatch(/private-/);
});

it.each([false, true])("does not duplicate raw or wrapped API errors (wrapped=%s)", async (wrapped) => {
	setApiBaseUrl("http://127.0.0.1:3001");
	vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response(JSON.stringify({ code: "INTERNAL_ERROR", message: "private server details" }), { status: 500 }));
	await expect(queryClient.fetchQuery({ queryKey: ["api", wrapped], retry: false, queryFn: async () => {
		const { error } = await apiClient.GET(wrapped ? "/api/v1/sessions" : "/api/v1/projects");
		throw wrapped ? apiErrorException(error) : error;
	} })).rejects.toBeDefined();
	await waitFor(() => expect(capture).toHaveBeenCalled());
	expect(capture).toHaveBeenCalledOnce();
	expect(capture.mock.calls[0][0]).toMatchObject({ kind: "message" });
});

it("still reports missing data without an API error and ignores caller cancellation", async () => {
	await expect(queryClient.fetchQuery({ queryKey: ["missing-data"], retry: false, queryFn: () => { throw apiErrorException(undefined, "private missing data"); } })).rejects.toThrow();
	await waitFor(() => expect(capture).toHaveBeenCalledOnce());
	await captureRendererException(new DOMException("cancelled", "AbortError"));
	expect(capture).toHaveBeenCalledOnce();
});

it("captures a real router loader rejection once while preserving silent session-route fallbacks", async () => {
	const root = createRootRoute();
	const error = new SyntaxError("private route id");
	const child = createRoute({ getParentRoute: () => root, path: "/", loader: () => { throw error; }, errorComponent: RouteErrorCapture });
	const router = createRouter({ routeTree: root.addChildren([child]), history: createMemoryHistory({ initialEntries: ["/"] }), defaultOnCatch: reportRouteError });
	await router.load();
	const view = render(<StrictMode><RouterProvider router={router} /></StrictMode>);
	await waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(JSON.stringify(capture.mock.calls)).not.toContain("private route id");
	view.unmount();
});

it("does not suppress an unknown client exception merely because it was formatted as an API message", async () => {
	await expect(queryClient.fetchQuery({ queryKey: ["client-bug"], retry: false, queryFn: () => { throw apiErrorException(new TypeError("private client computation")); } })).rejects.toThrow();
	await waitFor(() => expect(capture).toHaveBeenCalledOnce());
	expect(capture.mock.calls[0][0]).toMatchObject({ kind: "exception" });
});

it("deduplicates a non-JSON API error body while preserving its UI message", async () => {
	setApiBaseUrl("http://127.0.0.1:3001");
	vi.spyOn(globalThis, "fetch").mockResolvedValue(new Response("upstream gateway failure", { status: 502 }));
	await expect(queryClient.fetchQuery({ queryKey: ["plain-api-body"], retry: false, queryFn: async () => {
		const { error } = await apiClient.GET("/api/v1/settings"); throw error;
	} })).rejects.toThrow("upstream gateway failure");
	expect(capture).toHaveBeenCalledOnce();
	expect(capture.mock.calls[0][0]).toMatchObject({ kind: "message" });
});

it("does not report intentional router redirects or not-found results", async () => {
	reportRouteError(notFound()); reportRouteError(redirect({ href: "/" }));
	await Promise.resolve();
	expect(capture).not.toHaveBeenCalled();
});


it.each(["Sign in to AO Cloud to use this feature", "Unexpected handler failure"])("keeps IPC rejection owned by main: %s", async (detail) => {
	const error = new Error(`Error invoking remote method 'remotes:previewUrl': Error: ${detail}`);
	await expect(queryClient.fetchQuery({ queryKey: ["ipc"], retry: false, queryFn: () => { throw error; } })).rejects.toBe(error);
	const wrapped = apiErrorException(error);
	const mutation = queryClient.getMutationCache().build(queryClient, { retry: false, mutationFn: async () => { throw wrapped; } });
	await expect(mutation.execute(undefined)).rejects.toBe(wrapped);
	reportRouteError(wrapped);
	await Promise.resolve();
	expect(capture).not.toHaveBeenCalled();
});

it("does not report expected disconnected-host query or mutation failures", async () => {
	const queryFn = () => clientForHost("disconnected-private-host");
	await expect(queryClient.fetchQuery({ queryKey: ["disconnected-host"], retry: false, queryFn })).rejects.toThrow("is not connected");
	const mutation = queryClient.getMutationCache().build(queryClient, { retry: false, mutationFn: async () => queryFn() });
	await expect(mutation.execute(undefined)).rejects.toThrow("is not connected");
	expect(capture).not.toHaveBeenCalled();
});
