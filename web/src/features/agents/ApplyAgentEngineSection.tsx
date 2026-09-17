import { useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Button } from '@/components/ui/button'
import { useConversationConfig } from '@/features/conversation-config/use-conversation-config'
import type { ConversationConfigBinding } from '@/features/conversation-config/types'
import { runtimeSelection, runtimeModel, type RuntimePreferences } from '@/features/agent-runtime/types'

/** The conversation link supplies the exact target; ordinary Agents visits only edit defaults. */
export function ApplyAgentEngineSection({ binding, agentKind, customAgentId, runtime, saveDefaults }: {
  binding: ConversationConfigBinding
  agentKind: 'ide' | 'general'
  customAgentId?: string
  runtime: RuntimePreferences
  saveDefaults: () => Promise<unknown>
}) {
  const { t } = useTranslation()
  const [message, setMessage] = useState('')
  const [applying, setApplying] = useState(false)
  const conversation = useConversationConfig(binding)
  const snapshot = conversation.snapshot
  const matches = Boolean(snapshot?.revision && snapshot.agent_kind === agentKind && (snapshot.custom_agent_id ?? '') === (customAgentId ?? ''))
  const selection = runtimeSelection(runtime)

  const apply = async () => {
    if (!selection || !matches) return
    setApplying(true); setMessage('')
    try {
      await saveDefaults()
      if (await conversation.patch({ runtime: selection })) setMessage(t('agentRuntime.applied'))
    } catch { setMessage(t('agentRuntime.saveBeforeApply')) }
    finally { setApplying(false) }
  }

  return <section className="flex flex-col gap-3 border-b border-border pb-5" aria-label={t('agentRuntime.applyTitle')}>
    <h3 className="font-medium">{t('agentRuntime.applyTitle')}</h3>
    <p className="break-all text-muted-foreground">{t('agentRuntime.conversation')}: {binding.session_id}</p>
    {snapshot && <p className="text-muted-foreground">{t('agentRuntime.applyPreview', { from: t(`agentRuntime.${snapshot.runtime?.kind ?? 'native'}`), to: t(`agentRuntime.${runtime.selected ?? 'native'}`), model: runtimeModel(selection ?? undefined)?.profile_id ?? runtimeModel(selection ?? undefined)?.model ?? t('agentRuntime.nativeModelRetained') })}</p>}
    <div><Button size="sm" variant="outline" disabled={applying || conversation.loading || conversation.saving || !matches || !selection} onClick={() => void apply()}>{t('agentRuntime.apply')}</Button></div>
    {conversation.error && <p role="alert" className="text-destructive">{conversation.error}</p>}
    {message && <p role="status">{message}</p>}
  </section>
}
