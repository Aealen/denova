package config

import (
	"errors"
	"fmt"
	"strings"
)

// RuntimeID identifies an execution engine, independently of the business
// Agent kind or a model provider. Engine implementations remain in internal.
type RuntimeID string

const (
	RuntimeNative RuntimeID = "native"
	RuntimeCodex  RuntimeID = "codex"
)

var ErrInvalidAgentRuntime = errors.New("invalid Agent runtime configuration")

// CodexRuntimeSettings is one complete model selection. Model-specific effort
// availability is checked by the internal runtime model catalog at execution.
type CodexRuntimeSettings struct {
	Model  string `toml:"model" json:"model"`
	Effort string `toml:"effort,omitempty" json:"effort,omitempty"`
}

// RuntimePreferences retains inactive engine settings across selection changes.
// Native settings stay in their existing fields and must not be duplicated here.
type RuntimePreferences struct {
	Selected RuntimeID             `toml:"selected,omitempty" json:"selected,omitempty"`
	Codex    *CodexRuntimeSettings `toml:"codex,omitempty" json:"codex,omitempty"`
}

// AgentRuntimeSettings deliberately has no default applying to game, image, or
// background agents. A missing role preference inherits the parent layer.
type AgentRuntimeSettings struct {
	IDE     *RuntimePreferences `toml:"ide,omitempty" json:"ide,omitempty"`
	General *RuntimePreferences `toml:"general,omitempty" json:"general,omitempty"`
}

// RuntimeSelection is the resolved, immutable engine branch of a conversation.
// An absent selection in a released conversation always means Native.
type RuntimeSelection struct {
	Kind  RuntimeID             `json:"kind"`
	Codex *CodexRuntimeSettings `json:"codex,omitempty"`
}

func (preferences RuntimePreferences) Validate() error {
	switch preferences.Selected {
	case "", RuntimeNative, RuntimeCodex:
	default:
		return fmt.Errorf("%w: unknown selected runtime %q", ErrInvalidAgentRuntime, preferences.Selected)
	}
	if preferences.Codex != nil {
		return preferences.Codex.validate()
	}
	return nil
}

func (settings CodexRuntimeSettings) validate() error {
	if strings.TrimSpace(settings.Model) == "" || settings.Model != strings.TrimSpace(settings.Model) {
		return fmt.Errorf("%w: Codex model must be a non-empty identifier without surrounding whitespace", ErrInvalidAgentRuntime)
	}
	if settings.Effort != strings.TrimSpace(settings.Effort) {
		return fmt.Errorf("%w: Codex effort contains surrounding whitespace", ErrInvalidAgentRuntime)
	}
	return nil
}

func (selection RuntimeSelection) Validate(agentKind string) error {
	switch selection.Kind {
	case RuntimeNative:
		if selection.Codex != nil {
			return fmt.Errorf("%w: Native selection contains Codex settings", ErrInvalidAgentRuntime)
		}
	case RuntimeCodex:
		if agentKind != AgentKindIDE && agentKind != AgentKindGeneral {
			return fmt.Errorf("%w: external runtime does not support Agent kind %q", ErrInvalidAgentRuntime, agentKind)
		}
		if selection.Codex == nil {
			return fmt.Errorf("%w: Codex settings are required", ErrInvalidAgentRuntime)
		}
		return selection.Codex.validate()
	default:
		return fmt.Errorf("%w: unknown runtime %q", ErrInvalidAgentRuntime, selection.Kind)
	}
	return nil
}

// Selection projects only the active branch; retained inactive settings cannot
// enter execution input or invalidate the selected engine's context identity.
func (preferences RuntimePreferences) Selection(agentKind string) (RuntimeSelection, error) {
	kind := preferences.Selected
	if kind == "" {
		kind = RuntimeNative
	}
	selection := RuntimeSelection{Kind: kind}
	if kind == RuntimeCodex && preferences.Codex != nil {
		value := *preferences.Codex
		selection.Codex = &value
	}
	return selection, selection.Validate(agentKind)
}

// MergeAgentRuntimeSettings merges the selector and each engine independently.
// An engine model/effort object replaces its whole parent branch.
func MergeAgentRuntimeSettings(parent, child AgentRuntimeSettings) AgentRuntimeSettings {
	return AgentRuntimeSettings{
		IDE:     mergeRuntimePreferences(parent.IDE, child.IDE),
		General: mergeRuntimePreferences(parent.General, child.General),
	}
}

func mergeRuntimePreferences(parent, child *RuntimePreferences) *RuntimePreferences {
	if parent == nil && child == nil {
		return nil
	}
	merged := RuntimePreferences{}
	if parent != nil {
		merged = *parent
	}
	if child != nil {
		if child.Selected != "" {
			merged.Selected = child.Selected
		}
		if child.Codex != nil {
			merged.Codex = child.Codex
		}
	}
	if merged.Codex != nil {
		value := *merged.Codex
		merged.Codex = &value
	}
	return &merged
}

func (settings AgentRuntimeSettings) ForAgent(agentKind string) RuntimePreferences {
	var preferences *RuntimePreferences
	switch agentKind {
	case AgentKindIDE:
		preferences = settings.IDE
	case AgentKindGeneral:
		preferences = settings.General
	}
	if preferences == nil {
		return RuntimePreferences{}
	}
	return *mergeRuntimePreferences(nil, preferences)
}

func (settings AgentRuntimeSettings) Validate() error {
	for _, preferences := range []*RuntimePreferences{settings.IDE, settings.General} {
		if preferences != nil {
			if err := preferences.Validate(); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateSettingsRuntimes(settings Settings) error {
	if err := settings.AgentRuntimes.Validate(); err != nil {
		return err
	}
	for _, definition := range settings.CustomAgents {
		if definition.Runtime == nil {
			continue
		}
		if err := definition.Runtime.Validate(); err != nil {
			return fmt.Errorf("custom Agent %q: %w", definition.ID, err)
		}
		kind := CustomAgentRuntimeKind(definition)
		if definition.Runtime.Selected == RuntimeCodex && kind != AgentKindIDE && kind != AgentKindGeneral {
			return fmt.Errorf("%w: custom Agent %q does not support external execution", ErrInvalidAgentRuntime, definition.ID)
		}
	}
	return nil
}
