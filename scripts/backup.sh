#!/usr/bin/env sh
# Dump the meetings database to a timestamped file.
#
#   ./scripts/backup.sh [output-dir]     (default: ./backups)
#
# Meeting notes are the only thing in this system that cannot be regenerated:
# audio is deleted after transcription by design, so a lost row is lost for
# good. Run this before upgrades, and on a schedule if the notes matter.
set -eu

OUT_DIR="${1:-./backups}"
STAMP=$(date +%Y%m%d-%H%M%S)
FILE="$OUT_DIR/notulen-$STAMP.sql.gz"

mkdir -p "$OUT_DIR"

# --clean --if-exists so the dump can be replayed onto a populated database.
docker compose exec -T postgres pg_dump \
    -U "${POSTGRES_USER:-postgres}" \
    -d "${POSTGRES_DB:-meeting_notes}" \
    --clean --if-exists \
  | gzip > "$FILE"

SIZE=$(wc -c < "$FILE")
if [ "$SIZE" -lt 1000 ]; then
    echo "backup looks empty ($SIZE bytes) — is the database running?" >&2
    exit 1
fi

MEETINGS=$(docker compose exec -T postgres psql -U "${POSTGRES_USER:-postgres}" \
    -d "${POSTGRES_DB:-meeting_notes}" -t -A -c "SELECT count(*) FROM meetings;" 2>/dev/null || echo '?')

echo "$FILE"
echo "  $SIZE bytes, $MEETINGS meetings"
