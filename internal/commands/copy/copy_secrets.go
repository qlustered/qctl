package copycmd

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Secret keys of a --data-sources-file entry with a rule of their own.
const (
	dsKeyS3SecretKey              = "s3_secret_key"
	dsKeyDropboxAccessToken       = "dropbox_access_token"
	dsKeyDropboxAuthorizationCode = "dropbox_authorization_code"
)

// offeredDataSource returns the data source of offered whose id is id.
func offeredDataSource(offered []api.DatasetCopyDataSourceInfo, id int) (api.DatasetCopyDataSourceInfo, bool) {
	i := slices.IndexFunc(offered, func(source api.DatasetCopyDataSourceInfo) bool { return source.ID == id })
	if i < 0 {
		return api.DatasetCopyDataSourceInfo{}, false
	}
	return offered[i], true
}

// missingSecrets returns the names in source's secrets_to_resupply that spec
// does not supply, in source's order. A supplied dropbox_access_token supplies
// dropbox_authorization_code.
func missingSecrets(source api.DatasetCopyDataSourceInfo, spec dataSourceSpec) []string {
	var missing []string
	for _, name := range source.SecretsToResupply {
		if _, supplied := spec.Secrets[name]; supplied {
			continue
		}
		if _, supplied := spec.Secrets[dsKeyDropboxAccessToken]; supplied && name == dsKeyDropboxAuthorizationCode {
			continue
		}
		missing = append(missing, name)
	}
	return missing
}

// needsEverySecret reports whether a copy in state is refused while it lacks
// a secret its source holds: whether state is active or disabled.
func needsEverySecret(state string) bool {
	return state == string(api.DataSourceStateActive) || state == string(api.DataSourceStateDisabled)
}

// refuseMissingSecrets returns an exit-2 error naming, one line each, every
// spec whose copy is active or disabled and lacks a secret its source holds,
// or nil when there is none.
func refuseMissingSecrets(offered []api.DatasetCopyDataSourceInfo, specs []dataSourceSpec) error {
	var lines []string
	for _, spec := range specs {
		source, ok := offeredDataSource(offered, spec.ID)
		if !ok {
			continue
		}
		state, missing := copyState(source, spec), missingSecrets(source, spec)
		if !needsEverySecret(state) || len(missing) == 0 {
			continue
		}
		pronoun := secretsPronoun(missing)
		lines = append(lines, fmt.Sprintf("%s: %s needs every secret its source holds, and this one lacks %s; "+
			"add %s to the entry (a value of exactly ${VAR} is read from the environment), "+
			"or set state: draft to save the copy without %s",
			dataSourceEntryLabel(spec.Entry, spec.ID), copyInState(state), planNames(missing), pronoun, pronoun))
	}
	if len(lines) == 0 {
		return nil
	}
	return qerrors.NewBadUsage(strings.Join(lines, "\n"))
}

// copySecretsLine names the secrets spec supplies to its copy of source and
// the ones source holds that it lacks, and what a copy in its state does
// without them. It holds no secret value.
func copySecretsLine(source api.DatasetCopyDataSourceInfo, spec dataSourceSpec) string {
	var parts []string
	if len(spec.Secrets) > 0 {
		parts = append(parts, "supplied "+strings.Join(slices.Sorted(maps.Keys(spec.Secrets)), ", "))
	}
	missing := missingSecrets(source, spec)
	if len(missing) == 0 {
		return strings.Join(parts, "; ")
	}
	pronoun := secretsPronoun(missing)
	parts = append(parts, "missing "+planNames(missing))
	switch state := copyState(source, spec); {
	case state == string(api.DataSourceStateDraft):
		parts = append(parts, copyInState(state)+" saves without "+pronoun)
	case needsEverySecret(state):
		parts = append(parts, copyInState(state)+" is refused without "+pronoun)
	}
	if slices.Contains(missing, dsKeyS3SecretKey) {
		parts = append(parts, "the copy keeps s3_access_key, so its connection check signs with an empty secret")
	}
	return strings.Join(parts, "; ")
}

// copyInState names a copy in state, a data source state, for a sentence.
func copyInState(state string) string {
	if state == string(api.DataSourceStateActive) {
		return "an active copy"
	}
	return "a " + planCell(state) + " copy"
}

// secretsPronoun is the pronoun that stands for names in a sentence.
func secretsPronoun(names []string) string {
	if len(names) == 1 {
		return "it"
	}
	return "them"
}
