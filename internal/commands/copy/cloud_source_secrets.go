package copycmd

import (
	"fmt"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/cloud_sources"
	"github.com/qlustered/qctl/internal/cmdutil"
	qerrors "github.com/qlustered/qctl/internal/errors"
	pkgmanifest "github.com/qlustered/qctl/internal/pkg/manifest"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

// secretsFileFlag names the YAML mapping of the credentials a cloud source
// copy takes, without the leading dashes.
const secretsFileFlag = "secrets-file"

// s3AccessKeyKey is the wire key of the S3 access-key ID.
const s3AccessKeyKey = "s3_access_key"

// secretsFileKeys are the keys of --secrets-file.
var secretsFileKeys = cloud_sources.CopyRequestSecretKeys()

// readSecretsFile reads the --secrets-file path, where "-" is cmd's stdin,
// and parses it with parseSecretsFile.
func readSecretsFile(cmd *cobra.Command, path string) (map[string]string, error) {
	text, err := cmdutil.ReadTextFile(cmd, path, "secrets")
	if err != nil {
		return nil, err
	}
	return parseSecretsFile([]byte(text))
}

// parseSecretsFile parses data, one YAML document holding a non-empty mapping
// of secretsFileKeys to strings, into each key's value as resolveSecret
// resolves it. A key given as null counts as left out. A malformed file exits
// 2, and no error it returns holds a value.
func parseSecretsFile(data []byte) (map[string]string, error) {
	docs, err := pkgmanifest.SplitDocuments(data)
	if err != nil {
		return nil, invalidYAMLFile(secretsFileFlag, err)
	}
	if len(docs) > 1 {
		return nil, qerrors.NewBadUsagef("--%s holds %d YAML documents; put every secret in one mapping",
			secretsFileFlag, len(docs))
	}
	var root yaml.Node
	if len(docs) == 1 {
		if err := yaml.Unmarshal(docs[0], &root); err != nil {
			return nil, invalidYAMLFile(secretsFileFlag, err)
		}
	}
	if len(root.Content) == 0 {
		return nil, noSecretsInFile()
	}
	mapping := yamlAliasTarget(root.Content[0])
	if mapping.Kind != yaml.MappingNode {
		return nil, qerrors.NewBadUsagef("--%s must be a YAML mapping of secret keys to values, got %s",
			secretsFileFlag, yamlKind(mapping))
	}
	if len(mapping.Content) == 0 {
		return nil, noSecretsInFile()
	}
	aliased := yamlAliasedNodes(&root)
	supplied := make(map[string]string, len(mapping.Content)/2)
	seen := make(map[string]bool, len(mapping.Content)/2)
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		key := mapping.Content[i].Value
		if err := checkSecretsFileKey(mapping.Content[i]); err != nil {
			return nil, err
		}
		if seen[key] {
			return nil, badSecretsFile("%s is given more than once", key)
		}
		seen[key] = true
		secret, err := secretsFileValue(key, mapping.Content[i+1], aliased)
		if err != nil {
			return nil, err
		}
		if secret != nil {
			supplied[key] = *secret
		}
	}
	return supplied, nil
}

// secretsFileValue returns node, the value of key in --secrets-file whose
// aliases refer to aliased, as resolveSecret resolves it, or nil when node is
// null. No error it returns holds the value.
func secretsFileValue(key string, node *yaml.Node, aliased map[*yaml.Node]bool) (*string, error) {
	if err := checkSecretNode(key, node, aliased); err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "--"+secretsFileFlag)
	}
	var value *string
	if err := node.Decode(&value); err != nil {
		return nil, badSecretsFile("%s must be a string, got %s", key, yamlKind(yamlAliasTarget(node)))
	}
	if value == nil {
		return nil, nil
	}
	secret, err := resolveSecret(key, *value)
	if err != nil {
		return nil, qerrors.Wrap(qerrors.ExitBadUsage, err, "--"+secretsFileFlag)
	}
	return &secret, nil
}

// checkSecretsFileKey refuses node, a key of --secrets-file, unless it is a
// plain scalar naming one of secretsFileKeys. The error names the key as
// unknownYAMLKey does.
func checkSecretsFileKey(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		switch {
		case slices.Contains(secretsFileKeys, node.Value):
			return nil
		case node.Value == dsKeyDropboxAccessToken:
			return badSecretsFile("a copy cannot take %s; supply %s, which the server exchanges for a token",
				node.Value, dsKeyDropboxAuthorizationCode)
		}
	}
	return badSecretsFile("unknown key %s; valid keys are %s; set other fields with --set",
		unknownYAMLKey(node, secretsFileKeys), strings.Join(secretsFileKeys, ", "))
}

// noSecretsInFile is the exit-2 error for a --secrets-file with no key.
func noSecretsInFile() error {
	return qerrors.NewBadUsagef("--%s holds no secret; leave the flag out to supply none", secretsFileFlag)
}

// badSecretsFile is the exit-2 error for a malformed --secrets-file.
func badSecretsFile(format string, args ...any) error {
	return qerrors.NewBadUsagef("--%s: %s", secretsFileFlag, fmt.Sprintf(format, args...))
}

// missingCopySecrets returns the names in resupply that supplied does not
// hold, in resupply's order.
func missingCopySecrets(resupply []string, supplied map[string]string) []string {
	var missing []string
	for _, name := range resupply {
		if _, ok := supplied[name]; !ok {
			missing = append(missing, name)
		}
	}
	return missing
}

// refuseMissingCopySecrets returns an exit-2 error when the copy of source
// that overrides describes is active or disabled and lacks a name in
// resupply, or nil otherwise.
func refuseMissingCopySecrets(source *cloud_sources.CloudSourceTiny, resupply []string, overrides cloud_sources.CopyOverrides) error {
	state := string(overrides.State)
	missing := missingCopySecrets(resupply, overrides.Secrets)
	if !needsEverySecret(state) || len(missing) == 0 {
		return nil
	}
	pronoun := secretsPronoun(missing)
	return qerrors.NewBadUsagef("%s of cloud source %d (%s) needs every secret the source holds, and this one lacks %s; "+
		"add %s to --%s (a value of exactly ${VAR} is read from the environment), "+
		"or pass --%s draft to save the copy without %s",
		copyInState(state), source.ID, planCell(source.Name), planNames(missing),
		pronoun, secretsFileFlag, stateFlag, pronoun)
}
