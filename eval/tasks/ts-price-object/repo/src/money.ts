/** Formats an amount in cents, e.g. formatPrice(1234) === "$12.34". */
export function formatPrice(cents: number, currency: string = "USD"): string {
  const symbol = currency === "USD" ? "$" : currency + " ";
  return `${symbol}${(cents / 100).toFixed(2)}`;
}
