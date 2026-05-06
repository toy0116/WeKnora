#!/usr/bin/env bash
# backup.sh — WeKnora data backup and cross-machine migration helper
#
# EXPORT (run on source machine):
#   ./scripts/backup.sh export [--out DIR] [--host URL]
#
# IMPORT (run on target machine, WeKnora stopped):
#   DB_HOST=... DB_USER=... DB_NAME=... DB_PASSWORD=... \
#   LOCAL_STORAGE_BASE_DIR=... \
#   ./scripts/backup.sh import \
#       --db    weknora-db-YYYYMMDD-HHmmss.sql.gz \
#       --files weknora-files-YYYYMMDD-HHmmss.tar.gz
#
# Environment variables read by import:
#   DB_DRIVER    postgres (default) | sqlite
#   DB_HOST, DB_PORT, DB_USER, DB_PASSWORD, DB_NAME  — PostgreSQL target
#   DB_PATH                                          — SQLite path
#   LOCAL_STORAGE_BASE_DIR                           — file storage root

set -euo pipefail

HOST="${WEKNORA_HOST:-http://127.0.0.1:8080}"
OUT_DIR="."

usage() {
  grep '^#' "$0" | sed 's/^# \?//'
  exit 1
}

# ── export ────────────────────────────────────────────────────────────────────

do_export() {
  while [[ $# -gt 0 ]]; do
    case "$1" in
      --out)  OUT_DIR="$2";  shift 2 ;;
      --host) HOST="$2";     shift 2 ;;
      *) echo "Unknown flag: $1"; usage ;;
    esac
  done

  mkdir -p "$OUT_DIR"
  TS=$(date -u +%Y%m%d-%H%M%S)

  echo "==> [1/3] Exporting database ..."
  DB_FILE="${OUT_DIR}/weknora-db-${TS}.sql.gz"
  if ! curl -fSL --max-time 600 -o "$DB_FILE" "${HOST}/admin/backup/database"; then
    echo "ERROR: database export failed (is WeKnora running at ${HOST}?)"
    exit 1
  fi
  echo "     saved: $DB_FILE ($(du -sh "$DB_FILE" | cut -f1))"

  echo "==> [2/3] Exporting files ..."
  FILES_FILE="${OUT_DIR}/weknora-files-${TS}.tar.gz"
  if ! curl -fSL --max-time 600 -o "$FILES_FILE" "${HOST}/admin/backup/files"; then
    echo "ERROR: files export failed"
    exit 1
  fi
  echo "     saved: $FILES_FILE ($(du -sh "$FILES_FILE" | cut -f1))"

  echo "==> [3/3] Exporting config ..."
  CFG_FILE="${OUT_DIR}/weknora-config-${TS}.json"
  if ! curl -fSL --max-time 30 -o "$CFG_FILE" "${HOST}/admin/backup/config"; then
    echo "ERROR: config export failed"
    exit 1
  fi
  echo "     saved: $CFG_FILE"

  echo ""
  echo "Export complete.  Transfer these files to the target machine:"
  ls -lh "${OUT_DIR}/weknora-"*"-${TS}."*
  echo ""
  echo "Then run on the target:"
  echo "  DB_HOST=<host> DB_USER=<user> DB_PASSWORD=<pass> DB_NAME=<db> \\"
  echo "  LOCAL_STORAGE_BASE_DIR=<path> \\"
  echo "  ./scripts/backup.sh import --db $DB_FILE --files $FILES_FILE"
}

# ── import ────────────────────────────────────────────────────────────────────

do_import() {
  DB_FILE=""
  FILES_ARCHIVE=""

  while [[ $# -gt 0 ]]; do
    case "$1" in
      --db)    DB_FILE="$2";       shift 2 ;;
      --files) FILES_ARCHIVE="$2"; shift 2 ;;
      *) echo "Unknown flag: $1"; usage ;;
    esac
  done

  if [[ -z "$DB_FILE" && -z "$FILES_ARCHIVE" ]]; then
    echo "Error: specify at least --db or --files"; usage
  fi

  DB_DRIVER="${DB_DRIVER:-postgres}"

  # ── Database restore ───────────────────────────────────────────────────────
  if [[ -n "$DB_FILE" ]]; then
    echo "==> Restoring database from ${DB_FILE} ..."
    if [[ "$DB_DRIVER" == "postgres" ]]; then
      : "${DB_HOST:?DB_HOST must be set}"
      DB_PORT="${DB_PORT:-5432}"
      : "${DB_USER:?DB_USER must be set}"
      : "${DB_NAME:?DB_NAME must be set}"
      export PGPASSWORD="${DB_PASSWORD:-}"

      echo "     Dropping existing connections ..."
      psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
        -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='${DB_NAME}' AND pid<>pg_backend_pid();" \
        2>/dev/null || true

      echo "     Re-creating database ${DB_NAME} ..."
      psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
        -c "DROP DATABASE IF EXISTS \"${DB_NAME}\";" 2>/dev/null || true
      psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d postgres \
        -c "CREATE DATABASE \"${DB_NAME}\";"

      echo "     Restoring data (this may take a few minutes) ..."
      gunzip -c "$DB_FILE" | psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME"
      echo "     Database restored."

    elif [[ "$DB_DRIVER" == "sqlite" ]]; then
      DB_PATH="${DB_PATH:-./data/weknora.db}"
      mkdir -p "$(dirname "$DB_PATH")"
      cp "$DB_FILE" "$DB_PATH"
      echo "     SQLite database restored → ${DB_PATH}"
    else
      echo "Error: unsupported DB_DRIVER=${DB_DRIVER}"; exit 1
    fi
  fi

  # ── Files restore ──────────────────────────────────────────────────────────
  if [[ -n "$FILES_ARCHIVE" ]]; then
    STORAGE_DIR="${LOCAL_STORAGE_BASE_DIR:-}"
    if [[ -z "$STORAGE_DIR" ]]; then
      echo "Error: set LOCAL_STORAGE_BASE_DIR before restoring files"; exit 1
    fi
    echo "==> Restoring files to ${STORAGE_DIR} ..."
    mkdir -p "$STORAGE_DIR"
    tar -xzf "$FILES_ARCHIVE" -C "$STORAGE_DIR"
    echo "     Files restored."
  fi

  echo ""
  echo "Import complete."
  echo "Update config.yaml / .env for the new machine and start WeKnora."
}

# ── dispatch ──────────────────────────────────────────────────────────────────

CMD="${1:-}"
shift || true

case "$CMD" in
  export) do_export "$@" ;;
  import) do_import "$@" ;;
  *) usage ;;
esac
