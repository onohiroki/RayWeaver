package rayio

import (
	"gopkg.in/yaml.v3"

	"github.com/hiroki/rayweaver/internal/types"
)

func ReadInput(data []byte) (*types.Input, error) {
	var input types.Input
	if err := yaml.Unmarshal(data, &input); err != nil {
		return nil, err
	}
	return &input, nil
}

func WriteOutput(output *types.Output) ([]byte, error) {
	return yaml.Marshal(output)
}
