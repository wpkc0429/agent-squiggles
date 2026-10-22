import * as time from "../time";
import type { Track } from "../player";

export function toCsv(tracks: Track[]): string {
  return tracks.map((t) => `${t.title},${time.formatDuration(t.seconds)}`).join("\n");
}
