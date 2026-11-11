import { useEffect } from "react";
import { isNotFound, isRedirect, type ErrorComponentProps } from "@tanstack/react-router";
import { captureRendererException } from "../lib/telemetry";

// Session hosts keep ownership of the visible error panel.
export function reportRouteError(error: unknown): void {
	if (!isNotFound(error) && !isRedirect(error)) void captureRendererException(error, { operation: "route", source: "route-error" });
}

export function RouteErrorCapture({ error }: ErrorComponentProps) {
	useEffect(() => { reportRouteError(error); }, [error]);
	return null;
}
