package copycmd

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/qlustered/qctl/internal/api"
	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/qlustered/qctl/internal/pkg/secrets"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// dataSourcesFileFlag names the YAML list of the data sources a copy takes,
// without the leading dashes.
const dataSourcesFileFlag = "data-sources-file"

// dsKeySourceID is the key of a --data-sources-file entry that names the data
// source it copies.
const dsKeySourceID = "source_id"

// secretMaskRune is the mask character a secret must not hold.
const secretMaskRune = '•'

var (
	// dataSourceSecretKeys are the keys of a --data-sources-file entry that
	// carry a secret.
	dataSourceSecretKeys = cloud_sources.CopySecretKeys()
	// dataSourceEntryKeys are the keys of a --data-sources-file entry.
	dataSourceEntryKeys = slices.Concat([]string{dsKeySourceID}, dataSourceKeys, dataSourceSecretKeys)
)

// dataSourceEntryLabel names the entry'th entry, counted from 1, of
// --data-sources-file, and the data source it copies.
func dataSourceEntryLabel(entry, sourceID int) string {
	return fmt.Sprintf("--%s entry %d (source_id %d)", dataSourcesFileFlag, entry, sourceID)
}

// readDataSourcesFile reads the --data-sources-file path, where "-" is cmd's
// stdin, and parses it with parseDataSourcesFile.
func readDataSourcesFile(cmd *cobra.Command, path string) ([]dataSourceSpec, error) {
	text, err := cmdutil.ReadTextFile(cmd, path, "data sources")
	if err != nil {
		return nil, err
	}
	return parseDataSourcesFile([]byte(text))
}

// parseDataSourcesFile parses data, one YAML document holding a non-empty list
// of entries, into one spec per entry, in order. A key given as null counts as
// left out. A secret whose whole value is ${VAR} takes the value of the
// environment variable VAR. A malformed file or entry exits 2.
func parseDataSourcesFile(data []byte) ([]dataSourceSpec, error) {
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return nil, invalidYAMLFile(dataSourcesFileFlag, err)
	}
	if len(docs) > 1 {
		return nil, qerrors.NewBadUsagef("--%s holds %d YAML documents; list every entry in one",
			dataSourcesFileFlag, len(docs))
	}
	var root yaml.Node
	if len(docs) == 1 {
		if err := yaml.Unmarshal(docs[0], &root); err != nil {
			return nil, invalidYAMLFile(dataSourcesFileFlag, err)
		}
	}
	if len(root.Content) == 0 {
		return nil, noDataSourceEntries()
	}
	list := yamlAliasTarget(root.Content[0])
	if list.Kind != yaml.SequenceNode {
		return nil, qerrors.NewBadUsagef("--%s must be a YAML list with one entry per copy, got %s",
			dataSourcesFileFlag, yamlKind(list))
	}
	if len(list.Content) == 0 {
		return nil, noDataSourceEntries()
	}
	specs := make([]dataSourceSpec, 0, len(list.Content))
	aliased := yamlAliasedNodes(&root)
	for i, item := range list.Content {
		spec, err := parseDataSourceEntry(i+1, item, aliased)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, nil
}

