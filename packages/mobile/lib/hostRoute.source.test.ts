import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = (path: string) => readFileSync(new URL(path, import.meta.url), "utf8");

/** The text from `start` up to `end`; both must be there, or the slice would be the wrong text. */
function between(text: string, start: string, end: string): string {
	const from = text.indexOf(start);
	expect(from, start).toBeGreaterThanOrEqual(0);
	const to = text.indexOf(end, from);
	expect(to, end).toBeGreaterThan(from);
	return text.slice(from, to);
}

/** `first` comes before `then`, and both are there: a missing one is -1, which would pass a bare comparison. */
function expectBefore(text: string, first: string, then: string) {
	const a = text.indexOf(first);
	const b = text.indexOf(then);
	expect(a, first).toBeGreaterThanOrEqual(0);
	expect(b, then).toBeGreaterThanOrEqual(0);
	expect(a).toBeLessThan(b);
}

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
		expect(preview).toContain(": appWaits ? <View style={styles.center}>{machine === \"rejected\" ? <MachineRejectedState /> : <EmptyState busy title=\"Connecting to this machine…\" />}</View> : <View style={styles.appPane}>");
		const reload = between(preview, "const reloadApp = useCallback", "}, [config]);");
		expectBefore(reload, "if (!currentConfig.current) {", "web.current?.reload();");
		expect(reload).toContain("if (!config || !reloadWhenVerified.current) return;");
		expect(preview).toContain("pullToRefreshEnabled={Boolean(config)}");
	});

	it("waits for a verified connection before an artifact or changed image loads afresh with the credential", () => {
		const artifact = source("../app/artifact/[sessionId].tsx");
		const body = between(artifact, "function ArtifactBody(", "function NoPreview(");
		expectBefore(body, "if (!verified) return", "backend.webSource(");
		const web = between(artifact, "function ArtifactWeb(", "function ArtifactImage(");
		// Built from the live connection where it is gated, never handed down from a parent's render.
		expectBefore(web, "const source = backend.webSource(file.sessionId, file.path);", "const verified = useVerifiedFor(backend, source ? sourceKey(source) : \"unresolved\");");
		expect(web).toContain("if (!source || !verified ||");
		// Retry is a fresh view built once verified, not a reload of the address on screen.
		expect(web).toContain("afterVerified(backend, () => setAttempt((n) => n + 1), retrying.current.signal);");
		expect(web).toContain("key={`${version}|${inlined.html ? \"inline\" : \"url\"}|${attempt}`}");
		expect(web).not.toContain("web.current?.reload()");
		expect(artifact).not.toMatch(/<ArtifactWeb[^>]*source=/);
		const file = source("../app/files/[sessionId]/file.tsx");
		expect(file).toContain("if (!verified) return <View style={styles.center}><EmptyState busy title={`Opening ${name}…`} /></View>;");
	});

	// A view mounted while connected but loading only later (off screen, a page
	// not yet swiped to) checks when it first loads, not when it mounted.
	it("checks a picture, a strip tile, a player and a document peek when they first load", () => {
		const poster = source("./ArtifactPoster.tsx");
		expectBefore(poster, "const wanted = reached && thumbnail && failedUri !== thumbnail.uri ? thumbnail : undefined;", "const picture = useVerifiedFor(backend, sourceKey(wanted)) ? wanted : undefined;");
		const artifact = source("../app/artifact/[sessionId].tsx");
		const strip = between(artifact, "function StripPreview(", "function PagerTitle(");
		expectBefore(strip, "backend.imageSource(sessionId, artifact.path, artifact.updatedAt)", "const picture = useVerifiedFor(backend, sourceKey(wanted)) ? wanted : undefined;");
		expect(between(artifact, "function ArtifactFilmstrip(", "function StripPreview(")).toContain("<StripPreview ");
		const body = between(artifact, "function ArtifactBody(", "function NoPreview(");
		expectBefore(body, "const playable = useVerifiedFor(backend, active && (kind === \"video\" || kind === \"audio\") ? \"play\" : null);", "const stream = backend.streamSource(");
		expect(body).toContain("active={active && playable}");
		const thumbnail = source("./ArtifactThumbnail.tsx");
		expectBefore(thumbnail, "const drawable = useVerifiedFor(backend, markdown && value ? key : null);", "if (!drawable) return null;");
		expectBefore(thumbnail, "if (!drawable) return null;", "<ChatMarkdown text={value} resolveImage={resolveImage}");
	});

	// The pane on screen may carry the last connection's address and credential.
	it("opens the full-screen image through the verified connection, not the pane's source", () => {
		const file = source("../app/files/[sessionId]/file.tsx");
		const compare = between(file, "function ImageCompare(", "const makeStyles = ");
		expect(compare).toContain("void resolveWhenVerified(backend, (signal) => sources.resolveSide(pane.side, signal), controller.signal).then((source) => {");
		expect(compare).toContain("if (source && !controller.signal.aborted) setViewing({ label: pane.label, source });");
		expect(compare).toContain("onPress={() => open(pane)}");
		expect(compare).not.toContain("setViewing(pane))");
		expect(file).toContain("backend.imageSource(sessionId, path, which, version, read, signal)");
	});

	// A read that waits for the connection is cancelled with the screen that asked for it.
	it("cancels a file, folder, search or image read with the screen that asked for it", () => {
		const file = source("../app/files/[sessionId]/file.tsx");
		expect(file).toContain("useEffect(() => () => reading.current?.abort(), []);");
		expect(file).toContain("await branchBackend.file(id, path, read, controller.signal);");
		expect(file).toContain("retryWhenVerified(backend, (signal) => backend.imageSource(sessionId, path, which, version, read, signal), controller.signal)");
		const folder = source("./files/FolderList.tsx");
		expect(folder).toContain("useEffect(() => () => inFlight.current?.abort(), []);");
		expect(folder).toContain("await backend.tree(sessionId, dir, controller.signal);");
		expect(folder).toContain("backend.search(sessionId, trimmed, controller.signal)");
		expect(folder).toContain("return () => { controller.abort(); clearTimeout(timer); };");
		const sender = source("./files/useReviewSender.ts");
		expect(sender).toContain("}, signal);");
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

	// The app closes the connection on purpose every time it goes to the
	// background; a machine that refused this phone has stopped polling, so a
	// screen that waited on it spun for good.
	it("says a machine is offline or refused this phone only by the shared rule, never for a connection an app switch closed", () => {
		const screens: [path: string, gate: string, state: string][] = [
			["../app/preview/[id].tsx", "const machine = !config ? machineState(app) : \"ok\";", "machine"],
			["../app/artifact/[sessionId].tsx", "const machine = !backend && !cloud ? machineState(app) : \"ok\";", "machine"],
			["../app/artifacts/[sessionId].tsx", "const machine = !backend && !cloud ? machineState(app) : \"ok\";", "machine"],
			["../app/files/[sessionId]/index.tsx", "const machine = !backend ? machineState(app) : \"ok\";", "machine"],
			["../app/files/[sessionId]/folder.tsx", "const machine = !backend ? machineState(app) : \"ok\";", "machine"],
			["../app/files/[sessionId]/file.tsx", "const machine = !backend ? machineState(app) : \"ok\";", "machine"],
			["../app/shell/[handleId].tsx", "const state = !config && !opened ? machineState(app) : \"ok\";", "state"],
		];
		for (const [path, gate, state] of screens) {
			const screen = source(path);
			expect(screen, path).not.toContain('connection === "closed"');
			expect(screen, path).not.toContain("machineOffline");
			expect(screen, path).toContain("const app = useApp();");
			expect(screen, path).toContain(gate);
			// The refusal gets the board's copy and repair, before "Machine offline".
			expectBefore(screen, `${state} === "rejected"`, `${state} === "offline"`);
			expect(between(screen, `${state} === "rejected"`, `${state} === "offline"`), path).toContain("<MachineRejectedState />");
		}
		// Preview's held app page, not yet mounted, waits for a verified connection;
		// not for one that refused this phone.
		expect(source("../app/preview/[id].tsx")).toContain("appWaits ? <View style={styles.center}>{machine === \"rejected\" ? <MachineRejectedState /> : <EmptyState busy title=\"Connecting to this machine…\" />}</View>");
		// The live browser pane, kept on screen through an app switch.
		const pane = source("./preview/BrowserLivePane.tsx");
		expect(pane).toContain("const state = config ? \"ok\" : machineState(app);");
		expect(pane).toContain("rejectedMachineLine(app.errorStatus)");
	});

	// The board's copy and the repair Settings offers; never a Retry, which would
	// only spend another attempt toward the daemon's lockout.
	it("words a refusal as the board does, offering only a new code", () => {
		const state = source("./MachineRejectedState.tsx");
		expect(state).toContain("const copy = rejectedMachineCopy(errorStatus, config?.host);");
		expect(state).toContain("router.push({ pathname: \"/pair\", params: { repairHostId: currentHostId } })");
		expect(state.match(/<Button /g)).toEqual(["<Button "]);
		expect(state).not.toMatch(/refresh\(|reload/);
		const route = source("../app/session/[id].tsx");
		expect(between(route, "case \"rejected\":", "case \"ended\":")).toContain("<MachineRejectedState action={<Button key=\"board\" title=\"Open board\"");
		expect(route).toContain("sessionRouteView({ listed, paired, unreachable, connection, error, errorStatus, loading,");
	});

	// Each machine's projects keep their rows through an app switch, so a bare
	// closed connection marked every one "(offline)", in the app-switcher snapshot too.
	// A machine's state is its freshness dot now, which waits out a blip.
	it("never marks a machine's projects offline for a closed connection", () => {
		const rows = source("./hostedRows.ts");
		expect(rows).not.toContain('connection === "closed"');
		expect(rows).toContain("hostName: host.name,");
		const projects = source("../app/(tabs)/(projects)/projects.tsx");
		expect(projects).toContain("freshness={showMachine ? freshness.get(item.project.hostId) : undefined}");
	});
});
