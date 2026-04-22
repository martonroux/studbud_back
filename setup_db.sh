#!/usr/bin/env bash
set -euo pipefail

DB_MAIN=${DB_MAIN:-studbud}
DB_TEST=${DB_TEST:-studbud_test}
DB_USER=${DB_USER:-postgres}

create_if_missing() {
    local db=$1
    if ! psql -U "$DB_USER" -lqt | cut -d \| -f 1 | grep -qw "$db"; then
        echo "Creating database: $db"
        createdb -U "$DB_USER" "$db"
    else
        echo "Database exists: $db"
    fi
}

create_if_missing "$DB_MAIN"
create_if_missing "$DB_TEST"

echo "Done."
