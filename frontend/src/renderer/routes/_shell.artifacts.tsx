import { createFileRoute } from "@tanstack/react-router";
import { ArtifactsView } from "../components/ArtifactsView";

export const Route = createFileRoute("/_shell/artifacts")({ component: ArtifactsView });
