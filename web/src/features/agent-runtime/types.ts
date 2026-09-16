export type AgentEngineID = 'native' | 'codex'
export interface CodexRuntimeSettings { model: string; effort?: string }
export interface RuntimePreferences { selected?: AgentEngineID; codex?: CodexRuntimeSettings }
export type RuntimeSelection = { kind: 'native' } | { kind: 'codex'; codex: CodexRuntimeSettings }
export interface EngineCapabilities {
  ask_user: boolean
  cancel: boolean
  interactive_approval: boolean
  delegation: boolean
  goal: boolean
  queue: boolean
  steer: boolean
  pause: boolean
}
export type ConfigurationSectionID = 'shared.instructions' | 'shared.skills' | 'shared.context_sources' | 'shared.input_budget'
  | 'native.model' | 'native.permissions' | 'native.context_policy' | 'native.checkpoint' | 'native.subagents'
  | 'codex.model' | 'codex.execution_policy'
export interface ConfigurationSection {
  id: ConfigurationSectionID
  owner: 'shared' | AgentEngineID
  state: 'editable' | 'read_only' | 'inactive' | 'unavailable'
  reason_key?: string
}
export interface AgentConfiguration { selected: AgentEngineID; sections: ConfigurationSection[]; tool_manifest?: import('@/features/settings/types').ResolvedAgentToolCapability[] }
export interface EngineDescriptor {
  id: AgentEngineID
  name_key: string
  status: 'unchecked' | 'not_installed' | 'auth_required' | 'ready' | 'unavailable' | 'incompatible'
  reason_key?: string
  capabilities: EngineCapabilities
  configuration_sections: ConfigurationSection[]
}
export interface EngineModel { id: string; display_name: string; efforts: string[]; default_effort?: string }
export interface EngineModels { items: EngineModel[]; default_id?: string }

/** Selectors inherit independently; model/effort is one atomic engine branch. */
export function resolveRuntimePreferences(parent?: RuntimePreferences, own?: RuntimePreferences): RuntimePreferences {
  return { selected: own?.selected ?? parent?.selected ?? 'native', codex: own?.codex ?? parent?.codex }
}
