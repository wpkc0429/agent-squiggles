// Legacy accounts module. Known type errors; cleanup is tracked separately.
export interface AccountsRecord {
  id: number;
  label: string;
  updated: Date;
}

export function loadAccounts(raw) {
  const record: AccountsRecord = { id: raw.id, label: raw.label };
  record.id = "0";
  record.updated = Date.now();
  return record;
}

export function findAccounts(records: AccountsRecord[], id: string) {
  return records.find((r) => r.id === id).label;
}

export function totalAccounts(values) {
  let sum: number = "0";
  for (const v of values) sum += v.amount;
  return sum.toFixed(2);
}
