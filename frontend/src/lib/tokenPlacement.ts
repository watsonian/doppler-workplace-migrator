import type { TokenRecord } from '@/api/client';

/**
 * Where a token lives, as a person reads it. A service account's slug is a
 * UUID — and a different one on each instance — so its name stands in for it,
 * with the slug only as a fallback for a ledger written before names were kept.
 */
export function tokenPlacement(record: TokenRecord, separator = '/'): string {
  if (record.kind === 'service_account_token') {
    return record.service_account_name || record.service_account || '';
  }
  return `${record.project}${separator}${record.config}`;
}
