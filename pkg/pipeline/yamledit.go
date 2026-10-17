package pipeline

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// EditYAML applies edit to the document's top-level mapping and re-encodes
// it. Only the nodes edit touches change; keys, order, quoting and comments
// stay as the author wrote them. Use this instead of Marshal whenever a
// stored pipeline needs a server-side field (version, snapshot source):
// re-marshalling the parsed struct rewrites the whole document.
func EditYAML(yamlText string, edit func(root *yaml.Node)) (string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return "", fmt.Errorf("parse pipeline yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return "", fmt.Errorf("pipeline yaml must be a mapping")
	}
	edit(doc.Content[0])
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", fmt.Errorf("encode pipeline yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return "", fmt.Errorf("encode pipeline yaml: %w", err)
	}
	return buf.String(), nil
}

// YAMLGet returns the value for key in mapping m, or nil.
func YAMLGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// YAMLMapping returns the mapping under key in m, adding an empty one if
// key is missing.
func YAMLMapping(m *yaml.Node, key string) *yaml.Node {
	if v := YAMLGet(m, key); v != nil {
		return v
	}
	v := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, v)
	return v
}

// YAMLSet sets key in mapping m to a scalar of the given tag ("!!str",
// "!!int"), adding the key if it is missing.
func YAMLSet(m *yaml.Node, key, value, tag string) {
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = scalar
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, scalar)
}
