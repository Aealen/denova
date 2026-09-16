import { describe, expect, it } from 'vitest'
import { resolveRuntimePreferences, type RuntimePreferences } from './types'

describe('runtime configuration inheritance', () => {
  it('replaces a complete model branch independently of the selected engine', () => {
    const parent: RuntimePreferences = { selected: 'codex', codex: { model: 'parent-model', effort: 'high' } }
    const draft: RuntimePreferences = { selected: 'native', codex: { model: 'child-model' } }
    expect(resolveRuntimePreferences(parent, draft)).toEqual(draft)
    delete draft.selected
    expect(resolveRuntimePreferences(parent, draft)).toEqual({ selected: 'codex', codex: { model: 'child-model' } })
    delete draft.codex
    expect(resolveRuntimePreferences(parent, draft)).toEqual(parent)
    expect(parent.codex?.effort).toBe('high')
  })
})
