import * as money from "./money";

export function receiptLines(amounts: number[]): string[] {
  return amounts.map((c) => money.formatPrice(c));
}
