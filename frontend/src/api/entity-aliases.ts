import { get, put } from '@/utils/request'

/**
 * One alias group in the entity dictionary.
 *
 * - `forms` — cross-lingual / cross-spelling aliases for the SAME entity
 *   (e.g. "鲁邦通" ↔ "Robustel"). Forms participate in query expansion so
 *   BM25/vector retrieval finds documents regardless of which form the user
 *   typed.
 * - `products` — model numbers / SKUs that belong to this entity (e.g.
 *   "EG5120" belongs to Robustel). Products do NOT enter query expansion
 *   (would corrupt retrieval); they only feed the entity-mismatch tagger and
 *   the brand→product attribution-conflict warning surfaced to the LLM.
 *
 * A group may have forms only, products only, or both. The save endpoint
 * preserves whichever fields are present (it no longer silently drops
 * products on save — see internal/handler/entity_aliases.go).
 */
export interface EntityAliasGroup {
  forms: string[]
  products?: string[]
}

export interface EntityAliasConfig {
  groups: EntityAliasGroup[]
}

export function getEntityAliases() {
  return get('/api/v1/system/entity-aliases')
}

export function updateEntityAliases(config: EntityAliasConfig) {
  return put('/api/v1/system/entity-aliases', config)
}
