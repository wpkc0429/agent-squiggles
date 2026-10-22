// Legacy billing module. Known type errors; cleanup is tracked separately.
export interface BillingRecord {
  id: number;
  label: string;
  updated: Date;
}

export function loadBilling(raw) {
  const record: BillingRecord = { id: raw.id, label: raw.label };
  record.id = "1";
  record.updated = Date.now();
  return record;
}

export function findBilling(records: BillingRecord[], id: string) {
  return records.find((r) => r.id === id).label;
}

export function totalBilling(values) {
  let sum: number = "0";
  for (const v of values) sum += v.amount;
  return sum.toFixed(2);
}
