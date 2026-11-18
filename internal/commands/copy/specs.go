// Package copycmd implements the `qctl copy` command group.
package copycmd

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/api"
	qerrors "github.com/qlustered/qctl/internal/errors"
)

// Flag names, without the leading dashes.
const (
	includeFlag = "include"
	excludeFlag = "exclude"
)

// Non-secret keys of a --data-sources-file entry other than source_id, in wire
// spelling.
const (
	dsKeyName           = "name"
	dsKeyState          = "state"
	dsKeySchedule       = "schedule"
	dsKeyPattern        = "pattern"
	dsKeyArchivePattern = "archive_pattern"
	dsKeyS3Prefix       = "s3_prefix"
	dsKeySftpFolder     = "sftp_folder"
	dsKeyGsPrefix       = "gs_prefix"
	dsKeyExcelSheetName = "excel_sheet_name"
)

// dsKeySimpleURL is the secret key of a --data-sources-file entry that
// supplies a URL, and the location_field of a source that reads one.
const dsKeySimpleURL = "simple_url"

// dataSourceDistinguishingKeys are the --data-sources-file keys that a copied
// source's distinguishing_fields may list.
var dataSourceDistinguishingKeys = []string{
	dsKeyPattern, dsKeyArchivePattern, dsKeyS3Prefix, dsKeySftpFolder, dsKeyGsPrefix, dsKeyExcelSheetName,
}

// dataSourceKeys are the non-secret --data-sources-file keys other than
// source_id.
var dataSourceKeys = append([]string{dsKeyName, dsKeyState, dsKeySchedule}, dataSourceDistinguishingKeys...)

// dataSourceStates are the values of the --data-sources-file state key.
var dataSourceStates = []api.DatasetCopyDataSourceRequestState{
	api.DatasetCopyDataSourceRequestStateActive,
	api.DatasetCopyDataSourceRequestStateDraft,
	api.DatasetCopyDataSourceRequestStateDisabled,
}

// parseCopyOptions parses the values of --include or --exclude (flag, without
// dashes) into copy options, deduplicated, in first-seen order. Each value may
// be a comma-separated list, and an option may be spelled with hyphens
// (value-maps). It returns nil when values is empty.
func parseCopyOptions(flag string, values []string) ([]api.DatasetCopyOption, error) {
	if len(values) == 0 {
		return nil, nil
	}
	valid := api.EnumValues[api.DatasetCopyOption]()
	var options []api.DatasetCopyOption
	for _, value := range values {
		for _, element := range strings.Split(value, ",") {
			name := strings.ReplaceAll(strings.TrimSpace(element), "-", "_")
			if name == "" {
				return nil, qerrors.NewBadUsagef("invalid value %q for --%s: empty option name", value, flag)
			}
			if !slices.Contains(valid, name) {
				return nil, qerrors.NewBadUsagef("invalid value %q for --%s: must be one of %s",
					strings.TrimSpace(element), flag, strings.Join(valid, ", "))
			}
			if option := api.DatasetCopyOption(name); !slices.Contains(options, option) {
				options = append(options, option)
			}
		}
	}
	return options, nil
}

// resolveInclude returns the include list of a copy request, in the order of
// offered: every option offered as default or always when include and exclude
// are both empty; otherwise the options in include plus every always option;
// otherwise the default and always options minus exclude. The result is never
// nil.
func resolveInclude(offered []api.DatasetCopyOptionInfo, include, exclude []api.DatasetCopyOption) ([]api.DatasetCopyOption, error) {
	if len(include) > 0 && len(exclude) > 0 {
		return nil, qerrors.NewBadUsagef("--%s and --%s are mutually exclusive", includeFlag, excludeFlag)
	}
	if err := requireOfferedOptions(includeFlag, include, offered); err != nil {
		return nil, err
	}
	if err := requireOfferedOptions(excludeFlag, exclude, offered); err != nil {
		return nil, err
	}
	for _, info := range offered {
		if info.Always && slices.Contains(exclude, info.Option) {
			return nil, qerrors.NewBadUsagef("invalid value %q for --%s: %s is always copied",
				info.Option, excludeFlag, info.Option)
		}
	}
	result := make([]api.DatasetCopyOption, 0, len(offered))
	for _, info := range offered {
		var carried bool
		switch {
		case info.Always:
			carried = true
		case len(include) > 0:
			carried = slices.Contains(include, info.Option)
		default:
			carried = info.Default && !slices.Contains(exclude, info.Option)
		}
		if carried {
			result = append(result, info.Option)
		}
	}
	return result, nil
}

