// Package manifestmigrate scans stored Pipeline, Notebook, and ModelService
// manifests for the removed placement.worker / placement.label fields
// (fed.md §13.6) and, on request, rewrites them without those fields.
package manifestmigrate

import (
	"bytes"
	"fmt"

	"gopkg.in/yaml.v3"
)

// StripPlacementWorkerLabel removes any "driver.placement.worker" and
// "driver.placement.label" key found anywhere in yamlText, at any nesting
// depth. This deliberately does not assume a manifest kind: a Pipeline has a
// driver block under spec.defaults and under each spec.steps[], while
// Notebook and ModelService each have exactly one under spec — walking the
// whole tree for any mapping literally named "driver" handles all three
// without the caller needing to know which one it has.
//
// changed reports whether anything was actually removed; when false, result
// equals yamlText unchanged (including its original formatting).
func StripPlacementWorkerLabel(yamlText string) (result string, changed bool, err error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		return "", false, fmt.Errorf("parse yaml: %w", err)
	}
	if !stripNode(&doc) {
		return yamlText, false, nil
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return "", false, fmt.Errorf("re-encode yaml: %w", err)
	}
	_ = enc.Close()
	return buf.String(), true, nil
}

// stripNode walks doc/sequence/mapping nodes looking for "driver" mapping
// children and strips placement.worker/label from each one it finds.
func stripNode(n *yaml.Node) bool {
	switch n.Kind {
	case yaml.DocumentNode, yaml.SequenceNode:
		changed := false
		for _, c := range n.Content {
			if stripNode(c) {
				changed = true
			}
		}
		return changed
	case yaml.MappingNode:
		changed := false
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			if key.Value == "driver" && val.Kind == yaml.MappingNode {
				if stripPlacement(val) {
					changed = true
				}
			}
			if stripNode(val) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

// stripPlacement finds the "placement" child of a driver mapping node and
// removes its "worker"/"label" keys.
func stripPlacement(driverNode *yaml.Node) bool {
	for i := 0; i+1 < len(driverNode.Content); i += 2 {
		key, val := driverNode.Content[i], driverNode.Content[i+1]
		if key.Value == "placement" && val.Kind == yaml.MappingNode {
			return removeKeys(val, "worker", "label")
		}
	}
	return false
}

// removeKeys drops the given keys from a mapping node's Content in place.
func removeKeys(mapNode *yaml.Node, keys ...string) bool {
	changed := false
	kept := make([]*yaml.Node, 0, len(mapNode.Content))
	for i := 0; i+1 < len(mapNode.Content); i += 2 {
		k, v := mapNode.Content[i], mapNode.Content[i+1]
		drop := false
		for _, target := range keys {
			if k.Value == target {
				drop = true
				break
			}
		}
		if drop {
			changed = true
			continue
		}
		kept = append(kept, k, v)
	}
	mapNode.Content = kept
	return changed
}
