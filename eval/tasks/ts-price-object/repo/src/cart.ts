import { formatPrice } from "./money";

export interface Line {
  name: string;
  cents: number;
  qty: number;
}

export function cartTotal(lines: Line[]): string {
  const cents = lines.reduce((sum, l) => sum + l.cents * l.qty, 0);
  return formatPrice(cents);
}
