import { createFileRoute } from "@tanstack/react-router";
import { SessionSearchView } from "../components/SessionSearchView";

export const Route = createFileRoute("/_shell/search")({
	component: SessionSearchView,
});