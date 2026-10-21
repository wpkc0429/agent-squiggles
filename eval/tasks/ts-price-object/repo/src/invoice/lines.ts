import { formatPrice } from "../money";
import type { Line } from "../cart";

export function invoiceLines(lines: Line[], currency: string): string[] {
  return lines.map((l) => `${l.qty} x ${l.name} @ ${formatPrice(l.cents, currency)}`);
}
