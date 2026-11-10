import { useEffect, useState } from "react";
import { ActivityIndicator, StyleSheet, View } from "react-native";
import PdfRendererView from "react-native-pdf-renderer";
import { artifactStreamSource } from "./api";
import { downloadArtifact } from "./artifactDownload";
import { artifactCacheEntry, type SessionArtifact } from "./artifacts";
import type { ServerConfig } from "./config";
import { userFacingError } from "./connectionError";
import type { Theme } from "./theme";
import { useTheme, useThemedStyles } from "./ThemeProvider";
import { Button, EmptyState } from "./ui";
import { useGlassBarInset } from "./useHeaderOverlap";

type PdfState = { key: string; uri?: string; error?: string };

/**
 * A PDF artifact drawn by the platform's PDF renderer (Android's PdfRenderer),
 * pages scrolled and pinch-zoomed, as the desktop shows its pages. Android's
 * WebView has no PDF viewer, so the file is downloaded to the cache first,
 * with the connection's credential, and drawn from there.
 */
export function ArtifactPdf({ config, sessionId, artifact }: { config: ServerConfig; sessionId: string; artifact: Pick<SessionArtifact, "path" | "updatedAt"> }) {
	const t = useTheme();
	const styles = useThemedStyles(makeStyles);
	const glassInset = useGlassBarInset();
	const key = `${sessionId}|${artifact.path}|${artifact.updatedAt}`;
	const [state, setState] = useState<PdfState>();
	const [attempt, setAttempt] = useState(0);
	useEffect(() => {
		let live = true;
		const source = artifactStreamSource(config, sessionId, artifact.path, artifact.updatedAt);
		if (!source) {
			setState({ key, error: "Its path isn't one the phone can request." });
			return;
		}
		downloadArtifact(source, artifactCacheEntry(sessionId, artifact.path, artifact.updatedAt)).then(
			(uri) => { if (live) setState({ key, uri }); },
			(cause) => { if (live) setState({ key, error: userFacingError(cause, "The PDF didn't download.") }); },
		);
		return () => { live = false; };
	}, [artifact.path, artifact.updatedAt, attempt, config, key, sessionId]);
	const retry = <Button title="Try again" icon="refresh-cw" onPress={() => { setState(undefined); setAttempt((n) => n + 1); }} />;
	if (state?.key === key && state.error) return <View style={styles.center}><EmptyState icon="file" title="Couldn't load this PDF" message={state.error} action={retry} /></View>;
	if (state?.key !== key || !state.uri) return <View style={styles.center}><ActivityIndicator color={t.accent} /></View>;
	return (
		<View style={[styles.screen, { paddingTop: glassInset }]}>
			<PdfRendererView
				source={state.uri}
				distanceBetweenPages={8}
				maxZoom={5}
				onError={() => setState({ key, error: "The phone couldn't read this file as a PDF." })}
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
