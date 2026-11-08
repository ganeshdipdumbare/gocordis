// Package loader mounts plugins from a declarative entry tree.
package loader

import (
	"fmt"
	"strconv"

	"gopkg.in/yaml.v3"
)

// Expr is a !!js scalar evaluated when the entry is mounted.
type Expr string

// MarshalYAML keeps expressions tagged when an entry tree is written back.
func (e Expr) MarshalYAML() (any, error) {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!js", Value: string(e)}, nil
}

// EntryOptions describes one mounted plugin or one group of entries.
type EntryOptions struct {
	ID        string         `yaml:"id,omitempty" json:"id,omitempty"`
	Name      string         `yaml:"name,omitempty" json:"name,omitempty"`
	URL       string         `yaml:"url,omitempty" json:"url,omitempty"`
	Config    any            `yaml:"config,omitempty" json:"config,omitempty"`
	Group     bool           `yaml:"group,omitempty" json:"group,omitempty"`
	Disabled  any            `yaml:"disabled,omitempty" json:"disabled,omitempty"`
	Inject    []string       `yaml:"inject,omitempty" json:"inject,omitempty"`
	Intercept map[string]any `yaml:"intercept,omitempty" json:"intercept,omitempty"`
	Isolate   map[string]any `yaml:"isolate,omitempty" json:"isolate,omitempty"`

	// Entries is the child list of a group.
	Entries []EntryOptions `yaml:"-" json:"-"`
}

// UnmarshalYAML decodes an entry, preserving !!js expressions and decoding a
// group's config as its child entries.
func (o *EntryOptions) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.DocumentNode {
		node = node.Content[0]
	}
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("entry must be a mapping")
	}

	group := false
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == "group" {
			group = node.Content[i+1].Value == "true"
		}
	}

	for i := 0; i+1 < len(node.Content); i += 2 {
		key := node.Content[i].Value
		value := node.Content[i+1]
		switch key {
		case "id":
			o.ID = value.Value
		case "name":
			o.Name = value.Value
		case "url":
			o.URL = value.Value
		case "group":
			o.Group = value.Value == "true"
		case "disabled":
			decoded, err := decodeValue(value)
			if err != nil {
				return err
			}
			o.Disabled = decoded
		case "inject":
			inject, err := decodeInject(value)
			if err != nil {
				return err
			}
			o.Inject = inject
		case "intercept":
			decoded, err := decodeValue(value)
			if err != nil {
				return err
			}
			if decoded != nil {
				mapping, ok := decoded.(map[string]any)
				if !ok {
					return fmt.Errorf("intercept must be a mapping")
				}
				o.Intercept = mapping
			}
		case "isolate":
			decoded, err := decodeValue(value)
			if err != nil {
				return err
			}
			if decoded != nil {
				mapping, ok := decoded.(map[string]any)
				if !ok {
					return fmt.Errorf("isolate must be a mapping")
				}
				o.Isolate = mapping
			}
		case "config":
			if group {
				var entries []EntryOptions
				if err := value.Decode(&entries); err != nil {
					return fmt.Errorf("group config must be an entry list: %w", err)
				}
				o.Entries = entries
			} else {
				decoded, err := decodeValue(value)
				if err != nil {
					return err
				}
				o.Config = decoded
			}
		}
	}
	return nil
}

// MarshalYAML writes an entry back, using config for a group's children.
func (o EntryOptions) MarshalYAML() (any, error) {
	result := map[string]any{}
	if o.ID != "" {
		result["id"] = o.ID
	}
	if o.URL != "" && (o.Name == "" || o.Name == o.URL) {
		result["url"] = o.URL
	} else if o.Name != "" {
		result["name"] = o.Name
	}
	if o.Group {
		result["group"] = true
		result["config"] = o.Entries
	} else if o.Config != nil {
		result["config"] = o.Config
	}
	if o.Disabled != nil {
		result["disabled"] = o.Disabled
	}
	if len(o.Inject) > 0 {
		result["inject"] = o.Inject
	}
	if len(o.Intercept) > 0 {
		result["intercept"] = o.Intercept
	}
	if len(o.Isolate) > 0 {
		result["isolate"] = o.Isolate
	}
	return result, nil
}

func decodeInject(node *yaml.Node) ([]string, error) {
	switch node.Kind {
	case yaml.SequenceNode:
		result := make([]string, 0, len(node.Content))
		for _, item := range node.Content {
			result = append(result, item.Value)
		}
		return result, nil
	case yaml.MappingNode:
		result := make([]string, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			result = append(result, node.Content[i].Value)
		}
		return result, nil
	case yaml.ScalarNode:
		if node.Tag == "!!null" {
			return nil, nil
		}
	}
	return nil, fmt.Errorf("inject must be a list or a mapping")
}

func decodeValue(node *yaml.Node) (any, error) {
	if node.Kind == yaml.AliasNode {
		return decodeValue(node.Alias)
	}
	switch node.Kind {
	case yaml.DocumentNode:
		return decodeValue(node.Content[0])
	case yaml.SequenceNode:
		result := make([]any, 0, len(node.Content))
		for _, item := range node.Content {
			value, err := decodeValue(item)
			if err != nil {
				return nil, err
			}
			result = append(result, value)
		}
		return result, nil
	case yaml.MappingNode:
		result := make(map[string]any, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			value, err := decodeValue(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			result[node.Content[i].Value] = value
		}
		return result, nil
	case yaml.ScalarNode:
		if node.Tag == "!!js" || node.Tag == "tag:yaml.org,2002:js" || node.ShortTag() == "!!js" {
			return Expr(node.Value), nil
		}
		switch node.Tag {
		case "!!null":
			return nil, nil
		case "!!bool":
			return node.Value == "true", nil
		case "!!int":
			if value, err := strconv.Atoi(node.Value); err == nil {
				return value, nil
			}
			return strconv.ParseInt(node.Value, 10, 64)
		case "!!float":
			return strconv.ParseFloat(node.Value, 64)
		default:
			return node.Value, nil
		}
	}
	return nil, fmt.Errorf("unsupported YAML node")
}
