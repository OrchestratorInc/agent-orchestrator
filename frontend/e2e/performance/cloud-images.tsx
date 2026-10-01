/// <reference path="../../src/renderer/global.d.ts" />
// Visual fixture with real shared UI, upload hooks and Cloud TUI controls.
// Storage/control-plane responses are mocked. Go integration tests exercise
// the real signed HTTP storage, PostgreSQL links and worker downloads.
import { useCallback, useState } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { TaskComposerView } from "@aoagents/product-ui";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import { ChatComposer } from "../../src/renderer/components/chat/ChatComposer";
import { AttachmentPreview } from "../../src/renderer/components/chat/AttachmentPreview";
import { CloudTerminalAttachments } from "../../src/renderer/components/CloudTerminalAttachments";
import { useFileAttachments } from "../../src/renderer/hooks/useFileAttachments";
import { CLOUD_IMAGE_LIMITS, uploadCloudAttachments } from "../../src/renderer/lib/cloud-attachments";
import { createRendererCloudCpClient } from "../../src/renderer/lib/cloud-cp/renderer-client";
import { settingsQueryKey } from "../../src/renderer/hooks/useSettings";
import type { WorkspaceSession } from "../../src/renderer/types/workspace";
import { appI18n } from "../../src/renderer/i18n";
import { I18nextProvider } from "react-i18next";
import "../../src/renderer/styles.css";

const canvas = document.createElement("canvas");
canvas.width = 320;
canvas.height = 160;
const ctx = canvas.getContext("2d")!;
ctx.fillStyle = "#24293a";
ctx.fillRect(0, 0, 320, 160);
ctx.fillStyle = "#f3c85b";
ctx.fillRect(20, 30, 280, 30);
ctx.fillStyle = "#87baff";
ctx.fillRect(20, 80, 120, 55);
ctx.fillRect(155, 80, 145, 55);
ctx.fillStyle = "#24293a";
ctx.font = "16px sans-serif";
ctx.fillText("Layout to review", 35, 51);
const preview = canvas.toDataURL("image/png");
const orgId = "11111111-1111-4111-8111-111111111111",
	projectId = "22222222-2222-4222-8222-222222222222",
	sessionId = "33333333-3333-4333-8333-333333333333";
const images = new Map<string, Record<string, unknown>>();
const originalFetch = window.fetch.bind(window);
window.fetch = async (input, init) =>
	String(input).includes("/mock-image-storage/")
		? new Response(null, { status: 204 })
		: originalFetch(input, init);
window.ao = {
	cloudCp: {
		request: async ({ path, body }: { path: string; body?: string }) => {
			const input = JSON.parse(body ?? "{}");
			let output: unknown;
			if (path.endsWith("/attachments")) {
				const id = crypto.randomUUID();
				const attachment = { ...input, id, status: "pending" };
				images.set(id, attachment);
				output = {
					attachment,
					upload: {
						url: `/mock-image-storage/${id}`,
						fields: {},
						expiresAt: new Date(Date.now() + 600000).toISOString(),
					},
				};
			} else if (path.endsWith("/complete")) {
				const id = path.split("/").at(-2)!;
				const attachment = images.get(id)!;
				attachment.status = "ready";
				output = { attachment };
			} else if (path.endsWith("/read-grant"))
				output = { url: preview, expiresAt: new Date(Date.now() + 300000).toISOString() };
			else if (path.endsWith("/materialize"))
				output = {
					paths: input.attachmentIds.map((id: string) => `.ao/attachments/image-${id}.png`),
					workerId: "fixture-worker",
					epoch: 4,
				};
			else output = {};
			return { status: 200, headers: { "Content-Type": "application/json" }, body: JSON.stringify(output) };
		},
	},
} as unknown as typeof window.ao;
const client = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
client.setQueryData(settingsQueryKey, {
	cloudEnabled: true,
	cloudOffering: true,
	localEnabled: true,
	cloudControlPlaneUrl: location.origin,
	chatHarnesses: ["codex"],
	defaultSessionMode: "tui",
	client: "fixture",
	trackerIntakeEnabled: false,
});
const cp = createRendererCloudCpClient(location.origin);
const resolve = async () => preview;
const session = {
	id: sessionId,
	workspaceId: projectId,
	cloud: { orgId },
	kind: "worker",
	terminalGeneration: "4",
} as WorkspaceSession;
const upload = (files: Parameters<typeof uploadCloudAttachments>[5]) =>
	uploadCloudAttachments(cp, location.origin, orgId, projectId, sessionId, files);
