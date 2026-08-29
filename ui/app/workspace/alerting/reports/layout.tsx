import { createFileRoute } from "@tanstack/react-router";
import DailyReportsPage from "./page";
export const Route = createFileRoute("/workspace/alerting/reports")({ component: DailyReportsPage });