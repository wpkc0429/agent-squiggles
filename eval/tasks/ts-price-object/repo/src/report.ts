import { fmt } from "./index";

export function summary(totals: Record<string, number>): string {
  return Object.entries(totals)
    .map(([day, cents]) => `${day}: ${fmt(cents)}`)
    .join("\n");
}
