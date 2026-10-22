import { formatDuration } from "./time";

export interface Track {
  title: string;
  seconds: number;
}

export function nowPlaying(track: Track, position: number): string {
  return `${track.title} ${formatDuration(position)} / ${formatDuration(track.seconds)}`;
}
