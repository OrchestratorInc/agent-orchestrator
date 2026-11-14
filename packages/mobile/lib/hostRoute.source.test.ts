import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

describe("retained-stack host routes", () => {
	it("qualifies shell and preview links before they leave Chat", () => {
		const chat = source("./chat/ChatSessionScreen.tsx");
		expect(chat).toMatch(/pathname: "\/shell\/\[handleId\]"[^\n]*hostId:/);
		expect(chat).toMatch(/pathname: "\/preview\/\[id\]"[^\n]*hostId:/);
	});

	it("does not mount a terminal or preview for an unqualified route", () => {
		const shell = source("../app/shell/[handleId].tsx");
		const preview = source("../app/preview/[id].tsx");
		expect(shell).toContain("hostRouteMatches(routeHostId, currentHostId)");
		expect(shell).toContain("<TerminalSessionScreen /></View>;");
		expect(preview).toContain("hostRouteMatches(routeHostId, currentHostId)");
		expect(preview).toContain("heldPreview(");
	});

	it("routes terminal previews through the owning host without leaking another credential", () => {
		const terminal = source("./session/TerminalSessionScreen.tsx");
		const preview = source("../app/preview/[id].tsx");
		expect(terminal).toContain("hostId: params.hostId");
		// The URL and its credential always come from the same connection (see heldPreview).
		expect(preview).toContain("heldPreview(loaded, config, routeHostId)");
		expect(preview).toContain("authHeaders(shown.config)");
	});

	// Through a reconnect the page on screen keeps the old connection's headers,
	// but nothing new goes out with them until the machine answers as itself.
	it("reloads or mounts the app preview with its credential only on a verified connection", () => {
		const preview = source("../app/preview/[id].tsx");
		expect(preview).toContain("const appWaits = showingApp && !config && !appMounted.current;");
		expect(preview).toContain(": appWaits ? <View style={styles.center}><EmptyState busy title=\"Connecting to this machine…\" /></View> : <View style={styles.appPane}>");
		const reload = preview.slice(preview.indexOf("const reloadApp = useCallback"), preview.indexOf("}, [config]);"));
		expect(reload.indexOf("if (!currentConfig.current) {")).toBeLessThan(reload.indexOf("web.current?.reload();"));
		expect(reload).toContain("if (!config || !reloadWhenVerified.current) return;");
		expect(preview).toContain("pullToRefreshEnabled={Boolean(config)}");
	});

	it("waits for a verified connection before an artifact or changed image loads afresh with the credential", () => {
		const artifact = source("../app/artifact/[sessionId].tsx");
		const body = artifact.slice(artifact.indexOf("function ArtifactBody("), artifact.indexOf("function NoPreview("));
		expect(body.indexOf("const verified = useVerifiedOnMount(backend);")).toBeLessThan(body.indexOf("backend.webSource("));
		const web = artifact.slice(artifact.indexOf("function ArtifactWeb("), artifact.indexOf("function ArtifactImage("));
		expect(web).toContain("const verified = useVerifiedOnMount(backend);");
		expect(web).toContain("afterVerified(backend, () => web.current?.reload(), retrying.current.signal);");
		expect(web).not.toContain("setError(undefined); web.current?.reload();");
		expect(artifact).toContain("const picture = verified && artifactViewKind(item) === \"image\"");
		expect(source("./ArtifactPoster.tsx")).toContain("const picture = verified && reached && thumbnail");
		const file = source("../app/files/[sessionId]/file.tsx");
		expect(file).toContain("onPress={() => afterVerified(backend, () => setViewing(pane))}");
		expect(file).toContain("if (!verified) return <View style={styles.center}><EmptyState busy title={`Opening ${name}…`} /></View>;");
	});

	it("requires the owning host for review detail, reviewer chat, and actions", () => {
		for (const path of ["../app/review/[sessionId].tsx", "../app/reviewer/[reviewId].tsx", "../app/sheets/review-actions.tsx"]) {
			const route = source(path);
			expect(route).toContain("<HostScope key={hostId} hostId={hostId}>");
			expect(route).toContain("hostRouteMatches(routeHostId, currentHostId)");
		}
		expect(source("../app/review/[sessionId].tsx")).toContain("hostId: routeHostId");
		expect(source("./PRCard.tsx")).toContain("reviewRouteForPR(session.id, pr, hostId)");
	});
});