// requireOfferedOptions refuses any of options that offered does not list.
func requireOfferedOptions(flag string, options []api.DatasetCopyOption, offered []api.DatasetCopyOptionInfo) error {
	for _, option := range options {
		if slices.ContainsFunc(offered, func(info api.DatasetCopyOptionInfo) bool { return info.Option == option }) {
			continue
		}
		names := make([]string, 0, len(offered))
		for _, info := range offered {
			names = append(names, string(info.Option))
		}
		available := "the table offers no copy options"
		if len(names) > 0 {
			available = "the table offers " + strings.Join(names, ", ")
		}
		return qerrors.NewBadUsagef("invalid value %q for --%s: %s", option, flag, available)
	}
	return nil
}

// dataSourceSpec is one entry of --data-sources-file.
type dataSourceSpec struct {
	// Entry is the entry's position in the file, counted from 1.
	Entry int
	// ID is the id of the data source to copy.
	ID int
	// Values maps each non-secret key given other than source_id, in wire
	// spelling, to its value.
	Values map[string]string
	// Secrets maps each secret key given, in wire spelling, to its value, read
	// from the environment for an exact ${VAR}.
	Secrets map[string]string
}

// buildDataSourceRequests returns one request per spec, in order, each setting
// exactly the keys its spec gives. It refuses a spec whose id offered does not
// list, a distinguishing key its source does not list, and an unchanged copy
// of an active source whose location_field is set and is not simple_url: one
// whose given distinguishing keys all equal the source's current values. It
// also refuses names already used in this copy and copies of the same source
// that would read the same files as an earlier active copy.
func buildDataSourceRequests(offered []api.DatasetCopyDataSourceInfo, specs []dataSourceSpec, copyName string) ([]api.DatasetCopyDataSourceRequest, error) {
	requests := make([]api.DatasetCopyDataSourceRequest, 0, len(specs))
	seenNames := map[string]bool{copyName + " Source": true}
	activeCopies := make(map[int][]dataSourceSpec)
	for _, spec := range specs {
		source, found := offeredDataSource(offered, spec.ID)
		if !found {
			return nil, badDataSourceEntry(spec, "data source %d is not one this table's copy can take; %s",
				spec.ID, copyableDataSources(offered))
		}
		fields := sortedDistinguishingFields(source)
		changed := false
		for _, key := range dataSourceDistinguishingKeys {
			value, given := spec.Values[key]
			if !given {
				continue
			}
			current, listed := source.DistinguishingFields[key]
			if !listed {
				return nil, badDataSourceEntry(spec, "%s is not a distinguishing field of data source %d (%s), a %s source; %s",
					key, source.ID, source.Name, source.DataSourceType, describeDistinguishingFields(fields))
			}
			if current == nil || *current != value {
				changed = true
			}
		}
		if source.State == api.DataSourceStateActive && source.LocationField != nil && !readsSimpleURL(source) && !changed {
			return nil, badDataSourceEntry(spec, "data source %d (%s) is active, so a copy reads the same files "+
				"unless it changes at least one of %s", source.ID, source.Name, strings.Join(fields, ", "))
		}
		if err := checkCopyCollisions(source, spec, fields, seenNames, activeCopies); err != nil {
			return nil, err
		}
		requests = append(requests, newDataSourceRequest(spec))
	}
	return requests, nil
}

// checkCopyCollisions refuses a reused name or the same files as an earlier
// active copy, then records this copy for later checks. Two copies of a
// simple_url source read the same files only when both supply the same
// simple_url.
func checkCopyCollisions(source api.DatasetCopyDataSourceInfo, spec dataSourceSpec, fields []string, seenNames map[string]bool, activeCopies map[int][]dataSourceSpec) error {
	name := source.Name
	if given, ok := spec.Values[dsKeyName]; ok {
		name = given
	}
	if seenNames[name] {
		return badDataSourceEntry(spec, "name %q is already used by another data source of the copy", name)
	}
	seenNames[name] = true
	if source.LocationField != nil {
		for _, earlier := range activeCopies[source.ID] {
			if !sameCopyFields(source.DistinguishingFields, earlier, spec) {
				continue
			}
			if !readsSimpleURL(source) {
				return badDataSourceEntry(spec, "data source %d would read the same files as an earlier active copy; change at least one of %s",
					source.ID, strings.Join(fields, ", "))
			}
			if sameSuppliedURL(earlier, spec) {
				return badDataSourceEntry(spec, "data source %d would read the same files as an earlier active copy that supplies "+
					"the same %s; change %s or at least one of %s", source.ID, dsKeySimpleURL, dsKeySimpleURL, strings.Join(fields, ", "))
			}
		}
	}
	if copyState(source, spec) == string(api.DataSourceStateActive) {
		activeCopies[source.ID] = append(activeCopies[source.ID], spec)
	}
	return nil
}

