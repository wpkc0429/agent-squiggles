// Legacy importer module. Known type errors; cleanup is tracked separately.
export interface ImporterRecord {
  id: number;
  label: string;
  updated: Date;
}

export function loadImporter(raw) {
  const record: ImporterRecord = { id: raw.id, label: raw.label };
  record.id = "2";
  record.updated = Date.now();
  return record;
}

export function findImporter(records: ImporterRecord[], id: string) {
  return records.find((r) => r.id === id).label;
}

export function totalImporter(values) {
  let sum: number = "0";
  for (const v of values) sum += v.amount;
  return sum.toFixed(2);
}
