import { createFileRoute } from "@tanstack/react-router";
import { UsageView } from "../components/usage/UsageView";

export const Route = createFileRoute("/_shell/usage")({ component: UsageView });
