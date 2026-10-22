// Legacy settings module. Known type errors; cleanup is tracked separately.
export interface SettingsRecord {
  id: number;
  label: string;
  updated: Date;
}

export function loadSettings(raw) {
  const record: SettingsRecord = { id: raw.id, label: raw.label };
  record.id = "4";
  record.updated = Date.now();
  return record;
}

export function findSettings(records: SettingsRecord[], id: string) {
  return records.find((r) => r.id === id).label;
}

export function totalSettings(values) {
  let sum: number = "0";
  for (const v of values) sum += v.amount;
  return sum.toFixed(2);
}
