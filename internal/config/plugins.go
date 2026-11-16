package config

import (
	"fmt"
	"time"

	"github.com/underwoo/elzar/internal/plugin"
)

func loadPlugins(raw any, options *plugin.Options) error {
	table, ok := raw.(map[string]any)
	if !ok {
		return fmt.Errorf("plugins must be a table")
	}
	for key, value := range table {
		switch key {
		case "enabled":
			enabled, ok := value.(bool)
			if !ok {
				return fmt.Errorf("plugins.enabled must be a boolean")
			}
			options.Enabled = enabled
		case "site_dir":
			dir, ok := value.(string)
			if !ok {
				return fmt.Errorf("plugins.site_dir must be a string")
			}
			options.SiteDir = dir
		case "disabled":
			values, ok := value.([]any)
			if !ok {
				return fmt.Errorf("plugins.disabled must be an array of strings")
			}
			options.Disabled = nil
			for _, value := range values {
				name, ok := value.(string)
				if !ok {
					return fmt.Errorf("plugins.disabled must be an array of strings")
				}
				options.Disabled = append(options.Disabled, name)
			}
		case "timeout":
			text, ok := value.(string)
			if !ok {
				return fmt.Errorf("plugins.timeout must be a positive duration")
			}
			duration, err := time.ParseDuration(text)
			if err != nil || duration <= 0 {
				return fmt.Errorf("plugins.timeout must be a positive duration")
			}
			options.Timeout = duration
		default:
			return fmt.Errorf("unknown plugins setting %q", key)
		}
	}
	return nil
}
