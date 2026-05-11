import { get, put } from '@/utils/request'

export interface EntityAliasGroup {
  forms: string[]
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
