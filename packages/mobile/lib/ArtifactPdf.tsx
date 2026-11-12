import { useEffect, useMemo, useRef, useState } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import PdfRendererView from "react-native-pdf-renderer";
import { ArtifactTooLargeError, discardArtifact, downloadArtifact } from "./artifactDownload";
import { artifactCacheEntry, MAX_ARTIFACT_DOWNLOAD_BYTES, type SessionArtifact } from "./artifacts";
import type { ArtifactsBackend } from "./artifactsBackend";
import { userFacingError } from "./connectionError";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { Button, EmptyState } from "./ui";
import { useGlassBarInset } from "./useHeaderOverlap";

type PdfState = { key: string; uri?: string; error?: string; tooLarge?: boolean; renewing?: boolean };

// How long a refused Cloud download waits for renewed access before saying it failed.
const RENEW_WAIT_MS = 10_000;

/**
 * A PDF artifact drawn by the platform's PDF renderer (Android's PdfRenderer),
 * pages scrolled and pinch-zoomed, as the desktop shows its pages. Android's
 * WebView has no PDF viewer, so the file is downloaded to the cache first,
 * with the connection's credential, and drawn from there.
 *
 * Only the page on screen downloads: a neighbouring page in the gallery waits
 * until it is swiped to (a download it started keeps going, and is picked up
 * again). A file over MAX_ARTIFACT_DOWNLOAD_BYTES is not downloaded at all.
 *
 * Cloud's address is signed and expires, and the native download cannot renew
 * it itself: a refused download asks for fresh access once (`backend.renew`),
 * and the renewed backend downloads again. Try again may ask once more.
 */
export function ArtifactPdf({ backend, sessionId, artifact, active, label, hint }: {
	/** The paired machine's daemon, or Orchestrator Cloud. */
	backend: ArtifactsBackend;
	sessionId: string;
	artifact: Pick<SessionArtifact, "path" | "updatedAt" | "size">;
	/** Whether this is the page on screen. */
	active: boolean;
	/** What the file is, for when it is too large: "PDF file · 240.0 MB". */
	label: string;
	/** Where else it can be opened. */
	hint: string;
}) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const glassInset = useGlassBarInset();
	// Session ids are only unique on one machine: the same path on two paired machines is two files.
	const machine = backend.key;
	const key = `${machine}|${sessionId}|${artifact.path}|${artifact.updatedAt}`;
	const entry = useMemo(() => artifactCacheEntry(machine, sessionId, artifact.path, artifact.updatedAt), [artifact.path, artifact.updatedAt, machine, sessionId]);
	const [state, setState] = useState<PdfState>();
	const [attempt, setAttempt] = useState(0);
	const renewed = useRef(false);
	// Waiting on renewed access is not settled: the renewed backend runs the download again.
	const settled = state?.key === key && !state.renewing;
	useEffect(() => {
		if (!active || settled) return;
		let live = true;
		const source = backend.streamSource(sessionId, artifact.path, artifact.updatedAt);
		if (!source) {
			setState({ key, error: "Its path isn't one the phone can request." });
			return;
		}
		(async () => {
			// The list gives the size; a file it does not list is asked (one byte, see getArtifactSize).
			// Cloud does not say a size beforehand: the download's own check then decides.
			const size = artifact.size > 0 ? artifact.size : await backend.size?.(sessionId, artifact.path).catch(() => undefined);
			if (size !== undefined && size > MAX_ARTIFACT_DOWNLOAD_BYTES) throw new ArtifactTooLargeError();
			return downloadArtifact(source, entry, { maxBytes: MAX_ARTIFACT_DOWNLOAD_BYTES });
		})().then(
			(uri) => { if (live) setState({ key, uri }); },
			(cause) => {
				if (!live) return;
				if (cause instanceof ArtifactTooLargeError) {
					setState({ key, tooLarge: true });
					return;
				}
				if (backend.renew && !renewed.current) {
					renewed.current = true;
					backend.renew();
					setState({ key, renewing: true });
					return;
				}
				setState({ key, error: userFacingError(cause, "The PDF didn't download.") });
			},
		);
		return () => { live = false; };
	}, [active, artifact.path, artifact.size, artifact.updatedAt, attempt, backend, entry, key, sessionId, settled]);
	// Renewal that brings no new access ends in the error, with Try again.
	const renewing = state?.key === key && state.renewing === true;
	useEffect(() => {
		if (!renewing) return;
		const timer = setTimeout(() => setState({ key, error: "Orchestrator Cloud didn't renew access to this file." }), RENEW_WAIT_MS);
		return () => clearTimeout(timer);
	}, [key, renewing]);
	const retry = <Button title="Try again" icon="refresh-cw" onPress={() => { renewed.current = false; setState(undefined); setAttempt((n) => n + 1); }} />;
	if (settled && state.tooLarge) return <View style={styles.center}><EmptyState icon="file" title="Too large to preview on the phone" message={`${label}\n${hint}`} /></View>;
	if (settled && state.error) return <View style={styles.center}><EmptyState icon="file" title="Couldn't load this PDF" message={state.error} action={retry} /></View>;
	if (!settled || !state.uri) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	return (
		<View style={[styles.screen, { paddingTop: glassInset }]}>
			<PdfRendererView
				source={state.uri}
				distanceBetweenPages={8}
				maxZoom={5}
				onError={() => {
					// Try again downloads a fresh copy rather than reopening the one that failed.
					discardArtifact(entry);
					setState({ key, error: "The phone couldn't read this file as a PDF." });
				}}
				style={styles.pdf}
			/>
		</View>
	);
}

const makeStyles = (t: Theme) => StyleSheet.create({
	screen: { flex: 1, backgroundColor: t.bgBase },
	center: { flex: 1, alignItems: "center", justifyContent: "center", backgroundColor: t.bgBase },
	pdf: { flex: 1, backgroundColor: t.bgBase },
});
