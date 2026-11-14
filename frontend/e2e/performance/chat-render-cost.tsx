import type { ReactNode } from "react";
import { ChatWorkspace } from "../../src/renderer/components/chat/ChatWorkspace";
import { chatFixtureLongHistory } from "../../src/renderer/lib/chat-fixture";
import { useUiStore } from "../../src/renderer/stores/ui-store";
import type { ConversationSnapshot } from "../../src/renderer/types/conversation";

// Deliberately include misses, basenames, and absolute paths, rather than only
// exact hits at the front of the catalog. Every refresh is new JSON, as on wire.
const paths = Array.from({ length: 6000 }, (_, i) => `src/feature${i}/file${i}.ts`);
const references = (i: number) => [
	paths[i * 7], `file${i * 7 + 1}.ts:12`,
	`/repo/${paths[i * 7 + 2]}`, `feature${i * 7 + 3}/file${i * 7 + 3}.ts`,
	"not_a_file", "missing.ts",
].map((path) => `\`${path}\``).join(", ");

export function chatRenderCostWorkload(
	render: (node: ReactNode) => void,
	frame: () => Promise<number>,
) {
	return async () => {
		render(null);
		useUiStore.setState({ inspectorSessions: { "ao-long": { isOpen: false, view: "summary" } } });
		const history = chatFixtureLongHistory(18);
		const base: ConversationSnapshot = {
			...history,
			branchPoints: [],
			turns: history.turns.map((turn, i) => i === 17
				? { ...turn, state: "running", completedAt: undefined } : turn),
			items: history.items.map((item) => item.kind === "message" && item.role === "assistant"
				? { ...item, text: `Reviewed ${references(Number(item.id.slice(3)))}.`, streaming: item.turnId === "turn-h17" }
				: item),
		};
		let opened = "";
		let version = 0;
		let snapshot = base;
		const draw = (width = 1440) => {
			const currentVersion = ++version;
			render(<div style={{ height: "100%", width }}><ChatWorkspace
				snapshot={snapshot} sessionTitle="Chat render benchmark" filePaths={paths}
				onOpenFile={(path, line) => { opened = `${currentVersion}:${path}:${line ?? ""}`; }}
				onLinkOpen={() => {}} onSessionLinkOpen={() => {}}
			/></div>);
		};
		draw();
		await document.fonts.ready;
		await new Promise((resolve) => setTimeout(resolve, 700));
		const scroller = document.querySelector<HTMLElement>('[role="log"][aria-label="Conversation"]');
		if (!scroller) throw new Error("Conversation did not mount");
		const samples: Record<string, { renderMs: number[]; paintMs: number[]; frameGaps: number[] }> = {};
		const longTasks: number[] = [];
		const observer = new PerformanceObserver((list) => {
			longTasks.push(...list.getEntries().map((entry) => entry.duration));
		});
		observer.observe({ type: "longtask" });
		let previousFrame = await frame();
		for (const phase of ["parent", "streaming", "resize", "scroll"]) {
			const result = { renderMs: [] as number[], paintMs: [] as number[], frameGaps: [] as number[] };
			samples[phase] = result;
			for (let i = 0; i < 20; i++) {
				const start = performance.now();
				if (phase === "streaming") {
					snapshot = structuredClone(base);
					snapshot.items = snapshot.items.map((item) => item.id === "hr-17"
						? { ...item, text: `${item.kind === "message" ? item.text : ""} Streaming update ${i}.` } : item);
				}
				if (phase === "scroll") {
					scroller.scrollTop = (scroller.scrollHeight - scroller.clientHeight) * (i / 19);
					scroller.dispatchEvent(new Event("scroll"));
				} else draw(phase === "resize" ? 1100 + (i % 2) * 340 : 1440);
				result.renderMs.push(performance.now() - start);
				const now = await frame();
				result.frameGaps.push(now - previousFrame);
				previousFrame = now;
				await new Promise<void>((resolve) => {
					const channel = new MessageChannel();
					channel.port1.onmessage = () => { channel.port1.close(); channel.port2.close(); resolve(); };
					channel.port2.postMessage(0);
				});
				result.paintMs.push(performance.now() - start);
			}
		}
		await new Promise((resolve) => setTimeout(resolve, 500));
		observer.disconnect();
		const link = document.querySelector<HTMLButtonElement>('[aria-label="Open src/feature119/file119.ts in Files"]');
		if (!link) throw new Error("File link did not mount");
		link.click();
		const exact = scroller.textContent?.includes("Streaming update 19.") ?? false;
		const latestCallback = opened === `${version}:src/feature119/file119.ts:`;
		if (!exact || !latestCallback) throw new Error(`Lost live content or callback: ${exact}, ${opened}`);
		return { samples, longTasks, exact, latestCallback,
			loadedTurns: 18, catalogPaths: paths.length,
			mountedTurns: scroller.querySelectorAll("[data-chat-scroll-anchor]").length,
			fileLinks: scroller.querySelectorAll('[aria-label^="Open src/"]').length };
	};
}