// parseDataSourceEntry parses node, the entry'th entry of --data-sources-file,
// whose document's aliases refer to aliased.
func parseDataSourceEntry(entry int, node *yaml.Node, aliased map[*yaml.Node]bool) (dataSourceSpec, error) {
	node = yamlAliasTarget(node)
	if node.Kind != yaml.MappingNode {
		return dataSourceSpec{}, badDataSourceEntryNumber(entry, "expected a mapping of keys, got %s", yamlKind(node))
	}
	id, err := parseSourceID(entry, node)
	if err != nil {
		return dataSourceSpec{}, err
	}
	spec := dataSourceSpec{Entry: entry, ID: id, Values: map[string]string{}, Secrets: map[string]string{}}
	seen := make(map[string]bool, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content); i += 2 {
		keyNode, key := node.Content[i], node.Content[i].Value
		if keyNode.Kind != yaml.ScalarNode || !slices.Contains(dataSourceEntryKeys, key) {
			return dataSourceSpec{}, badDataSourceEntry(spec, "unknown key %s; valid keys are %s",
				unknownYAMLKey(keyNode, dataSourceEntryKeys), strings.Join(dataSourceEntryKeys, ", "))
		}
		if seen[key] {
			return dataSourceSpec{}, badDataSourceEntry(spec, "%s is given more than once", key)
		}
		seen[key] = true
		if key == dsKeySourceID {
			continue
		}
		if slices.Contains(dataSourceSecretKeys, key) {
			if err := checkSecretNode(key, node.Content[i+1], aliased); err != nil {
				return dataSourceSpec{}, qerrors.Wrap(qerrors.ExitBadUsage, err, dataSourceEntryLabel(spec.Entry, spec.ID))
			}
		}
		var value *string
		if err := node.Content[i+1].Decode(&value); err != nil {
			return dataSourceSpec{}, badDataSourceEntry(spec, "%s must be a string, got %s",
				key, yamlKind(yamlAliasTarget(node.Content[i+1])))
		}
		if value == nil {
			continue
		}
		if slices.Contains(dataSourceSecretKeys, key) {
			secret, err := entrySecret(spec, key, *value)
			if err != nil {
				return dataSourceSpec{}, err
			}
			spec.Secrets[key] = secret
			continue
		}
		if err := checkEntryValue(spec, key, *value); err != nil {
			return dataSourceSpec{}, err
		}
		spec.Values[key] = *value
	}
	return spec, nil
}

// parseSourceID returns the source_id of node, the entry'th entry of
// --data-sources-file.
func parseSourceID(entry int, node *yaml.Node) (int, error) {
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value != dsKeySourceID {
			continue
		}
		value := yamlAliasTarget(node.Content[i+1])
		if value.ShortTag() == "!!null" {
			break
		}
		var id int
		if value.ShortTag() != "!!int" || value.Decode(&id) != nil || id <= 0 {
			got := yamlKind(value)
			if value.Kind == yaml.ScalarNode {
				got = strconv.Quote(value.Value)
			}
			return 0, badDataSourceEntryNumber(entry, "source_id must be a positive integer, got %s", got)
		}
		return id, nil
	}
	return 0, badDataSourceEntryNumber(entry, "source_id is required")
}

// checkEntryValue refuses value for the non-secret key of spec's entry when
// the key cannot take it.
func checkEntryValue(spec dataSourceSpec, key, value string) error {
	switch {
	case key == dsKeyName && value == "":
		return badDataSourceEntry(spec, "name cannot be empty; leave it out to keep the source's name")
	case key == dsKeyState && !slices.Contains(dataSourceStates, api.DatasetCopyDataSourceRequestState(value)):
		return badDataSourceEntry(spec, "state must be one of %s, got %q", joinStates(), value)
	}
	return nil
}

// entrySecret returns the value of the secret key of spec's entry, given as
// text, as resolveSecret resolves it. No error it returns holds the value.
func entrySecret(spec dataSourceSpec, key, text string) (string, error) {
	secret, err := resolveSecret(key, text)
	if err != nil {
		return "", qerrors.Wrap(qerrors.ExitBadUsage, err, dataSourceEntryLabel(spec.Entry, spec.ID))
	}
	return secret, nil
}

// resolveSecret returns the value of the credential key given as text: the
// value of the environment variable when text is exactly ${VAR}, otherwise
// text. It refuses an empty text, an unset or empty variable, a variable
// that is not valid UTF-8, and a value holding secretMaskRune, with an error
// that starts with key and holds no value.
func resolveSecret(key, text string) (string, error) {
	if text == "" {
		if key == s3AccessKeyKey {
			return "", fmt.Errorf("%s cannot be empty; leave the key out to keep the source's access key", key)
		}
		return "", fmt.Errorf("%s cannot be empty: the server reads an empty secret as not supplied, "+
			"so leave the key out", key)
	}
	secret := text
	if name, ok := secrets.ExactEnvRef(text); ok {
		value, set := os.LookupEnv(name)
		switch {
		case !set:
			return "", fmt.Errorf("%s: environment variable %s is not set", key, name)
		case value == "":
			return "", fmt.Errorf("%s: environment variable %s is empty", key, name)
		case !utf8.ValidString(value):
			return "", fmt.Errorf("%s: environment variable %s is not valid UTF-8", key, name)
		}
		secret = value
	}
	if strings.ContainsRune(secret, secretMaskRune) {
		if key == s3AccessKeyKey {
			return "", fmt.Errorf("%s contains the mask character %c, so it is a masked value; "+
				"give the access key itself", key, secretMaskRune)
		}
		return "", fmt.Errorf("%s contains the mask character %c, which makes the server ignore it; "+
			"give the secret itself", key, secretMaskRune)
	}
	return secret, nil
}

