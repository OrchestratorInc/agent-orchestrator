import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { Button } from "./ui/button";

type Props = { children: React.ReactNode };
type BoundaryProps = Props & { title: string; reloadLabel: string };
type State = { hasError: boolean };

class LazyChunkErrorBoundary extends React.Component<BoundaryProps, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError(): State {
		return { hasError: true };
	}

	componentDidCatch(error: Error) {
		void captureRendererException(error, { source: "react-error-boundary", operation: "lazy_chunk" });
	}

	render() {
		if (!this.state.hasError) return this.props.children;
		return (
			<div className="flex h-full min-h-0 flex-col items-center justify-center gap-3 px-6 text-center" role="alert">
				<p className="text-sm text-muted-foreground">{this.props.title}</p>
				<Button size="sm" variant="secondary" onClick={() => window.location.reload()}>
					{this.props.reloadLabel}
				</Button>
			</div>
		);
	}
}

/**
 * Contains a failed lazy chunk (or a render error inside it) to its own panel.
 * Without it, a rejected import propagates to the route's error screen and takes
 * the whole shell, including every retained terminal, down with it. React.lazy
 * caches the rejection, so recovery is a reload.
 */
export function LazyChunkBoundary({ children }: Props) {
	const { t } = useTranslation();
	return (
		<LazyChunkErrorBoundary reloadLabel={t("titlebar.reload")} title={t("appError.title")}>
			{children}
		</LazyChunkErrorBoundary>
	);
}
