import React, { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { bindRendererErrorHandlers, captureRendererException } from "../lib/telemetry";

type Props = {
	children: React.ReactNode;
};

type BoundaryProps = Props & {
	fallbackBody: string;
	fallbackTitle: string;
};

type State = {
	hasError: boolean;
};

class TelemetryErrorBoundary extends React.Component<BoundaryProps, State> {
	state: State = { hasError: false };

	static getDerivedStateFromError() {
		return { hasError: true };
	}

	componentDidCatch(error: Error, info: React.ErrorInfo) {
		void captureRendererException(error, {
			source: "react-error-boundary",
			operation: "react_render",
		});
		void info;
	}

	render() {
		if (this.state.hasError) {
			return (
				<div className="flex h-screen items-center justify-center bg-background px-6 text-center text-foreground">
					<div>
						<h1 className="text-heading-sm font-semibold">{this.props.fallbackTitle}</h1>
						<p className="mt-2 text-sm text-muted-foreground">{this.props.fallbackBody}</p>
					</div>
				</div>
			);
		}
		return this.props.children;
	}
}

export function TelemetryBoundary({ children }: Props) {
	const { t } = useTranslation();
	useEffect(() => { bindRendererErrorHandlers(); }, []);
	return (
		<TelemetryErrorBoundary fallbackBody={t("appError.body")} fallbackTitle={t("appError.title")}>
			{children}
		</TelemetryErrorBoundary>
	);
}
