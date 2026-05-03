#!/usr/bin/env bash
set -euo pipefail

DB_PATH="${DB_PATH:-/opt/ai-gateway/metering/usage.sqlite}"
BACKUP_DIR="${BACKUP_DIR:-/opt/ai-gateway/metering/backups}"
RETENTION_DAYS="${RETENTION_DAYS:-90}"
MONTHLY_KEEP="${MONTHLY_KEEP:-12}"

umask 077

TODAY="$(date +%Y-%m-%d)"
DAY_OF_MONTH="$(date +%d)"
BACKUP_FILE="${BACKUP_DIR}/usage-${TODAY}.sqlite.gz"
TMP_DB="$(mktemp "${TMPDIR:-/tmp}/usage-backup.XXXXXX.sqlite")"
TMP_GZ="${TMP_DB}.gz"

cleanup() {
    rm -f "$TMP_DB" "$TMP_GZ"
}
trap cleanup EXIT

mkdir -p "$BACKUP_DIR"

if [[ ! -f "$DB_PATH" ]]; then
    echo "Database not found: $DB_PATH" >&2
    exit 1
fi

# Use SQLite's backup API to get a consistent copy while WAL mode is active.
sqlite3 "$DB_PATH" ".backup \"$TMP_DB\""

integrity="$(sqlite3 "$TMP_DB" "PRAGMA integrity_check")"
if [[ "$integrity" != "ok" ]]; then
    echo "Backup integrity check failed: $integrity" >&2
    exit 1
fi

gzip -c "$TMP_DB" > "$TMP_GZ"
mv -f "$TMP_GZ" "$BACKUP_FILE"

echo "Backup created: $BACKUP_FILE"

if [[ "$DAY_OF_MONTH" == "01" ]]; then
    MONTHLY_FILE="${BACKUP_DIR}/usage-monthly-$(date +%Y-%m).sqlite.gz"
    cp -f "$BACKUP_FILE" "$MONTHLY_FILE"
    echo "Monthly backup created: $MONTHLY_FILE"
fi

# Delete daily backups older than the retention window. Monthly backups use a
# separate naming scheme and are rotated by count below.
find "$BACKUP_DIR" -type f -name "usage-????-??-??.sqlite.gz" -mtime "+${RETENTION_DAYS}" -delete

mapfile -t expired_monthlies < <(
    find "$BACKUP_DIR" -type f -name "usage-monthly-*.sqlite.gz" -printf '%T@ %p\n' |
        sort -rn |
        tail -n "+$((MONTHLY_KEEP + 1))" |
        cut -d' ' -f2-
)

for f in "${expired_monthlies[@]}"; do
    echo "Removing expired monthly backup: $f"
    rm -f "$f"
done

echo "Backup rotation complete. Current backups:"
ls -lh "$BACKUP_DIR/"