// invalidYAMLFile is the exit-2 error for a file named by --flag that is not
// valid YAML, with err replaced when it quotes an anchor name.
func invalidYAMLFile(flag string, err error) error {
	if strings.Contains(err.Error(), "anchor '") {
		err = errors.New("yaml: a value starting with * is read as an alias, and this one is invalid; quote the value")
	}
	return qerrors.Wrap(qerrors.ExitBadUsage, err, "--"+flag+" is not valid YAML")
}

// noDataSourceEntries is the exit-2 error for a --data-sources-file with no
// entry.
func noDataSourceEntries() error {
	return qerrors.NewBadUsagef("--%s lists no data source; leave the flag out to copy none", dataSourcesFileFlag)
}

// badDataSourceEntry is the exit-2 error for spec's entry of
// --data-sources-file.
func badDataSourceEntry(spec dataSourceSpec, format string, args ...any) error {
	return qerrors.NewBadUsagef("%s: %s", dataSourceEntryLabel(spec.Entry, spec.ID), fmt.Sprintf(format, args...))
}

// badDataSourceEntryNumber is the exit-2 error for the entry'th entry of
// --data-sources-file, before its source_id is known.
func badDataSourceEntryNumber(entry int, format string, args ...any) error {
	return qerrors.NewBadUsagef("--%s entry %d: %s", dataSourcesFileFlag, entry, fmt.Sprintf(format, args...))
}

// unknownYAMLKey names node, a mapping key not in valid, for an error: quoted
// when it is a plain scalar that is in valid once its hyphens are underscores,
// otherwise by its line.
func unknownYAMLKey(node *yaml.Node, valid []string) string {
	if node.Kind == yaml.ScalarNode && slices.Contains(valid, strings.ReplaceAll(node.Value, "-", "_")) {
		return strconv.Quote(node.Value)
	}
	return fmt.Sprintf("on line %d", node.Line)
}

// yamlAliasedNodes returns the nodes that an alias under root refers to.
func yamlAliasedNodes(root *yaml.Node) map[*yaml.Node]bool {
	aliased := map[*yaml.Node]bool{}
	var walk func(*yaml.Node)
	walk = func(node *yaml.Node) {
		if node.Kind == yaml.AliasNode {
			aliased[node.Alias] = true
		}
		for _, child := range node.Content {
			walk(child)
		}
	}
	walk(root)
	return aliased
}

// checkSecretNode refuses node, the value of the credential key, when it
// carries an anchor that is not in aliased or a tag other than !!str, with an
// error that starts with key and holds no value.
func checkSecretNode(key string, node *yaml.Node, aliased map[*yaml.Node]bool) error {
	switch {
	case node.Anchor != "" && !aliased[node]:
		return fmt.Errorf("%s: a value starting with & is read as a YAML anchor; quote the value", key)
	case node.Style&yaml.TaggedStyle != 0 && node.Tag != "!!str":
		return fmt.Errorf("%s: a value starting with ! is read as a YAML tag; quote the value", key)
	}
	return nil
}

// yamlAliasTarget returns the node an alias node refers to, else node.
func yamlAliasTarget(node *yaml.Node) *yaml.Node {
	for node.Kind == yaml.AliasNode && node.Alias != nil {
		node = node.Alias
	}
	return node
}

// yamlKind names the kind of node for an error message, without its value.
func yamlKind(node *yaml.Node) string {
	switch node.Kind {
	case yaml.SequenceNode:
		return "a list"
	case yaml.MappingNode:
		return "a mapping"
	}
	return "a scalar"
}
