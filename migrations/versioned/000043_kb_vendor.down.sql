-- Roll back 000043_kb_vendor: drop the index first (it's WHERE-conditional so
-- a plain DROP INDEX IF EXISTS handles the case where the migration never
-- ran), then drop the column. Idempotent.

DROP INDEX IF EXISTS idx_knowledge_bases_vendor;
ALTER TABLE knowledge_bases DROP COLUMN IF EXISTS vendor;
