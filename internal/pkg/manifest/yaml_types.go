package manifest

import (
	"fmt"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

type yamlTypeVisitor func(*yaml.Node, reflect.Type, string, bool) error

func walkYAMLTypes(node *yaml.Node, typ reflect.Type, path string, listElement bool, visit yamlTypeVisitor) error {
	for node.Kind == yaml.AliasNode {
		node = node.Alias
	}
	if node.Kind == yaml.DocumentNode && len(node.Content) == 1 {
		return walkYAMLTypes(node.Content[0], typ, path, listElement, visit)
	}
	if typ == nil {
		return nil
	}
	if err := visit(node, typ, path, listElement); err != nil {
		return err
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Slice, reflect.Array:
		if node.Kind == yaml.SequenceNode {
			for i, child := range node.Content {
				if err := walkYAMLTypes(child, typ.Elem(), fmt.Sprintf("%s[%d]", path, i), true, visit); err != nil {
					return err
				}
			}
		}
	case reflect.Map:
		return walkYAMLMapTypes(node, typ, path, visit)
	case reflect.Struct:
		return walkYAMLStructTypes(node, typ, path, visit)
	}
	return nil
}

func walkYAMLMapTypes(node *yaml.Node, typ reflect.Type, path string, visit yamlTypeVisitor) error {
	for _, pair := range yamlMappingPairs(node) {
		childPath := fmt.Sprintf("%s[%q]", path, pair[0].Value)
		if err := walkYAMLTypes(pair[0], typ.Key(), childPath+" (key)", false, visit); err != nil {
			return err
		}
		if err := walkYAMLTypes(pair[1], typ.Elem(), childPath, false, visit); err != nil {
			return err
		}
	}
	return nil
}

func walkYAMLStructTypes(node *yaml.Node, typ reflect.Type, path string, visit yamlTypeVisitor) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	fields, inlineElem := yamlStructFields(typ)
	for _, pair := range yamlMappingPairs(node) {
		name := yamlKeyName(pair[0])
		childType, ok := fields[name]
		if !ok {
			childType = inlineElem
		}
		childPath := name
		if path != "" {
			childPath = path + "." + childPath
		}
		if err := walkYAMLTypes(pair[1], childType, childPath, false, visit); err != nil {
			return err
		}
	}
	return nil
}

func yamlStructFields(typ reflect.Type) (map[string]reflect.Type, reflect.Type) {
	fields := make(map[string]reflect.Type)
	var inlineElem reflect.Type
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		parts := strings.Split(field.Tag.Get("yaml"), ",")
		if parts[0] == "-" {
			continue
		}
		if slices.Contains(parts[1:], "inline") {
			childType := field.Type
			for childType.Kind() == reflect.Pointer {
				childType = childType.Elem()
			}
			switch childType.Kind() {
			case reflect.Struct:
				nested, nestedInline := yamlStructFields(childType)
				for name, typ := range nested {
					fields[name] = typ
				}
				if nestedInline != nil {
					inlineElem = nestedInline
				}
			case reflect.Map:
				inlineElem = childType.Elem()
			}
			continue
		}
		name := parts[0]
		if name == "" {
			name = strings.ToLower(field.Name)
		}
		fields[name] = field.Type
	}
	return fields, inlineElem
}

func yamlMappingPairs(node *yaml.Node) [][2]*yaml.Node {
	var pairs [][2]*yaml.Node
	collectYAMLMappingPairs(node, make(map[string]bool), make(map[*yaml.Node]bool), &pairs)
	return pairs
}

func yamlKeyName(node *yaml.Node) string {
	var name string
	if node.Decode(&name) == nil {
		return name
	}
	return node.Value
}

func collectYAMLMappingPairs(node *yaml.Node, keys map[string]bool, seen map[*yaml.Node]bool, pairs *[][2]*yaml.Node) {
	if seen[node] {
		return
	}
	seen[node] = true
	if node.Kind == yaml.AliasNode {
		collectYAMLMappingPairs(node.Alias, keys, seen, pairs)
		return
	}
	if node.Kind == yaml.SequenceNode {
		for _, child := range node.Content {
			collectYAMLMappingPairs(child, keys, seen, pairs)
		}
		return
	}
	if node.Kind != yaml.MappingNode {
		return
	}
	var merge *yaml.Node
	for i := 0; i+1 < len(node.Content); i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		name := yamlKeyName(key)
		if key.ShortTag() == "!!merge" {
			merge = value
		} else if !keys[name] {
			keys[name] = true
			*pairs = append(*pairs, [2]*yaml.Node{key, value})
		}
	}
	if merge != nil {
		collectYAMLMappingPairs(merge, keys, seen, pairs)
	}
}
