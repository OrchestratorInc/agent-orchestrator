import { createFileRoute } from "@tanstack/react-router";
import { TeamFeedView } from "../components/TeamFeedView";

export const Route = createFileRoute("/_shell/team")({ component: TeamFeedView });