function Fixture() {
	const task = useFileAttachments({ uploadFiles: upload, limits: CLOUD_IMAGE_LIMITS });
	const [pasted, setPasted] = useState<File[]>();
	const [terminal, setTerminal] = useState("");
	const [sent, setSent] = useState<string>();
	const [created, setCreated] = useState<string>();
	const addExample = useCallback(async () => {
		const blob = await new Promise<Blob>((resolve) => canvas.toBlob((blob) => resolve(blob!), "image/png"));
		const file = new File([blob], "layout.png", { type: "image/png" });
		await task.addFiles([file]);
		const clipboard = new DataTransfer();
		clipboard.items.add(file);
		document
			.querySelector('[aria-label="Message the agent"]')
			?.dispatchEvent(
				new ClipboardEvent("paste", { bubbles: true, cancelable: true, clipboardData: clipboard }),
			);
		setPasted([file]);
	}, [task.addFiles]);
	return (
		<main className="mx-auto max-w-3xl space-y-6 p-6">
			<header>
				<h1 className="text-xl font-semibold">Cloud image verification</h1>
				<p className="text-sm text-muted-foreground">
					Real composers. Mocked control plane and storage. No agent is running.
				</p>
				<button className="mt-3 rounded border px-3 py-2 text-sm" onClick={() => void addExample()}>
					Load example into composers
				</button>
			</header>
			<section className="rounded-lg border p-4">
				<h2 className="mb-2 font-medium">New Cloud task</h2>
				<TaskComposerView
					canSubmit={true}
					initialPrompt="Review this layout"
					onPromptChange={() => {}}
					showEffort={false}
					agent={{
						disabled: false,
						label: "Agent",
						placeholder: "Agent",
						value: "codex",
						onChange: () => {},
					}}
					model={{
						agentId: "codex",
						agentLabel: "Codex",
						disabled: false,
						fetching: false,
						loading: false,
						mode: "",
						value: "",
						projectId,
						onModeChange: () => {},
						onModelChange: () => {},
					}}
					effort={{ disabled: false, value: "", options: [], onChange: () => {} }}
					renderAgentControl={() => <span className="px-2 text-sm">Codex</span>}
					renderModelControl={() => null}
					renderEffortControl={() => null}
					labels={{
						addFile: "Attach image",
						effort: "Effort",
						fallbackAction: "Use TUI",
						removeFile: (name) => `Remove ${name}`,
						runsWith: "Runs with",
						start: "Start task",
						starting: "Starting",
						task: "Task",
						taskPlaceholder: "Describe a task",
					}}
					attachments={{
						items: task.attachments.map((a) => ({
							id: a.id,
							name: a.name,
							preview: a.attachmentId ? (
								<AttachmentPreview
									id={a.attachmentId}
									resolve={resolve}
									alt={a.name}
									className="size-full object-cover"
								/>
							) : undefined,
						})),
						error: task.error,
						onAddFiles: (files) => void task.addFiles(files),
						onRemove: task.remove,
					}}
					submission={{
						isSubmitting: false,
						showFallbackAction: false,
						onFallbackAction: () => {},
						onSubmit: async () => {
							await task.toSettledPayload();
							setCreated(`Task references ${task.getAttachments().length} verified image ID.`);
						},
					}}
				/>
				{created && (
					<p role="status" className="mt-2 text-sm">
						{created}
					</p>
				)}
			</section>
			<section className="rounded-lg border p-4">
				<h2 className="mb-2 font-medium">Cloud chat</h2>
				<ChatComposer
					onUploadAttachments={upload}
					resolveAttachmentPreview={resolve}
					onSend={async (_text, _files, _key, _retained, ids) =>
						setSent(`Message references ${ids?.length ?? 0} image ID.`)
					}
				/>
				{sent && (
					<p role="status" className="mt-2 text-sm">
						{sent}
					</p>
				)}
			</section>
			<section className="rounded-lg border p-4">
				<h2 className="mb-2 font-medium">Cloud agent TUI</h2>
				<CloudTerminalAttachments
					session={session}
					disabled={false}
					pastedFiles={pasted}
					onInsert={(paths, epoch) => {
						if (epoch !== 4) return false;
						setTerminal(paths.map((path) => `'${path}'`).join(" ") + " ");
						return true;
					}}
				/>
				<textarea
					aria-label="Terminal pending input"
					className="mt-3 h-20 w-full resize-none rounded bg-muted p-3 font-mono text-xs"
					value={terminal}
					readOnly
				/>
				<p className="text-xs text-muted-foreground">Acknowledged path only. Nothing has been submitted.</p>
			</section>
		</main>
	);
}
createRoot(document.getElementById("root")!).render(
	<QueryClientProvider client={client}>
		<I18nextProvider i18n={appI18n}>
			<TooltipProvider>
				<Fixture />
			</TooltipProvider>
		</I18nextProvider>
	</QueryClientProvider>,
);
