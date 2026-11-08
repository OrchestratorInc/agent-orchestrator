import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { Button } from "./ui/button";

type Props = { children: React.ReactNode };
type State = { hasError: boolean; error?: unknown };

/**
 * The in-panel crash state: a short message and a full reload. With `reset`,
 * it first offers to re-render just the panel, which keeps the rest of the app
 * (and its terminals) running. A failed chunk import cannot recover that way.
 */
export function LazyChunkErrorPanel({ reset, error }: { reset?: () => void; error?: unknown }) {
	const { t } = useTranslation();
	const message = error instanceof Error ? error.message : typeof error === "string" ? error : "";
	return (
		<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" role="alert">
			<p className="text-sm text-muted-foreground">{t("appError.title")}</p>
			<p className="max-w-md text-sm text-muted-foreground">{t(reset ? "appError.panelRecovery" : "appError.windowRecovery")}</p>
			{message ? (
				<details className="max-w-md text-left text-xs text-muted-foreground">
					<summary className="cursor-pointer">{t("appError.details")}</summary>
					<pre className="mt-2 max-h-40 select-text overflow-auto whitespace-pre-wrap break-words">{message}</pre>
				</details>
			) : null}
			<div className="flex items-center gap-2">
				{reset ? (
					<Button size="sm" variant="secondary" onClick={reset}>
						{t("appError.reloadView")}
					</Button>
				) : null}
				<Button size="sm" variant={reset ? "ghost" : "secondary"} onClick={() => window.location.reload()}>
					{t("appError.reloadWindow")}
				</Button>
			</div>
		</div>
	);
}

class LazyChunkErrorBoundary extends React.Component<Props, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError(error: unknown): State {
		return { hasError: true, error };
	}

	componentDidCatch(error: Error) {
		void captureRendererException(error, { source: "react-error-boundary", operation: "lazy_chunk" });
	}

	render() {
		if (!this.state.hasError) return this.props.children;
		return <LazyChunkErrorPanel error={this.state.error} />;
	}
}

/**
 * Contains a failed lazy chunk (or a render error inside it) to its own panel.
 * Without it, a rejected import propagates to the route's error screen and takes
 * the whole shell, including every retained terminal, down with it. React.lazy
 * caches the rejection, so recovery is a reload.
 */
export function LazyChunkBoundary({ children }: Props) {
	return <LazyChunkErrorBoundary>{children}</LazyChunkErrorBoundary>;
}
