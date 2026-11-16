/// <reference path="../../src/renderer/global.d.ts" />
// Browser fixture for native decoding/playback through the real artifact viewer.
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nextProvider } from "react-i18next";
import { appI18n } from "../../src/renderer/i18n";
import { ArtifactFileView } from "../../src/renderer/components/ArtifactFileView";
import { TooltipProvider } from "../../src/renderer/components/ui/tooltip";
import "../../src/renderer/styles.css";

const samples = {
	image: { path: "logo.png", url: "/src/renderer/assets/ao-logo.png" },
	video: { path: "clip.webm", url: "/e2e/media-preview/clip.webm" },
	audio: { path: "notification.mp3", url: "/src/renderer/assets/notification.mp3" },
};
const type = new URLSearchParams(location.search).get("type");
const sample = type === "video" || type === "audio" ? samples[type] : samples.image;
const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });

createRoot(document.getElementById("media-root")!).render(
	<QueryClientProvider client={client}>
		<I18nextProvider i18n={appI18n}>
			<TooltipProvider>
				<main className="flex h-dvh flex-col bg-background text-foreground">
					<nav className="flex gap-4 border-b border-border p-3 text-sm">
						<a href="?type=image">Image</a>
						<a href="?type=video">Video</a>
						<a href="?type=audio">Audio</a>
					</nav>
					<div className="min-h-0 flex-1">
						<ArtifactFileView artifactName={sample.path} path={sample.path} rawUrl={new URL(sample.url, location.origin).href} sessionId="media-fixture" />
					</div>
				</main>
			</TooltipProvider>
		</I18nextProvider>
	</QueryClientProvider>,
);
