import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { Button } from "./ui/button";

type Props = { children: React.ReactNode };
type State = { hasError: boolean };

/**
 * The in-panel crash state: a short message and a full reload. With `reset`,
 * it first offers to re-render just the panel, which keeps the rest of the app
 * (and its terminals) running. A failed chunk import cannot recover that way.
 */
export function LazyChunkErrorPanel({ reset }: { reset?: () => void }) {
	const { t } = useTranslation();
	return (
		<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" role="alert">
			<p className="text-sm text-muted-foreground">{t("appError.title")}</p>
			<div className="flex items-center gap-2">
				{reset ? (
					<Button size="sm" variant="secondary" onClick={reset}>
						{t("appError.tryAgain")}
					</Button>
				) : null}
				<Button size="sm" variant={reset ? "ghost" : "secondary"} onClick={() => window.location.reload()}>
					{t("titlebar.reload")}
				</Button>
			</div>
		</div>
	);
}

class LazyChunkErrorBoundary extends React.Component<Props, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError(): State {
		return { hasError: true };
	}

	componentDidCatch(error: Error) {
		void captureRendererException(error, { source: "react-error-boundary", operation: "lazy_chunk" });
	}

	render() {
		if (!this.state.hasError) return this.props.children;
		return <LazyChunkErrorPanel />;
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
