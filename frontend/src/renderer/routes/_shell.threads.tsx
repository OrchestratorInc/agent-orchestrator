import { createFileRoute } from "@tanstack/react-router";
import { ThreadsView } from "../components/ThreadsView";

export const Route = createFileRoute("/_shell/threads")({ component: ThreadsView });
