import React from "react";
import { useTranslation } from "react-i18next";
import { captureRendererException } from "../lib/telemetry";
import { Button } from "./ui/button";

type Props = { children: React.ReactNode };
type State = { hasError: boolean };

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

export function LazyChunkBoundary({ children }: Props) {
	return <LazyChunkErrorBoundary>{children}</LazyChunkErrorBoundary>;
}
