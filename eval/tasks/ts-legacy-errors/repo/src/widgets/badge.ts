import { dur } from "../index";

export function badge(seconds: number): string {
  return `[${dur(seconds)}]`;
}
