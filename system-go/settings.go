package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

const settingsKey = "settings-v1"

// pluginSettings holds the operator's defaults. It is a comparable struct on
// purpose: importBackup uses a zero-value check to tell "no settings in this
// backup" from "settings that happen to be empty".
type pluginSettings struct {
	SchemaVersion int `json:"schema_version"`
	// DefaultTarget is used by a subscription that names none. Empty means the
	// client's own class decides, which is the behaviour without any settings.
	DefaultTarget string `json:"default_target,omitempty"`
	// DefaultUA is sent to providers for subscriptions that name none.
	DefaultUA string `json:"default_ua,omitempty"`
}

func (rt *runtime) loadSettings() (pluginSettings, error) {
	value, found, err := rt.kvGet(settingsKey)
	if err != nil || !found {
		return pluginSettings{SchemaVersion: 1}, err
	}
	var out pluginSettings
	if err := json.Unmarshal(value, &out); err != nil {
		return pluginSettings{}, fmt.Errorf("decode settings: %w", err)
	}
	if out.SchemaVersion == 0 {
		out.SchemaVersion = 1
	}
	return out, nil
}

func (rt *runtime) saveSettings(s pluginSettings) error {
	if s.SchemaVersion == 0 {
		s.SchemaVersion = 1
	}
	if len(s.DefaultTarget) > 64 || hasControl(s.DefaultTarget) {
		return fmt.Errorf("default_target must be printable and at most 64 characters")
	}
	if len(s.DefaultUA) > 256 || hasControl(s.DefaultUA) {
		return fmt.Errorf("default_ua must be printable and at most 256 characters")
	}
	s.DefaultTarget = strings.TrimSpace(s.DefaultTarget)
	s.DefaultUA = strings.TrimSpace(s.DefaultUA)
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return rt.kvPut(settingsKey, raw)
}
