package config

import (
	"context"
	"encoding/json"

	"gopkg.in/yaml.v3"
)

var creators = make(map[string]Creator)

// Creator creates default config struct for a module
type Creator func() any

// RegisterConfigCreator registers a config struct for parsing
func RegisterConfigCreator(name string, creator Creator) {
	name += "_CONFIG"
	creators[name] = creator
}

func parseJSON(data []byte) (map[string]any, error) {
	result := make(map[string]any)
	for name, creator := range creators {
		config := creator()
		if err := json.Unmarshal(data, config); err != nil {
			return nil, err
		}
		result[name] = config
	}
	return result, nil
}

func parseYAML(data []byte) (map[string]any, error) {
	result := make(map[string]any)
	for name, creator := range creators {
		config := creator()
		if err := yaml.Unmarshal(data, config); err != nil {
			return nil, err
		}
		result[name] = config
	}
	return result, nil
}

func WithJSONConfig(ctx context.Context, data []byte) (context.Context, error) {
	var configs map[string]any
	var err error
	configs, err = parseJSON(data)
	if err != nil {
		return ctx, err
	}
	for name, config := range configs {
		ctx = context.WithValue(ctx, name, config)
	}
	return ctx, nil
}

func WithYAMLConfig(ctx context.Context, data []byte) (context.Context, error) {
	var configs map[string]any
	var err error
	configs, err = parseYAML(data)
	if err != nil {
		return ctx, err
	}
	for name, config := range configs {
		ctx = context.WithValue(ctx, name, config)
	}
	return ctx, nil
}

func WithConfig(ctx context.Context, name string, cfg any) context.Context {
	name += "_CONFIG"
	return context.WithValue(ctx, name, cfg)
}

// FromContext extracts config from a context
func FromContext(ctx context.Context, name string) any {
	return ctx.Value(name + "_CONFIG")
}
