import { formatDuration } from "../time";
import type { Track } from "../player";

export function listeningTime(tracks: Track[]): string {
  const total = tracks.reduce((sum, t) => sum + t.seconds, 0);
  return formatDuration(total, "long");
}