// copyState returns the state of spec's copy of source: the state spec gives,
// else source's.
func copyState(source api.DatasetCopyDataSourceInfo, spec dataSourceSpec) string {
	if given, ok := spec.Values[dsKeyState]; ok {
		return given
	}
	return string(source.State)
}

// readsSimpleURL reports whether source's location_field is simple_url.
func readsSimpleURL(source api.DatasetCopyDataSourceInfo) bool {
	return source.LocationField != nil && *source.LocationField == dsKeySimpleURL
}

// sameSuppliedURL reports whether first and second both supply simple_url,
// with the same value.
func sameSuppliedURL(first, second dataSourceSpec) bool {
	left, leftGiven := first.Secrets[dsKeySimpleURL]
	right, rightGiven := second.Secrets[dsKeySimpleURL]
	return leftGiven && rightGiven && left == right
}

// sameCopyFields reports whether two copies of source have the same final
// distinguishing fields after their overrides.
func sameCopyFields(source map[string]*string, first, second dataSourceSpec) bool {
	for key, original := range source {
		left, leftGiven := first.Values[key]
		right, rightGiven := second.Values[key]
		switch {
		case leftGiven && rightGiven:
			if left != right {
				return false
			}
		case leftGiven:
			if original == nil || left != *original {
				return false
			}
		case rightGiven:
			if original == nil || right != *original {
				return false
			}
		}
	}
	return true
}

// newDataSourceRequest returns the request for spec, with a pointer for exactly
// the keys and secrets it gives.
func newDataSourceRequest(spec dataSourceSpec) api.DatasetCopyDataSourceRequest {
	request := api.DatasetCopyDataSourceRequest{SourceID: spec.ID}
	for key, value := range spec.Values {
		switch key {
		case dsKeyName:
			request.Name = &value
		case dsKeyState:
			state := api.DatasetCopyDataSourceRequestState(value)
			request.State = &state
		case dsKeySchedule:
			request.Schedule = &value
		case dsKeyPattern:
			request.Pattern = &value
		case dsKeyArchivePattern:
			request.ArchivePattern = &value
		case dsKeyS3Prefix:
			request.S3Prefix = &value
		case dsKeySftpFolder:
			request.SftpFolder = &value
		case dsKeyGsPrefix:
			request.GsPrefix = &value
		case dsKeyExcelSheetName:
			request.ExcelSheetName = &value
		}
	}
	for key, value := range spec.Secrets {
		if field := secretRequestField(&request, key); field != nil {
			*field = &value
		}
	}
	return request
}

// secretRequestField returns the field of request that the secret key sets,
// or nil when key names none.
func secretRequestField(request *api.DatasetCopyDataSourceRequest, key string) **string {
	switch key {
	case dsKeyS3SecretKey:
		return &request.S3SecretKey
	case "sftp_password":
		return &request.SftpPassword
	case "sftp_ssh_key":
		return &request.SftpSSHKey
	case "sftp_ssh_key_passphrase":
		return &request.SftpSSHKeyPassphrase
	case "gpg_private_key":
		return &request.GpgPrivateKey
	case "gpg_passphrase":
		return &request.GpgPassphrase
	case "file_password":
		return &request.FilePassword
	case "gs_service_account_key":
		return &request.GsServiceAccountKey
	case dsKeyDropboxAccessToken:
		return &request.DropboxAccessToken
	case dsKeyDropboxAuthorizationCode:
		return &request.DropboxAuthorizationCode
	case dsKeySimpleURL:
		return &request.SimpleURL
	}
	return nil
}

// copyableDataSources lists offered as "ID (name)" for an error message.
func copyableDataSources(offered []api.DatasetCopyDataSourceInfo) string {
	if len(offered) == 0 {
		return "the table has no data source a copy can take"
	}
	names := make([]string, 0, len(offered))
	for _, source := range offered {
		names = append(names, fmt.Sprintf("%d (%s)", source.ID, source.Name))
	}
	return "copyable: " + strings.Join(names, ", ")
}

// sortedDistinguishingFields returns the keys of source's distinguishing
// fields, sorted.
func sortedDistinguishingFields(source api.DatasetCopyDataSourceInfo) []string {
	fields := make([]string, 0, len(source.DistinguishingFields))
	for field := range source.DistinguishingFields {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	return fields
}

// describeDistinguishingFields names fields for an error message.
func describeDistinguishingFields(fields []string) string {
	if len(fields) == 0 {
		return "it has no distinguishing fields"
	}
	return "its distinguishing fields are " + strings.Join(fields, ", ")
}

// joinStates lists dataSourceStates for an error message.
func joinStates() string {
	names := make([]string, 0, len(dataSourceStates))
	for _, state := range dataSourceStates {
		names = append(names, string(state))
	}
	return strings.Join(names, ", ")
}
