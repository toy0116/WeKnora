-- Migration: 000043_kb_vendor
-- Description: Adds `vendor` to knowledge_bases. When non-empty, the document
--              summary pipeline prefixes each KB's per-doc summary with this
--              vendor name, so downstream LLM consumers reading a summary
--              chunk for "EG71" cannot mistake a Milesight datasheet that
--              landed in a competitor-KB for one of our own products.
--
-- Backfill is intentionally not done here. Each deployment knows its own KB
-- naming conventions; operators run a tenant-specific UPDATE after applying
-- this migration. See README / runbook for the recommended pattern, and the
-- accompanying tenant-data migration in this repo's tools/ directory for the
-- LocalHub default mapping.

DO $$ BEGIN RAISE NOTICE '[Migration 000043] Adding vendor column to knowledge_bases...'; END $$;

ALTER TABLE knowledge_bases
    ADD COLUMN IF NOT EXISTS vendor VARCHAR(64) NOT NULL DEFAULT '';

-- An index isn't strictly required (vendor is a per-doc context-injection
-- input, not a query filter) but a small one lets future "list KBs owned by
-- vendor X" / multi-tenant audit queries hit the index instead of seq-scan.
CREATE INDEX IF NOT EXISTS idx_knowledge_bases_vendor
    ON knowledge_bases (vendor)
    WHERE vendor <> '';

DO $$ BEGIN RAISE NOTICE '[Migration 000043] Done.'; END $$;
