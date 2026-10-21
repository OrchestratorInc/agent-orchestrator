import type { Metadata } from "next";
import { NotFound } from "@/components/not-found";

export const metadata: Metadata = {
  title: "Page not found | Agent Orchestrator Docs",
  robots: { index: false },
};

export default function NotFoundPage() {
  return <NotFound />;
}
