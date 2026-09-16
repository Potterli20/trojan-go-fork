package config

import (
	"context"
	"encoding/json"
	"maps"
	"sync"

	"gopkg.in/yaml.v3"
)

// configKey 是 context value 的自定义 key 类型，避免使用内置 string 类型造成碰撞
type configKey string

var (
	creators = make(map[string]Creator)
	mu       sync.RWMutex
)

// Creator creates default config struct for a module
type Creator func() any

// RegisterConfigCreator registers a config struct for parsing
func RegisterConfigCreator(name string, creator Creator) {
	mu.Lock()
	defer mu.Unlock()
	name += "_CONFIG"
	creators[name] = creator
}

func parseJSON(data []byte) (map[string]any, error) {
	mu.RLock()
	creatorsSnapshot := make(map[string]Creator, len(creators))
	maps.Copy(creatorsSnapshot, creators)
	mu.RUnlock()

	result := make(map[string]any)
	for name, creator := range creatorsSnapshot {
		config := creator()
		if err := json.Unmarshal(data, config); err != nil {
			return nil, err
		}
		result[name] = config
	}
	return result, nil
}

func parseYAML(data []byte) (map[string]any, error) {
	mu.RLock()
	creatorsSnapshot := make(map[string]Creator, len(creators))
	maps.Copy(creatorsSnapshot, creators)
	mu.RUnlock()

	result := make(map[string]any)
	for name, creator := range creatorsSnapshot {
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
		ctx = context.WithValue(ctx, configKey(name), config)
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
		ctx = context.WithValue(ctx, configKey(name), config)
	}
	return ctx, nil
}

func WithConfig(ctx context.Context, name string, cfg any) context.Context {
	return context.WithValue(ctx, configKey(name+"_CONFIG"), cfg)
}

// FromContext extracts config from a context
func FromContext(ctx context.Context, name string) any {
	return ctx.Value(configKey(name + "_CONFIG"))
}
