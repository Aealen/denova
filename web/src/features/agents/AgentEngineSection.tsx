import { useEffect, useState } from 'react'
import { useTranslation } from 'react-i18next'
import { Check, Cpu } from 'lucide-react'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { checkAgentEngine, fetchAgentEngines, fetchEngineModels } from '@/features/agent-runtime/api'
import { resolveRuntimePreferences, type AgentEngineID, type EngineDescriptor, type EngineModels, type RuntimePreferences } from '@/features/agent-runtime/types'
import { Field } from './agent-form-controls'
import { AgentConfigurationDisclosure } from './agent-configuration-disclosure'

export function AgentEngineSection({ value, inherited, onChange, beforeSwitch, onCatalog }: {
  value: RuntimePreferences
  inherited: RuntimePreferences
  onChange: (value: RuntimePreferences) => void
  beforeSwitch: () => Promise<unknown>
  onCatalog: (engines: EngineDescriptor[]) => void
}) {
  const { t } = useTranslation()
  const resolved = resolveRuntimePreferences(inherited, value)
  const selected = resolved.selected ?? 'native'
  const [engines, setEngines] = useState<EngineDescriptor[]>([])
  const [models, setModels] = useState<EngineModels>({ items: [] })
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const engine = engines.find((item) => item.id === selected)
  const ready = engine?.status === 'ready'
  const model = models.items.find((item) => item.id === resolved.codex?.model)

  useEffect(() => { if (engines.length) onCatalog(engines) }, [engines, onCatalog])

  useEffect(() => {
    let active = true
    const load = () => fetchAgentEngines().then(({ items }) => { if (active) setEngines(items) }).catch(() => { if (active) setError(t('agentRuntime.connectionFailed')) })
    void load()
    return () => { active = false }
  }, [t])

  useEffect(() => {
    if (selected === 'native' || !ready) { setModels({ items: [] }); return }
    let active = true
    void fetchEngineModels(selected).then((next) => { if (active) setModels(next) })
      .catch(() => { if (active) setError(t('agentRuntime.connectionFailed')) })
    return () => { active = false }
  }, [engine, ready, selected, t])

  const act = async (action: () => Promise<unknown>) => {
    setBusy(true); setError('')
    try { await action() } catch (cause) {
      console.warn('[agent-runtime] configuration action failed', cause)
      setError(cause instanceof Error ? cause.message : t('agentRuntime.connectionFailed'))
    } finally { setBusy(false) }
  }
  const refresh = async () => {
    const checked = await checkAgentEngine(selected)
    setEngines((items) => items.map((item) => item.id === checked.id ? checked : item))
  }

  return <AgentConfigurationDisclosure id="runtime" icon={Cpu} title={t('agentRuntime.title')}
    summary={[t(`agentRuntime.${selected}`), selected === 'codex' && (model?.display_name ?? resolved.codex?.model)].filter(Boolean).join(' · ')} defaultOpen>
    <div className="grid min-w-0 gap-3 md:grid-cols-2">
      <Field label={t('agentRuntime.engine')} inherited={value.selected == null}
        onReset={value.selected ? () => void act(async () => { await beforeSwitch(); const next = { ...value }; delete next.selected; onChange(next) }) : undefined}>
        <Select value={selected} disabled={busy} onValueChange={(id) => void act(async () => { await beforeSwitch(); onChange({ ...value, selected: id as AgentEngineID }) })}>
          <SelectTrigger size="sm" className="min-w-0 flex-1" aria-label={t('agentRuntime.engine')}><SelectValue /></SelectTrigger>
          <SelectContent><SelectGroup>
            {(engines.length ? engines : [{ id: 'native', name_key: 'agentRuntime.native' }, { id: 'codex', name_key: 'agentRuntime.codex' }]).map((item) => <SelectItem key={item.id} value={item.id}>{t(item.name_key)}</SelectItem>)}
          </SelectGroup></SelectContent>
        </Select>
      </Field>
      {selected !== 'native' && <Field label={t('agentRuntime.connection')}>
        <Badge variant="secondary" role="status">
          {ready && <Check className="text-[var(--nova-success)]" />}
          {t(`agentRuntime.status.${engine?.status ?? 'unchecked'}`)}
        </Badge>
        <Button size="sm" variant="outline" disabled={busy} onClick={() => void act(refresh)}>{t('agentRuntime.check')}</Button>
      </Field>}
    </div>
    {selected !== 'native' && <>
      <p className="text-xs leading-relaxed text-[var(--nova-text-muted)]">{t('agentRuntime.sharedCodexHome')}</p>
      {engine?.reason_key && <p className="text-xs leading-relaxed text-[var(--nova-text-muted)]">{t(engine.reason_key)}</p>}
      {engine?.status === 'auth_required' && <p role="status" className="text-xs leading-relaxed text-[var(--nova-text-muted)]">{t('agentRuntime.loginInTerminal')}</p>}
      <div className="grid min-w-0 gap-3 md:grid-cols-2">
        <Field label={t('agentRuntime.model')} inherited={value.codex == null}
          onReset={value.codex ? () => { const next = { ...value }; delete next.codex; onChange(next) } : undefined}>
          <Select value={resolved.codex?.model ?? ''} disabled={busy || !ready || !models.items.length}
            onValueChange={(model) => onChange({ ...value, codex: { model } })}>
            <SelectTrigger size="sm" className="min-w-0 flex-1" aria-label={t('agentRuntime.model')}><SelectValue placeholder={t('agentRuntime.chooseModel')} /></SelectTrigger>
            <SelectContent><SelectGroup>
              {resolved.codex && !model && <SelectItem value={resolved.codex.model}>{resolved.codex.model}</SelectItem>}
              {models.items.map((item) => <SelectItem key={item.id} value={item.id}>{item.display_name}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
        </Field>
        {model && <Field label={t('agentRuntime.effort')}>
          <Select value={resolved.codex?.effort ?? 'default'} disabled={busy} onValueChange={(effort) => onChange({ ...value, codex: { model: model.id, ...(effort === 'default' ? {} : { effort }) } })}>
            <SelectTrigger size="sm" className="min-w-0 flex-1" aria-label={t('agentRuntime.effort')}><SelectValue /></SelectTrigger>
            <SelectContent><SelectGroup><SelectItem value="default">{t('agentRuntime.defaultEffort')}</SelectItem>
              {model.efforts.map((effort) => <SelectItem key={effort} value={effort}>{effort}</SelectItem>)}
            </SelectGroup></SelectContent>
          </Select>
        </Field>}
      </div>
    </>}
    <div className="flex flex-col gap-1.5 text-[11px] leading-relaxed text-[var(--nova-text-faint)]">
      <p>{t('agentRuntime.defaultsOnly')}</p>
      {selected !== 'native' && <p>{t('agentRuntime.policy.providedToolsWithoutApproval')}</p>}
    </div>
    {error && <p role="alert" className="text-xs text-destructive">{error}</p>}
  </AgentConfigurationDisclosure>
}
