#!/usr/bin/env sh
# Restore the meetings database from a backup.
#
#   ./scripts/restore.sh backups/notulen-20260911-120000.sql.gz
#   ./scripts/restore.sh -y <file>     skip the confirmation
#
# This REPLACES the current contents. It asks first, because the thing it
# overwrites is exactly the thing that cannot be regenerated.
set -eu

ASSUME_YES=0
if [ "${1:-}" = "-y" ] || [ "${1:-}" = "--yes" ]; then
    ASSUME_YES=1
    shift
fi

FILE="${1:-}"
if [ -z "$FILE" ] || [ ! -f "$FILE" ]; then
    echo "usage: $0 [-y] <backup-file.sql.gz>" >&2
    exit 1
fi

PGUSER_="${POSTGRES_USER:-postgres}"
PGDB_="${POSTGRES_DB:-meeting_notes}"

# < /dev/null on every helper call: `docker compose exec -T` forwards stdin to
# the container, so without this it swallows the confirmation the user types
# (and, when piped, the dump itself).
count_meetings() {
    docker compose exec -T postgres psql -U "$PGUSER_" -d "$PGDB_" \
        -t -A -c "SELECT count(*) FROM meetings;" < /dev/null 2>/dev/null || echo '?'
}

CURRENT=$(count_meetings)

echo "Restoring:  $FILE"
echo "This replaces the current database, which holds $CURRENT meetings."

if [ "$ASSUME_YES" -eq 0 ]; then
    printf 'Type "yes" to continue: '
    # Read from the terminal, not stdin, so the prompt survives a pipe.
    if [ -r /dev/tty ]; then
        read -r CONFIRM < /dev/tty
    else
        read -r CONFIRM
    fi
    [ "$CONFIRM" = "yes" ] || { echo "aborted"; exit 1; }
fi

gunzip -c "$FILE" | docker compose exec -T postgres psql \
    -U "$PGUSER_" -d "$PGDB_" --set ON_ERROR_STOP=on --quiet

echo "restored: $(count_meetings) meetings"
