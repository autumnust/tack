package tui

import (
	"os"

	"github.com/autumnust/tack/internal/model"
	"gopkg.in/yaml.v3"
)

func saveConfig(path string, config *model.Config) error {
	data, err := yaml.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}
