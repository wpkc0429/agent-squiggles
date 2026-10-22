export type Style = "short" | "long";

/** Formats a duration, e.g. formatDuration(125) === "2:05". */
export function formatDuration(seconds: number, style: Style = "short"): string {
  const m = Math.floor(seconds / 60);
  const s = Math.floor(seconds % 60);
  return style === "long" ? `${m} min ${s} s` : `${m}:${String(s).padStart(2, "0")}`;
}
