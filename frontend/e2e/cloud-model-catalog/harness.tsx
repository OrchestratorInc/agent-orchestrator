import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import type { AoBridge } from "../../src/preload";
import { appI18n } from "../../src/renderer/i18n";
import { TaskComposer } from "../../src/renderer/components/TaskComposer";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { agentReadiness } from "../../src/renderer/test/agent-readiness-fixtures";
import { setApiBaseUrl, setApiDaemonStatus } from "../../src/renderer/lib/api-client";
import { aoBridge } from "../../src/renderer/lib/bridge";
import "../../src/renderer/styles.css";

const variant = new URLSearchParams(location.search).get("variant") ?? "advisory";
setApiBaseUrl("http://127.0.0.1:8080");
setApiDaemonStatus({ state: "ready", port: 8080 });
const baseUrl = "https://catalog-fixture.example.test";
const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
client.setQueryData(["settings"], { defaultSessionMode: "tui", chatHarnesses: ["codex"], cloudEnabled: false, localEnabled: true, cloudControlPlaneUrl: baseUrl });
client.setQueryData(["cloud-org", baseUrl], { id: "fixture-org" });
client.setQueryData(["cloud-projects", baseUrl, "fixture-org"], [{ id: "fixture-project", displayName: "Fixture project", config: { worker: { agent: "codex", agentConfig: variant === "unavailable" ? { model: "configured-model", effort: "high" } : {} } } }]);
client.setQueryData(["cloud-provider-connections"], [{ id: "fixture-connection", provider: "codex", label: "default", validationState: "valid", config: {} }]);
client.setQueryData(["agent-readiness"], { agents: [agentReadiness("codex", "Codex")] });
const fixture = window as unknown as { ao: AoBridge; catalogCreates: Record<string, unknown>[] };
if (!fixture.ao) {
	fixture.ao = aoBridge;
	const originalFetch = window.fetch.bind(window);
	window.fetch = (input, init) => {
		const url = new URL(input instanceof Request ? input.url : String(input), location.href);
		if (url.origin === "http://127.0.0.1:8080" && url.pathname.startsWith("/api/v1/")) {
			return Promise.resolve(Response.json({ agent: "codex", selectionMode: "catalog", models: [{ id: "desktop-only", label: "Desktop-only suggestion", isDefault: true }], allowCustom: true }));
		}
		return originalFetch(input, init);
	};
}
fixture.catalogCreates = [];
fixture.ao.cloudCp.request = async ({ path, body }) => {
	if (path.endsWith("/models")) {
		if (variant === "legacy") return { status: 404, headers: {}, body: "404 page not found" };
		if (variant === "handler404") return { status: 404, headers: {}, body: JSON.stringify({ error: "Not Found", code: "not_found", message: "Session removed" }) };
		if (variant === "unavailable") return { status: 409, headers: {}, body: JSON.stringify({ error: "Conflict", code: "MODEL_CATALOG_UNAVAILABLE", message: "No worker" }) };
		return { status: 200, headers: {}, body: JSON.stringify({ model: "borrowed-model", models: [{ id: "borrowed-model", displayName: "Borrowed worker model", default: true, efforts: ["high"] }] }) };
	}
	if (path.endsWith("/sessions")) {
		fixture.catalogCreates.push(JSON.parse(body ?? "{}"));
		return { status: 201, headers: {}, body: JSON.stringify({ session: { id: "fixture-session" } }) };
	}
	return { status: 200, headers: {}, body: "{}" };
};

createRoot(document.getElementById("root")!).render(<QueryClientProvider client={client}>
	<I18nextProvider i18n={appI18n}><TooltipProvider><main className="mx-auto max-w-3xl p-8">
		<h1 className="mb-3 text-lg">Cloud model catalog</h1>
		<p role="note" className="mb-6 text-sm text-muted-foreground">Renderer fixture only. No worker, model, native transport or production calls.</p>
		<TaskComposer projectId="fixture-project" onCreated={() => undefined} />
	</main></TooltipProvider></I18nextProvider>
</QueryClientProvider>);
